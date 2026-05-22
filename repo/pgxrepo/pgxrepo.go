// Package pgxrepo provides a pgx-backed implementation of repo.Factory.
//
// Usage:
//
//	pool, _ := pgxpool.New(ctx, dsn)
//
//	factory := pgxrepo.NewFactory(pool, func(tx pgx.Tx) MyRepos {
//	    return MyRepos{
//	        Users:  userrepo.New(tx),
//	        Orders: orderrepo.New(tx),
//	    }
//	})
//
//	result, err := repo.Transact(ctx, factory, func(ctx context.Context, r MyRepos) (Order, error) {
//	    user, err := r.Users.FindByID(ctx, userID)
//	    if err != nil {
//	        return Order{}, err
//	    }
//	    return r.Orders.Create(ctx, user.ID, items)
//	})
package pgxrepo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/barryhennessy/bones/repo"
)

// Beginner is satisfied by *pgxpool.Pool and *pgx.Conn.
type Beginner interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}

// tx wraps pgx.Tx to satisfy repo.Tx.
type tx struct{ inner pgx.Tx }

func (t *tx) Commit(ctx context.Context) error { return t.inner.Commit(ctx) }

func (t *tx) Rollback(ctx context.Context) error {
	err := t.inner.Rollback(ctx)
	if err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return err
	}
	return nil
}

func (t *tx) Close(context.Context) error { return nil }

// Factory is a repo.Factory backed by a pgx connection pool.
type Factory[Repos any] struct {
	db      Beginner
	txOpts  pgx.TxOptions
	builder func(pgx.Tx) Repos
}

// NewFactory constructs a Factory.
// builder receives the live pgx.Tx and must return a Repos value in which
// every sub-repository holds a reference to that tx — and nothing else.
func NewFactory[Repos any](db *pgxpool.Pool, builder func(pgx.Tx) Repos) *Factory[Repos] {
	return NewFactoryWithOptions[Repos](db, pgx.TxOptions{}, builder)
}

// NewFactoryWithOptions is like NewFactory but allows custom TxOptions
// (isolation level, access mode, etc.).
func NewFactoryWithOptions[Repos any](
	db Beginner,
	opts pgx.TxOptions,
	builder func(pgx.Tx) Repos,
) *Factory[Repos] {
	return &Factory[Repos]{db: db, txOpts: opts, builder: builder}
}

// Begin implements repo.Factory.
func (f *Factory[Repos]) Begin(ctx context.Context) (repo.Tx, Repos, error) {
	pgxTx, err := f.db.BeginTx(ctx, f.txOpts)
	if err != nil {
		var zero Repos
		return nil, zero, err
	}
	return &tx{inner: pgxTx}, f.builder(pgxTx), nil
}
