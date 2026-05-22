// Package mongorepo provides a MongoDB-backed implementation of repo.Factory
// using multi-document transactions (requires a replica set or sharded cluster).
//
// Usage:
//
//	client, _ := mongo.Connect(ctx, options.Client().ApplyURI(uri))
//
//	factory := mongorepo.NewFactory(client, func(sessCtx mongo.SessionContext) MyRepos {
//	    return MyRepos{
//	        Users:  userrepo.New(sessCtx, db.Collection("users")),
//	        Orders: orderrepo.New(sessCtx, db.Collection("orders")),
//	    }
//	})
//
//	result, err := repo.Transact(ctx, factory, func(ctx context.Context, r MyRepos) (Order, error) {
//	    // Repositories already hold the SessionContext from construction time.
//	    user, err := r.Users.FindByID(ctx, userID)
//	    if err != nil {
//	        return Order{}, err
//	    }
//	    return r.Orders.Create(ctx, user.ID, items)
//	})
package mongorepo

import (
	"context"

	"go.mongodb.org/mongo-driver/mongo"

	"github.com/barryhennessy/bones/repo"
)

// sessionTx adapts a mongo.Session to repo.Tx.
type sessionTx struct {
	sess     mongo.Session
	sessCtx  mongo.SessionContext
	finished bool // true after successful Commit or successful AbortTransaction
	released bool // true after EndSession
}

func (s *sessionTx) Commit(_ context.Context) error {
	if s.finished {
		return nil
	}
	err := s.sess.CommitTransaction(s.sessCtx)
	if err == nil {
		s.finished = true
	}
	return err
}

func (s *sessionTx) Rollback(_ context.Context) error {
	if s.finished {
		return nil
	}
	err := s.sess.AbortTransaction(s.sessCtx)
	if err == nil {
		s.finished = true
	}
	return err
}

func (s *sessionTx) Close(ctx context.Context) error {
	if s.released {
		return nil
	}
	s.released = true
	s.sess.EndSession(ctx)
	return nil
}

// Factory is a repo.Factory backed by a MongoDB client.
type Factory[Repos any] struct {
	client  *mongo.Client
	builder func(mongo.SessionContext) Repos
}

// NewFactory constructs a Factory.
// builder receives a mongo.SessionContext; every repository it creates must
// use that context for all collection operations so they run within the transaction.
func NewFactory[Repos any](
	client *mongo.Client,
	builder func(mongo.SessionContext) Repos,
) *Factory[Repos] {
	return &Factory[Repos]{client: client, builder: builder}
}

// Begin implements repo.Factory.
// It starts a client session, begins a transaction, and builds repos scoped
// to the resulting SessionContext.
func (f *Factory[Repos]) Begin(ctx context.Context) (repo.Tx, Repos, error) {
	sess, err := f.client.StartSession()
	if err != nil {
		var zero Repos
		return nil, zero, err
	}

	if err = sess.StartTransaction(); err != nil {
		sess.EndSession(ctx)
		var zero Repos
		return nil, zero, err
	}

	sessCtx := mongo.NewSessionContext(ctx, sess)
	st := &sessionTx{sess: sess, sessCtx: sessCtx}
	return st, f.builder(sessCtx), nil
}
