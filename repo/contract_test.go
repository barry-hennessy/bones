package repo_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/barry-hennessy/test/sweet"
	tc "github.com/barry-hennessy/test/sweet/factories/tc"
	tcpostgres "github.com/barry-hennessy/test/sweet/factories/tc/postgres"
	"github.com/docker/go-connections/nat"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/barry-hennessy/bones/repo"
	"github.com/barry-hennessy/bones/repo/mongorepo"
	"github.com/barry-hennessy/bones/repo/pgxrepo"
)

const (
	postgresImage = "postgres:16-alpine"
	mongoImage    = "mongo:7"
	mongoDBName   = "repo_contract"
)

var contractSeq atomic.Uint64

type contractCounterRepo interface {
	Put(context.Context, string, int) error
	Increment(context.Context, string) error
	Value(context.Context, string) (int, error)
}

type contractRepos struct {
	Counters contractCounterRepo
}

type contractDeps struct {
	factory   repo.Factory[contractRepos]
	readValue func(context.Context, string) (int, bool, error)
	seedValue func(context.Context, string, int) error
}

func TestPgxRepoContract(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sweet.Run(t, "pgxrepo", newSweetContainerFactory(ctx, tcpostgres.NewPostgresContainer(postgresImage)), func(t *testing.T, container testcontainers.Container) {
		t.Parallel()

		dsn := postgresDSN(ctx, t, container)
		runContractTests(t, newPostgresDepsFactory(ctx, dsn))
	})
}

func TestMongoRepoContract(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sweet.Run(t, "mongorepo", newSweetContainerFactory(ctx, newMongoReplicaSetContainer(mongoImage)), func(t *testing.T, container testcontainers.Container) {
		t.Parallel()

		uri := mongoURI(ctx, t, container)
		initMongoReplicaSet(ctx, t, uri)
		runContractTests(t, newMongoDepsFactory(ctx, uri))
	})
}

func runContractTests(t *testing.T, factory sweet.DepFactory[contractDeps]) {
	sweet.Run(t, "commit_visible", factory, func(t *testing.T, d contractDeps) {
		t.Parallel()
		runTransactCommitVisible(t, d)
	})

	sweet.Run(t, "rollback_invisible", factory, func(t *testing.T, d contractDeps) {
		t.Parallel()
		runTransactRollbackInvisible(t, d)
	})

	sweet.Run(t, "canceled_context_rolls_back", factory, func(t *testing.T, d contractDeps) {
		t.Parallel()
		runTransactCanceledContextRollsBack(t, d)
	})

	sweet.Run(t, "concurrent_increments_commit", factory, func(t *testing.T, d contractDeps) {
		t.Parallel()
		runConcurrentIncrement(t, d)
	})
}

func runTransactCommitVisible(t *testing.T, d contractDeps) {
	key := uniqueName("commit")

	result, err := repo.Transact(context.Background(), d.factory, func(ctx context.Context, repos contractRepos) (int, error) {
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

func runTransactRollbackInvisible(t *testing.T, d contractDeps) {
	errBoom := errors.New("force rollback")
	firstKey := uniqueName("rollback_first")
	secondKey := uniqueName("rollback_second")

	_, err := repo.Transact(context.Background(), d.factory, func(ctx context.Context, repos contractRepos) (struct{}, error) {
		if err := repos.Counters.Put(ctx, firstKey, 1); err != nil {
			return struct{}{}, err
		}
		if err := repos.Counters.Put(ctx, secondKey, 2); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, errBoom
	})

	require.ErrorIs(t, err, errBoom)

	for _, key := range []string{firstKey, secondKey} {
		value, exists, readErr := d.readValue(context.Background(), key)
		require.NoError(t, readErr)
		assert.False(t, exists)
		assert.Zero(t, value)
	}
}

func runTransactCanceledContextRollsBack(t *testing.T, d contractDeps) {
	key := uniqueName("cancel")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := repo.Transact(ctx, d.factory, func(ctx context.Context, repos contractRepos) (struct{}, error) {
		if err := repos.Counters.Put(ctx, key, 7); err != nil {
			return struct{}{}, err
		}
		cancel()
		return struct{}{}, ctx.Err()
	})

	require.ErrorIs(t, err, context.Canceled)

	_, exists, readErr := d.readValue(context.Background(), key)
	require.NoError(t, readErr)
	assert.False(t, exists)
}

func runConcurrentIncrement(t *testing.T, d contractDeps) {
	key := uniqueName("concurrent")
	require.NoError(t, d.seedValue(context.Background(), key, 0))

	start := make(chan struct{})
	errCh := make(chan error, 2)
	var wg sync.WaitGroup

	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			_, err := repo.Transact(context.Background(), d.factory, func(ctx context.Context, repos contractRepos) (struct{}, error) {
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

func newPostgresDepsFactory(ctx context.Context, dsn string) sweet.DepFactory[contractDeps] {
	return func(t *testing.T) contractDeps {
		pool, err := pgxpool.New(ctx, dsn)
		require.NoError(t, err)
		t.Cleanup(pool.Close)

		table := uniqueName("counters")
		_, err = pool.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s (id text primary key, value integer not null)`, pgIdentifier(table)))
		require.NoError(t, err)

		t.Cleanup(func() {
			_, dropErr := pool.Exec(context.Background(), fmt.Sprintf(`DROP TABLE IF EXISTS %s`, pgIdentifier(table)))
			assert.NoError(t, dropErr)
		})

		return contractDeps{
			factory: pgxrepo.NewFactory(pool, func(tx pgx.Tx) contractRepos {
				return contractRepos{Counters: pgCounterRepo{tx: tx, table: table}}
			}),
			readValue: func(ctx context.Context, key string) (int, bool, error) {
				var value int
				err := pool.QueryRow(ctx, fmt.Sprintf(`SELECT value FROM %s WHERE id = $1`, pgIdentifier(table)), key).Scan(&value)
				if errors.Is(err, pgx.ErrNoRows) {
					return 0, false, nil
				}
				if err != nil {
					return 0, false, err
				}
				return value, true, nil
			},
			seedValue: func(ctx context.Context, key string, value int) error {
				_, err := pool.Exec(ctx,
					fmt.Sprintf(`INSERT INTO %s (id, value) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET value = EXCLUDED.value`, pgIdentifier(table)),
					key,
					value,
				)
				return err
			},
		}
	}
}

func newMongoDepsFactory(ctx context.Context, uri string) sweet.DepFactory[contractDeps] {
	return func(t *testing.T) contractDeps {
		client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
		require.NoError(t, err)
		require.NoError(t, client.Ping(ctx, nil))

		database := client.Database(mongoDBName)
		collectionName := uniqueName("counters")
		require.NoError(t, database.CreateCollection(ctx, collectionName))

		collection := database.Collection(collectionName)
		t.Cleanup(func() {
			assert.NoError(t, collection.Drop(context.Background()))
			assert.NoError(t, client.Disconnect(context.Background()))
		})

		return contractDeps{
			factory: mongorepo.NewFactory(client, func(sessCtx mongo.SessionContext) contractRepos {
				return contractRepos{Counters: mongoCounterRepo{ctx: sessCtx, collection: collection}}
			}),
			readValue: func(ctx context.Context, key string) (int, bool, error) {
				var doc mongoCounterDoc
				err := collection.FindOne(ctx, bson.D{{Key: "_id", Value: key}}).Decode(&doc)
				if errors.Is(err, mongo.ErrNoDocuments) {
					return 0, false, nil
				}
				if err != nil {
					return 0, false, err
				}
				return doc.Value, true, nil
			},
			seedValue: func(ctx context.Context, key string, value int) error {
				_, err := collection.UpdateOne(
					ctx,
					bson.D{{Key: "_id", Value: key}},
					bson.D{{Key: "$set", Value: bson.D{{Key: "value", Value: value}}}},
					options.Update().SetUpsert(true),
				)
				return err
			},
		}
	}
}

type pgCounterRepo struct {
	tx    pgx.Tx
	table string
}

func (r pgCounterRepo) Put(ctx context.Context, key string, value int) error {
	_, err := r.tx.Exec(ctx,
		fmt.Sprintf(`INSERT INTO %s (id, value) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET value = EXCLUDED.value`, pgIdentifier(r.table)),
		key,
		value,
	)
	return err
}

func (r pgCounterRepo) Increment(ctx context.Context, key string) error {
	tag, err := r.tx.Exec(ctx,
		fmt.Sprintf(`UPDATE %s SET value = value + 1 WHERE id = $1`, pgIdentifier(r.table)),
		key,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (r pgCounterRepo) Value(ctx context.Context, key string) (int, error) {
	var value int
	err := r.tx.QueryRow(ctx, fmt.Sprintf(`SELECT value FROM %s WHERE id = $1`, pgIdentifier(r.table)), key).Scan(&value)
	return value, err
}

type mongoCounterRepo struct {
	ctx        mongo.SessionContext
	collection *mongo.Collection
}

type mongoCounterDoc struct {
	ID    string `bson:"_id"`
	Value int    `bson:"value"`
}

func (r mongoCounterRepo) Put(ctx context.Context, key string, value int) error {
	_, err := r.collection.UpdateOne(
		r.ctx,
		bson.D{{Key: "_id", Value: key}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "value", Value: value}}}},
		options.Update().SetUpsert(true),
	)
	return err
}

func (r mongoCounterRepo) Increment(ctx context.Context, key string) error {
	result, err := r.collection.UpdateOne(
		r.ctx,
		bson.D{{Key: "_id", Value: key}},
		bson.D{{Key: "$inc", Value: bson.D{{Key: "value", Value: 1}}}},
	)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return nil
}

func (r mongoCounterRepo) Value(ctx context.Context, key string) (int, error) {
	var doc mongoCounterDoc
	err := r.collection.FindOne(r.ctx, bson.D{{Key: "_id", Value: key}}).Decode(&doc)
	return doc.Value, err
}

type mongoReplicaSetContainer struct {
	image        string
	internalPort string
}

func newSweetContainerFactory(ctx context.Context, c tc.Container) sweet.DepFactory[testcontainers.Container] {
	return func(t *testing.T) testcontainers.Container {
		container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: c.Request(),
			Started:          true,
		})
		if err != nil {
			t.Skipf("docker is required for repo contract tests: %v", err)
			return nil
		}

		t.Cleanup(func() {
			assert.NoError(t, c.Close(context.Background(), container))
		})

		return container
	}
}

func newMongoReplicaSetContainer(image string) *mongoReplicaSetContainer {
	return &mongoReplicaSetContainer{
		image:        image,
		internalPort: "27017/tcp",
	}
}

func (c *mongoReplicaSetContainer) Request() testcontainers.ContainerRequest {
	return testcontainers.ContainerRequest{
		Image:        c.image,
		ExposedPorts: []string{c.internalPort},
		Cmd:          []string{"mongod", "--bind_ip_all", "--replSet", "rs0"},
		WaitingFor: wait.ForAll(
			wait.ForLog("Waiting for connections"),
			wait.ForListeningPort(nat.Port(c.internalPort)),
		),
	}
}

func (c *mongoReplicaSetContainer) Close(ctx context.Context, container testcontainers.Container) error {
	return container.Terminate(ctx)
}

func postgresDSN(ctx context.Context, t *testing.T, container testcontainers.Container) string {
	t.Helper()

	host, err := container.Host(ctx)
	require.NoError(t, err)

	port, err := container.MappedPort(ctx, "5432/tcp")
	require.NoError(t, err)

	return fmt.Sprintf("postgres://postgres:postgres@%s:%s/postgres?sslmode=disable", host, port.Port())
}

func mongoURI(ctx context.Context, t *testing.T, container testcontainers.Container) string {
	t.Helper()

	host, err := container.Host(ctx)
	require.NoError(t, err)

	port, err := container.MappedPort(ctx, nat.Port("27017/tcp"))
	require.NoError(t, err)

	return fmt.Sprintf("mongodb://%s:%s/?connect=direct", host, port.Port())
}

func initMongoReplicaSet(ctx context.Context, t *testing.T, uri string) {
	t.Helper()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, client.Disconnect(context.Background()))
	})

	require.NoError(t, client.Ping(ctx, nil))

	err = client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetInitiate", Value: bson.D{}}}).Err()
	if err != nil && !strings.Contains(err.Error(), "already initialized") {
		require.NoError(t, err)
	}

	require.Eventually(t, func() bool {
		var status bson.M
		runErr := client.Database("admin").RunCommand(context.Background(), bson.D{{Key: "isMaster", Value: 1}}).Decode(&status)
		if runErr != nil {
			return false
		}

		if isMaster, ok := status["ismaster"].(bool); ok && isMaster {
			return true
		}
		if isWritablePrimary, ok := status["isWritablePrimary"].(bool); ok && isWritablePrimary {
			return true
		}
		return false
	}, 30*time.Second, 500*time.Millisecond)
}

func uniqueName(prefix string) string {
	var b strings.Builder
	b.Grow(len(prefix) + 24)
	b.WriteString("repo_")

	for _, r := range strings.ToLower(prefix) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}

	fmt.Fprintf(&b, "_%d", contractSeq.Add(1))
	return b.String()
}

func pgIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func drainReader(t *testing.T, reader io.Reader) string {
	t.Helper()

	if reader == nil {
		return ""
	}

	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	return string(data)
}
