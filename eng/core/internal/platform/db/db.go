// Package db owns the Postgres pool, the migrations and the two kinds of
// transaction the rest of core may open:
//
//   - InTenant: the default for every business module. Runs as the
//     restricted role tramo_app with app.tenant_id and app.user_id set, so
//     row level security filters every table by tenant even if a query
//     forgets to.
//   - System: runs as tramo_system, which reaches only the platform and
//     identity tables granted to it in migration 00003 (registration,
//     login, refresh tokens, idempotency, tenant keys, audit). It has no
//     access to any business table. Which packages may call it is an
//     allowlist enforced by a test (TestSystemCallersAreAllowlisted).
//
// The pool itself connects as the schema owner, used only to migrate.
// Every table has FORCE ROW LEVEL SECURITY, so on a non superuser owner a
// stray query on the raw pool sees no rows. See ADR 0002.
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

// Roles the transactions switch to (migrations 00002 and 00003).
const (
	AppRole    = "tramo_app"
	SystemRole = "tramo_system"
)

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
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS,
		goose.WithGoMigrations(migrations.Go...))
	if err != nil {
		return fmt.Errorf("db: migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("db: migrate up: %w", err)
	}
	return nil
}

// Scope is who is acting, on which tenant and with which role.
//
// Role is the caller's role in TenantID as stated by the verified access
// token (identity.Authenticate). It never comes from the request body,
// headers or query: modules that need it (who may approve, who decided)
// read it from here and nowhere else. Empty for jobs. The token is valid
// for up to 10 minutes, so a demotion takes effect at the next refresh; an
// action that must see the role as of now re-reads the membership in its
// tenant transaction.
type Scope struct {
	TenantID uuid.UUID
	UserID   uuid.UUID // uuid.Nil for jobs acting on behalf of the tenant
	Role     string
}

type scopeKey struct{}

// WithScope stores the tenant scope in the context. Only the auth
// middleware (identity.Authenticate) and the job runner call it; modules
// never build a scope by hand.
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
		// app.role lets policies and triggers check the role too:
		// current_setting('app.role', true).
		if _, err := tx.Exec(ctx,
			"select set_config('app.tenant_id', $1, true), set_config('app.user_id', $2, true), set_config('app.role', $3, true)",
			s.TenantID.String(), user, s.Role); err != nil {
			return fmt.Errorf("db: set scope: %w", err)
		}
		return fn(tx)
	})
}

// InboxRole may only run inbox.address_tenant (migration 00201).
const InboxRole = "tramo_inbox"

// InboxTenant returns the tenant whose active mailbox address has the local
// part code. It runs one fixed query as tramo_inbox, a role whose only
// privilege is that function: no caller can run anything else through it.
// It exists because mail arrives before any tenant scope does.
func (d *DB) InboxTenant(ctx context.Context, code string) (uuid.UUID, bool, error) {
	var tenant *uuid.UUID
	err := pgx.BeginFunc(ctx, d.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "set local role "+InboxRole); err != nil {
			return fmt.Errorf("db: set role: %w", err)
		}
		return tx.QueryRow(ctx, "select inbox.address_tenant($1)", code).Scan(&tenant)
	})
	if err != nil || tenant == nil {
		return uuid.Nil, false, err
	}
	return *tenant, true, nil
}

// System runs fn as tramo_system. Reserved for platform and identity (see
// the allowlist test); business modules must use InTenant.
func (d *DB) System(ctx context.Context, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, d.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "set local role "+SystemRole); err != nil {
			return fmt.Errorf("db: set role: %w", err)
		}
		return fn(tx)
	})
}
