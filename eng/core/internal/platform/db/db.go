// Package db owns the Postgres pool, the migrations and the two kinds of
// transaction the rest of core may open:
//
//   - InTenant: the default for every business module. Runs as the
//     restricted role tramo_app with app.tenant_id and app.user_id set, so
//     row level security filters every table by tenant even if a query
//     forgets to.
//   - System: runs as the schema owner and bypasses RLS. Only platform and
//     identity use it (registration, login, refresh tokens, idempotency),
//     always with explicit keys in the WHERE clause.
//
// See ADR 0002.
package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/Tramo-Labs/hq/eng/core/migrations"
)

// AppRole is the restricted role tenant transactions switch to.
const AppRole = "tramo_app"

// ErrNoScope means a tenant transaction was requested without a tenant in
// the context. It is a programming error, never a client error.
var ErrNoScope = errors.New("db: no tenant scope in context")

// DB wraps the pool.
type DB struct {
	Pool *pgxpool.Pool
}

// Open connects and pings.
func Open(ctx context.Context, url string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("db: parse url: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return &DB{Pool: pool}, nil
}

// Close releases the pool.
func (d *DB) Close() { d.Pool.Close() }

// Ping checks the database for readiness probes.
func (d *DB) Ping(ctx context.Context) error { return d.Pool.Ping(ctx) }

// Migrate applies every pending migration embedded in the binary.
func (d *DB) Migrate(ctx context.Context) error {
	sqlDB := stdlib.OpenDBFromPool(d.Pool)
	defer func() { _ = sqlDB.Close() }()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		return fmt.Errorf("db: migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("db: migrate up: %w", err)
	}
	return nil
}

// Scope is who is acting and on which tenant.
type Scope struct {
	TenantID uuid.UUID
	UserID   uuid.UUID // uuid.Nil for jobs acting on behalf of the tenant
}

type scopeKey struct{}

// WithScope stores the tenant scope in the context. The auth middleware and
// the job runner call it; modules never build a scope by hand.
func WithScope(ctx context.Context, s Scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, s)
}

// ScopeFrom returns the scope stored in the context.
func ScopeFrom(ctx context.Context) (Scope, bool) {
	s, ok := ctx.Value(scopeKey{}).(Scope)
	return s, ok && s.TenantID != uuid.Nil
}

// InTenant runs fn in a transaction restricted to the tenant in the context.
func (d *DB) InTenant(ctx context.Context, fn func(pgx.Tx) error) error {
	s, ok := ScopeFrom(ctx)
	if !ok {
		return ErrNoScope
	}
	return pgx.BeginFunc(ctx, d.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "set local role "+AppRole); err != nil {
			return fmt.Errorf("db: set role: %w", err)
		}
		user := ""
		if s.UserID != uuid.Nil {
			user = s.UserID.String()
		}
		if _, err := tx.Exec(ctx,
			"select set_config('app.tenant_id', $1, true), set_config('app.user_id', $2, true)",
			s.TenantID.String(), user); err != nil {
			return fmt.Errorf("db: set scope: %w", err)
		}
		return fn(tx)
	})
}

// System runs fn in an owner transaction that bypasses RLS. Reserved for
// platform and identity; business modules must use InTenant.
func (d *DB) System(ctx context.Context, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, d.Pool, fn)
}
