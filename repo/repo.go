// Package repo provides a generic transaction abstraction for repository patterns.
// It decouples transaction lifecycle management from repository implementations,
// supporting any backend (PostgreSQL via pgx, MongoDB, SQLite, etc.).
package repo

import "context"

// Tx represents a database transaction. Implementations are responsible for
// managing the underlying transaction handle (e.g. pgx.Tx, mongo.Session).
type Tx interface {
	// Commit persists all operations performed within this transaction.
	Commit(ctx context.Context) error

	// Rollback aborts all operations performed within this transaction.
	// Implementations should be idempotent: calling Rollback after Commit is a no-op.
	Rollback(ctx context.Context) error

	// Close releases resources held by the transaction handle after Commit or Rollback
	// has completed (e.g. MongoDB server session).
	// Transact calls Close exactly once on exit; implementations should be safe if
	// Close is invoked when the transaction is already fully released.
	Close(ctx context.Context) error
}

// Factory opens transactions and wires repositories to them.
// Each call to Begin must return a new Tx and a fresh set of repositories
// bound exclusively to that transaction.
//
// Example implementations:
//   - PgxFactory: begins a pgx.Tx and constructs pgx-backed repositories
//   - MongoFactory: starts a mongo.Session and constructs session-scoped repositories
type Factory[Repos any] interface {
	// Begin starts a new transaction and returns the transaction handle
	// together with a Repos value whose sub-repositories are all scoped to it.
	Begin(ctx context.Context) (Tx, Repos, error)
}

// DirectFactory opens repositories for backends that do not expose a meaningful
// transaction boundary. Open returns a fresh Repos value together with a release
// function that cleans up any resources acquired for that call.
type DirectFactory[Repos any] interface {
	Open(ctx context.Context) (Repos, func(context.Context) error, error)
}

// Transact runs fn inside a single transaction obtained from f.
//
// After a successful Begin, Transact defers Rollback then Close so that on every
// exit (including panic) Rollback runs before Close. Rollback must be idempotent
// after a successful Commit.
//
// If fn returns a non-nil error the transaction is rolled back and that error
// is returned. If fn returns nil the transaction is committed; any commit error
// is returned to the caller.
//
// The context passed to fn is the same context given to Transact, allowing
// callers to propagate deadlines and cancellations into repository calls.
func Transact[Repos any, Result any](
	ctx context.Context,
	f Factory[Repos],
	fn func(ctx context.Context, repos Repos) (Result, error),
) (Result, error) {
	tx, repos, err := f.Begin(ctx)
	if err != nil {
		var zero Result
		return zero, err
	}

	defer func() { _ = tx.Close(ctx) }()
	defer func() { _ = tx.Rollback(ctx) }()

	result, err := fn(ctx, repos)
	if err != nil {
		var zero Result
		return zero, err
	}

	if err = tx.Commit(ctx); err != nil {
		var zero Result
		return zero, err
	}

	return result, nil
}

// Run executes fn with repositories opened from f, without introducing any
// transaction, commit, or rollback semantics. It is intended for backends that
// only support direct, non-isolated operations.
//
// After a successful Open, Run defers the returned release function so cleanup
// still happens on every exit, including panic. If fn returns a non-nil error,
// that error is returned directly.
func Run[Repos any, Result any](
	ctx context.Context,
	f DirectFactory[Repos],
	fn func(ctx context.Context, repos Repos) (Result, error),
) (Result, error) {
	repos, release, err := f.Open(ctx)
	if err != nil {
		var zero Result
		return zero, err
	}

	if release != nil {
		defer func() { _ = release(ctx) }()
	}

	result, err := fn(ctx, repos)
	if err != nil {
		var zero Result
		return zero, err
	}

	return result, nil
}
