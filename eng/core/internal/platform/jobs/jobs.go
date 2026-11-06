// Package jobs is the background job queue: River on Postgres (ADR 0006).
//
//   - Jobs live in the `river` schema and are reached only as the role
//     tramo_jobs, through a pool of their own (NewPool). That pool cannot see
//     any business or identity table.
//   - A job is unique by its natural key: its InsertOpts declare UniqueOpts
//     (ByArgs, ByPeriod, ...), and inserting the same key again is skipped, not
//     run twice (Insert reports it).
//   - Business work inside a job goes through db.InTenant with the tenant the
//     job carries in its arguments (TenantContext); a job never sees another
//     tenant's rows.
//
// The api process inserts with an insert-only client (NewInserter); the
// worker process runs the queues (NewWorker).
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/migrations"
)

// Role is the database role every queue connection runs as.
const Role = "tramo_jobs"

// NewPool opens the queue's own pool. Every connection switches to
// tramo_jobs at connect time, so River's queries reach the river schema and
// nothing else.
func NewPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("jobs: parse url: %w", err)
	}
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "set role "+Role)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("jobs: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("jobs: ping: %w", err)
	}
	return pool, nil
}

// Client is a River client bound to the queue pool.
type Client = river.Client[pgx.Tx]

// NewInserter returns a client that only inserts jobs (the api process).
func NewInserter(pool *pgxpool.Pool) (*Client, error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Schema: migrations.RiverSchema,
		// Kinds are registered in the worker; the inserter does not need them.
		SkipUnknownJobCheck: true,
	})
}

// WorkerConfig describes what the worker process runs.
type WorkerConfig struct {
	Workers      *river.Workers
	Periodic     []*river.PeriodicJob
	MaxWorkers   int
	FetchPoll    time.Duration // 0 keeps River's default
	Logger       *slog.Logger
	TestOnlyMode bool // tests: no leader election delay
}

// NewWorker returns a client that runs the default queue.
func NewWorker(pool *pgxpool.Pool, c WorkerConfig) (*Client, error) {
	if c.MaxWorkers <= 0 {
		c.MaxWorkers = 10
	}
	cfg := &river.Config{
		Schema:       migrations.RiverSchema,
		Workers:      c.Workers,
		PeriodicJobs: c.Periodic,
		Queues:       map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: c.MaxWorkers}},
		Logger:       c.Logger,
		TestOnly:     c.TestOnlyMode,
	}
	if c.FetchPoll > 0 {
		cfg.FetchPollInterval = c.FetchPoll
		cfg.FetchCooldown = c.FetchPoll / 2
	}
	return river.NewClient(riverpgxv5.New(pool), cfg)
}

// Insert enqueues args and reports whether a new job was created (false:
// a job with the same unique key already exists, nothing new will run).
func Insert(ctx context.Context, c *Client, args river.JobArgs) (bool, error) {
	res, err := c.Insert(ctx, args, nil)
	if err != nil {
		return false, err
	}
	return !res.UniqueSkippedAsDuplicate, nil
}

// TenantContext is the scope a job works under: the tenant in its
// arguments, no user, no role. Jobs build their scope only through here.
func TenantContext(ctx context.Context, tenant uuid.UUID) context.Context {
	return db.WithScope(ctx, db.Scope{TenantID: tenant})
}
