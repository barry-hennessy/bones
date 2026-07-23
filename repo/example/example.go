// Package example demonstrates end-to-end usage of the repo library
// with a pgx backend: two repositories (users, orders) composed inside
// a single atomic transaction by a service that never touches pgx directly.
package example

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/barry-hennessy/bones/repo"
	"github.com/barry-hennessy/bones/repo/pgxrepo"
)

// ─── Domain types ────────────────────────────────────────────────────────────

type User struct {
	ID   int64
	Name string
}

type Order struct {
	ID     int64
	UserID int64
	Total  float64
}

// ─── Repository interfaces ───────────────────────────────────────────────────

type UserRepository interface {
	FindByID(ctx context.Context, id int64) (User, error)
}

type OrderRepository interface {
	Create(ctx context.Context, userID int64, total float64) (Order, error)
}

// ─── pgx-backed implementations ──────────────────────────────────────────────
// Each struct holds ONLY a pgx.Tx — no pool, no conn, no global state.

type pgxUserRepo struct{ tx pgx.Tx }

func (r *pgxUserRepo) FindByID(ctx context.Context, id int64) (User, error) {
	var u User
	err := r.tx.QueryRow(ctx,
		`SELECT id, name FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Name)
	return u, err
}

type pgxOrderRepo struct{ tx pgx.Tx }

func (r *pgxOrderRepo) Create(ctx context.Context, userID int64, total float64) (Order, error) {
	var o Order
	err := r.tx.QueryRow(ctx,
		`INSERT INTO orders (user_id, total) VALUES ($1, $2) RETURNING id, user_id, total`,
		userID, total,
	).Scan(&o.ID, &o.UserID, &o.Total)
	return o, err
}

// ─── Repos bundle ─────────────────────────────────────────────────────────────

type AppRepos struct {
	Users  UserRepository
	Orders OrderRepository
}

// ─── Factory wiring ───────────────────────────────────────────────────────────

func NewFactory(pool *pgxpool.Pool) repo.Factory[AppRepos] {
	return pgxrepo.NewFactory(pool, func(tx pgx.Tx) AppRepos {
		return AppRepos{
			Users:  &pgxUserRepo{tx: tx},
			Orders: &pgxOrderRepo{tx: tx},
		}
	})
}

// ─── Service layer ────────────────────────────────────────────────────────────
// The service receives a Factory and calls repo.Transact.
// It never imports pgx, never calls Rollback, never sees a connection pool.

type OrderService struct {
	factory repo.Factory[AppRepos]
}

func NewOrderService(factory repo.Factory[AppRepos]) *OrderService {
	return &OrderService{factory: factory}
}

func (s *OrderService) PlaceOrder(ctx context.Context, userID int64, total float64) (Order, error) {
	return repo.Transact(ctx, s.factory, func(ctx context.Context, r AppRepos) (Order, error) {
		user, err := r.Users.FindByID(ctx, userID)
		if err != nil {
			return Order{}, fmt.Errorf("user %d not found: %w", userID, err)
		}
		order, err := r.Orders.Create(ctx, user.ID, total)
		if err != nil {
			return Order{}, fmt.Errorf("create order: %w", err)
		}
		return order, nil
	})
}
