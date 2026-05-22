package repo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/barry-hennessy/test/sweet"
	"github.com/docker/go-connections/nat"
	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/barryhennessy/bones/repo"
	"github.com/barryhennessy/bones/repo/opensearchrepo"
)

const openSearchImage = "opensearchproject/opensearch:3.3.1"

type directContractDeps struct {
	factory   repo.DirectFactory[contractRepos]
	readValue func(context.Context, string) (int, bool, error)
	seedValue func(context.Context, string, int) error
}

type opensearchCounterRepo struct {
	client *opensearchapi.Client
	index  string
}

type opensearchCounterDoc struct {
	Value int `json:"value"`
}

type openSearchContainer struct {
	image        string
	internalPort string
}

func TestOpenSearchDirectContract(t *testing.T) {
	ctx := context.Background()
	sweet.Run(t, "opensearchrepo", newSweetContainerFactory(ctx, newOpenSearchContainer(openSearchImage)), func(t *testing.T, container testcontainers.Container) {
		addr := openSearchAddr(ctx, t, container)
		runDirectContractTests(t, newOpenSearchDepsFactory(ctx, addr))
	})
}

func runDirectContractTests(t *testing.T, factory sweet.DepFactory[directContractDeps]) {
	sweet.Run(t, "direct_visible", factory, func(t *testing.T, d directContractDeps) {
		runDirectVisible(t, d)
	})

	sweet.Run(t, "error_leaves_prior_write_visible", factory, func(t *testing.T, d directContractDeps) {
		runDirectErrorLeavesPriorWriteVisible(t, d)
	})

	sweet.Run(t, "concurrent_increments_visible", factory, func(t *testing.T, d directContractDeps) {
		runDirectConcurrentIncrement(t, d)
	})
}

func runDirectVisible(t *testing.T, d directContractDeps) {
	key := uniqueName("direct")

	result, err := repo.Run(context.Background(), d.factory, func(ctx context.Context, repos contractRepos) (int, error) {
		if err := repos.Counters.Put(ctx, key, 41); err != nil {
			return 0, err
		}
		if err := repos.Counters.Increment(ctx, key); err != nil {
			return 0, err
		}
		return repos.Counters.Value(ctx, key)
	})

	require.NoError(t, err)
	assert.Equal(t, 42, result)

	value, exists, err := d.readValue(context.Background(), key)
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, 42, value)
}

func runDirectErrorLeavesPriorWriteVisible(t *testing.T, d directContractDeps) {
	errBoom := errors.New("force direct failure")
	key := uniqueName("direct_partial")

	_, err := repo.Run(context.Background(), d.factory, func(ctx context.Context, repos contractRepos) (struct{}, error) {
		if err := repos.Counters.Put(ctx, key, 1); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, errBoom
	})

	require.ErrorIs(t, err, errBoom)

	value, exists, readErr := d.readValue(context.Background(), key)
	require.NoError(t, readErr)
	require.True(t, exists)
	assert.Equal(t, 1, value)
}

func runDirectConcurrentIncrement(t *testing.T, d directContractDeps) {
	key := uniqueName("direct_concurrent")
	require.NoError(t, d.seedValue(context.Background(), key, 0))

	start := make(chan struct{})
	errCh := make(chan error, 2)
	var wg sync.WaitGroup

	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			_, err := repo.Run(context.Background(), d.factory, func(ctx context.Context, repos contractRepos) (struct{}, error) {
				return struct{}{}, repos.Counters.Increment(ctx, key)
			})
			errCh <- err
		}()
	}

	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}

	value, exists, err := d.readValue(context.Background(), key)
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, 2, value)
}

func newOpenSearchDepsFactory(ctx context.Context, addr string) sweet.DepFactory[directContractDeps] {
	return func(t *testing.T) directContractDeps {
		client, err := opensearchapi.NewClient(opensearchapi.Config{
			Client: opensearch.Config{
				Addresses: []string{addr},
			},
		})
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			_, infoErr := client.Info(ctx, nil)
			return infoErr == nil
		}, 2*time.Minute, 500*time.Millisecond)

		index := uniqueName("counters")
		_, err = client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{
			Index: index,
			Body:  bytes.NewBufferString(`{"mappings":{"properties":{"value":{"type":"integer"}}}}`),
		})
		require.NoError(t, err)

		t.Cleanup(func() {
			_, deleteErr := client.Indices.Delete(context.Background(), opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
			})
			var osErr *opensearch.StructError
			if deleteErr != nil && !(errors.As(deleteErr, &osErr) && osErr.Status == 404) {
				assert.NoError(t, deleteErr)
			}
		})

		return directContractDeps{
			factory: opensearchrepo.NewFactory(client, func(client *opensearchapi.Client) contractRepos {
				return contractRepos{Counters: opensearchCounterRepo{client: client, index: index}}
			}),
			readValue: func(ctx context.Context, key string) (int, bool, error) {
				return openSearchReadValue(ctx, client, index, key)
			},
			seedValue: func(ctx context.Context, key string, value int) error {
				return opensearchCounterRepo{client: client, index: index}.Put(ctx, key, value)
			},
		}
	}
}

func (r opensearchCounterRepo) Put(ctx context.Context, key string, value int) error {
	body, err := json.Marshal(opensearchCounterDoc{Value: value})
	if err != nil {
		return err
	}

	_, err = r.client.Index(ctx, opensearchapi.IndexReq{
		Index:      r.index,
		DocumentID: key,
		Body:       bytes.NewReader(body),
		Params: opensearchapi.IndexParams{
			Refresh: "wait_for",
		},
	})
	return err
}

func (r opensearchCounterRepo) Increment(ctx context.Context, key string) error {
	retryOnConflict := 10
	body := bytes.NewBufferString(`{"script":{"lang":"painless","source":"ctx._source.value += params.delta","params":{"delta":1}}}`)

	_, err := r.client.Update(ctx, opensearchapi.UpdateReq{
		Index:      r.index,
		DocumentID: key,
		Body:       body,
		Params: opensearchapi.UpdateParams{
			Refresh:         "wait_for",
			RetryOnConflict: &retryOnConflict,
		},
	})
	return err
}

func (r opensearchCounterRepo) Value(ctx context.Context, key string) (int, error) {
	value, found, err := openSearchReadValue(ctx, r.client, r.index, key)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, fmt.Errorf("document %q not found", key)
	}
	return value, nil
}

func openSearchReadValue(ctx context.Context, client *opensearchapi.Client, index, key string) (int, bool, error) {
	resp, err := client.Document.Get(ctx, opensearchapi.DocumentGetReq{
		Index:      index,
		DocumentID: key,
	})
	if err != nil {
		var osErr *opensearch.StructError
		if errors.As(err, &osErr) && osErr.Status == 404 {
			return 0, false, nil
		}
		return 0, false, err
	}
	if !resp.Found {
		return 0, false, nil
	}

	var doc opensearchCounterDoc
	if err := json.Unmarshal(resp.Source, &doc); err != nil {
		return 0, false, err
	}
	return doc.Value, true, nil
}

func newOpenSearchContainer(image string) *openSearchContainer {
	return &openSearchContainer{
		image:        image,
		internalPort: "9200/tcp",
	}
}

func (c *openSearchContainer) Request() testcontainers.ContainerRequest {
	return testcontainers.ContainerRequest{
		Image:        c.image,
		ExposedPorts: []string{c.internalPort},
		Env: map[string]string{
			"discovery.type":            "single-node",
			"plugins.security.disabled": "true",
			"OPENSEARCH_JAVA_OPTS":      "-Xms512m -Xmx512m",
		},
		WaitingFor: wait.ForListeningPort(nat.Port(c.internalPort)),
	}
}

func (c *openSearchContainer) Close(ctx context.Context, container testcontainers.Container) error {
	return container.Terminate(ctx)
}

func openSearchAddr(ctx context.Context, t *testing.T, container testcontainers.Container) string {
	t.Helper()

	host, err := container.Host(ctx)
	require.NoError(t, err)

	port, err := container.MappedPort(ctx, nat.Port("9200/tcp"))
	require.NoError(t, err)

	return fmt.Sprintf("http://%s:%s", host, port.Port())
}
