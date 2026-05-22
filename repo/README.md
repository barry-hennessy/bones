# repo

A tiny, generic Go library for running repository operations either inside a real backend transaction or through an explicit direct-execution path, with zero coupling to any specific database driver.

## Core API

```go
// Four methods on Tx.

type Tx interface {
    Commit(ctx context.Context) error
    Rollback(ctx context.Context) error
    Close(ctx context.Context) error
}

type Factory[Repos any] interface {
    Begin(ctx context.Context) (Tx, Repos, error)
}

type DirectFactory[Repos any] interface {
    Open(ctx context.Context) (Repos, func(context.Context) error, error)
}

func Transact[Repos, Result any](
    ctx context.Context,
    f Factory[Repos],
    fn func(ctx context.Context, repos Repos) (Result, error),
) (Result, error)

func Run[Repos, Result any](
    ctx context.Context,
    f DirectFactory[Repos],
    fn func(ctx context.Context, repos Repos) (Result, error),
) (Result, error)
```

`Transact` owns the full lifecycle. After a successful `Begin`, it defers `Close` then defers `Rollback` so that on every exit (including panic) `Rollback` runs before `Close`. `Rollback` must be idempotent after a successful `Commit` (for example, pgx treats rollback-after-commit as closed; Mongo skips a second abort).

| Outcome | Action |
|---|---|
| `Begin` fails | return error immediately (no `Rollback` / `Close`) |
| `fn` returns error | deferred `Rollback`, return error; deferred `Close` |
| `fn` succeeds, `Commit` fails | deferred `Rollback`, return commit error; deferred `Close` |
| `fn` succeeds, `Commit` succeeds | deferred `Rollback` (no-op), return result; deferred `Close` |
| `fn` panics | deferred `Rollback`, deferred `Close`, then panic continues |

`Run` is the direct path for backends that do not provide a meaningful commit/rollback boundary. After a successful `Open`, it defers the returned release function, calls `fn`, and returns `fn`'s result or error without introducing transaction semantics that the backend does not actually have.

## Choosing the right API

- Use `Transact` only when the backend exposes a real commit/abort boundary that maps honestly to `Commit` and `Rollback`.
- Use `Run` for direct, non-isolated operations when the backend has no such boundary.
- Do not fake a `Tx` for databases that cannot roll back cross-operation state.

## Usage

### 1. Define your repository interfaces and Repos bundle

```go
type UserRepository interface {
    FindByID(ctx context.Context, id int64) (User, error)
}

type OrderRepository interface {
    Create(ctx context.Context, userID int64, total float64) (Order, error)
}

// Repos is the type parameter passed to Factory and Transact.
type Repos struct {
    Users  UserRepository
    Orders OrderRepository
}
```

### 2. Implement against a `pgx.Tx` — and nothing else

```go
type pgxUserRepo struct{ tx pgx.Tx }

func (r *pgxUserRepo) FindByID(ctx context.Context, id int64) (User, error) {
    var u User
    err := r.tx.QueryRow(ctx, `SELECT id, name FROM users WHERE id=$1`, id).
        Scan(&u.ID, &u.Name)
    return u, err
}
```

Each repository struct holds **only** its transaction handle. No pool. No connection. No way to bypass the transaction.

### 3. Wire a Factory

```go
factory := pgxrepo.NewFactory(pool, func(tx pgx.Tx) Repos {
    return Repos{
        Users:  &pgxUserRepo{tx: tx},
        Orders: &pgxOrderRepo{tx: tx},
    }
})
```

### 4. Call Transact in your service

```go
order, err := repo.Transact(ctx, factory,
    func(ctx context.Context, r Repos) (Order, error) {
        user, err := r.Users.FindByID(ctx, userID)
        if err != nil {
            return Order{}, err
        }
        return r.Orders.Create(ctx, user.ID, total)
    },
)
```

The service never imports pgx, never calls `Rollback`, never sees a connection pool.

### 5. Call Run for direct execution

```go
result, err := repo.Run(ctx, directFactory,
    func(ctx context.Context, r Repos) (Result, error) {
        return r.Search.Index(ctx, doc)
    },
)
```

This path is explicit: there is cleanup, but no commit, rollback, or isolation guarantee.

## Backend adapters

### pgx (`pgxrepo`)

```go
// Default options
factory := pgxrepo.NewFactory(pool, func(tx pgx.Tx) Repos { ... })

// Custom isolation level
factory := pgxrepo.NewFactoryWithOptions(pool,
    pgx.TxOptions{IsoLevel: pgx.Serializable},
    func(tx pgx.Tx) Repos { ... },
)
```

`NewFactory` accepts `*pgxpool.Pool`; `NewFactoryWithOptions` accepts the broader `Beginner` interface, which is also satisfied by `*pgx.Conn`.

### MongoDB (`mongorepo`)

MongoDB transactions require a replica set or sharded cluster. The adapter starts a `mongo.Session`, begins a transaction, and provides a `mongo.SessionContext` to the builder. Repositories pass this context to every collection operation — that's the only wiring required. This is still a real transaction boundary, even though the implementation is session-based rather than SQL-style.

```go
factory := mongorepo.NewFactory(client, func(sessCtx mongo.SessionContext) Repos {
    return Repos{
        Users:  &mongoUserRepo{sessCtx: sessCtx, coll: db.Collection("users")},
        Orders: &mongoOrderRepo{sessCtx: sessCtx, coll: db.Collection("orders")},
    }
})
```

### Direct backends

For backends without transactions, implement `DirectFactory` instead of `Factory`. That keeps the service layer honest about the fact that operations are direct and non-isolated.

```go
type myDirectFactory[Repos any] struct{ ... }

func (f *myDirectFactory[Repos]) Open(ctx context.Context) (Repos, func(context.Context) error, error) {
    // acquire resources, build repos, return cleanup
}
```

### Roll your own transaction adapter

Two things to implement:

```go
// 1. A Tx adapter for your driver
type myTx struct{ ... }
func (t *myTx) Commit(ctx context.Context) error   { ... }
func (t *myTx) Rollback(ctx context.Context) error { ... } // idempotent after Commit
func (t *myTx) Close(ctx context.Context) error   { ... }

// 2. A Factory
type myFactory[Repos any] struct{ ... }
func (f *myFactory[Repos]) Begin(ctx context.Context) (repo.Tx, Repos, error) {
    // start your transaction, build repos scoped to it, return all three
}
```

## Design principles

- **The transaction is invisible to the service layer.** The `fn` passed to `Transact` receives repos and a context. It cannot commit or roll back — it can only succeed or fail.
- **Direct execution is explicit.** `Run` is for backends that do not provide real rollback semantics; it should never pretend otherwise.
- **Each repository owns exactly one resource: its transaction handle.** A `pgxUserRepo` holds a `pgx.Tx` and nothing else. There is no path to the database that bypasses the transaction.
- **Factories are the seam.** Use `Factory` for real transactions and `DirectFactory` for direct execution. Repository interfaces and service code stay unchanged apart from which runner you call.
- **No reflection, no `interface{}`, no magic.** Everything is expressed in generics; mismatches are compile errors.
