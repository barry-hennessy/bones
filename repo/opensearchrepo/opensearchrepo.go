// Package opensearchrepo provides an OpenSearch-backed implementation of
// repo.DirectFactory for direct, non-transactional repository execution.
package opensearchrepo

import (
	"context"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

// Factory is a repo.DirectFactory backed by an OpenSearch client.
type Factory[Repos any] struct {
	client  *opensearchapi.Client
	builder func(*opensearchapi.Client) Repos
}

// NewFactory constructs a Factory.
// builder receives the live OpenSearch client and must return a Repos value
// whose sub-repositories use that client for direct, non-transactional calls.
func NewFactory[Repos any](
	client *opensearchapi.Client,
	builder func(*opensearchapi.Client) Repos,
) *Factory[Repos] {
	return &Factory[Repos]{client: client, builder: builder}
}

// Open implements repo.DirectFactory.
func (f *Factory[Repos]) Open(context.Context) (Repos, func(context.Context) error, error) {
	return f.builder(f.client), func(context.Context) error { return nil }, nil
}
