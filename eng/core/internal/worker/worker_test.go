package worker_test

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db/dbtest"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/jobs"
	"github.com/Tramo-Labs/hq/eng/core/internal/worker"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

type env struct {
	db   *db.DB
	pool *pgxpool.Pool
}

func newEnv(t *testing.T) env {
	t.Helper()
	d := dbtest.New(t)
	pool, err := jobs.NewPool(context.Background(), dbtest.URL(dbtest.DatabaseName(t, d)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return env{db: d, pool: pool}
}

// start runs a worker with ws until the test ends and returns the stream of
// completed jobs.
func (e env) start(t *testing.T, ws *river.Workers) (*jobs.Client, <-chan *river.Event) {
	t.Helper()
	c, err := jobs.NewWorker(e.pool, jobs.WorkerConfig{Workers: ws, FetchPoll: 50 * time.Millisecond, TestOnlyMode: true})
	if err != nil {
		t.Fatal(err)
	}
	events, cancel := c.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
	ctx, stop := context.WithCancel(context.Background())
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = c.Stop(context.Background())
		stop()
	})
	return c, events
}

// completed waits for n completions of kind and fails on a failed job.
func completed(t *testing.T, events <-chan *river.Event, kind string, n int, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	got := 0
	for got < n {
		select {
		case ev := <-events:
			if ev.Job.Kind != kind {
				continue
			}
			if ev.Kind == river.EventKindJobFailed {
				t.Fatalf("%s failed: %v", kind, ev.Job.Errors)
			}
			got++
		case <-deadline:
			t.Fatalf("%s: %d of %d completions", kind, got, n)
		}
	}
}

// noMore checks that no further completion of kind arrives for a while.
func noMore(t *testing.T, events <-chan *river.Event, kind string, wait time.Duration) {
	t.Helper()
	deadline := time.After(wait)
	for {
		select {
		case ev := <-events:
			if ev.Job.Kind == kind {
				t.Fatalf("%s ran again (job %d)", kind, ev.Job.ID)
			}
		case <-deadline:
			return
		}
	}
}

// The same natural key never runs twice: not when inserted twice in a row,
// not when twenty callers race, not after the first run completed.
func TestDuplicateKeyRunsOnce(t *testing.T) {
	e := newEnv(t)
	setup, err := worker.New(e.db)
	if err != nil {
		t.Fatal(err)
	}
	inserter, err := jobs.NewInserter(e.pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	day := worker.MaintenanceArgs{Day: "2026-11-06"}

	first, err := jobs.Insert(ctx, inserter, day)
	if err != nil || !first {
		t.Fatalf("first insert: %v %v", first, err)
	}
	again, err := jobs.Insert(ctx, inserter, day)
	if err != nil || again {
		t.Fatalf("same key inserted twice: %v %v", again, err)
	}
	var wg sync.WaitGroup
	var created atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := jobs.Insert(ctx, inserter, day); err == nil && ok {
				created.Add(1)
			}
		}()
	}
	wg.Wait()
	if created.Load() != 0 {
		t.Fatalf("%d racing inserts created a job", created.Load())
	}

	_, events := e.start(t, setup.Workers) // no periodic schedule in tests
	kind := worker.MaintenanceArgs{}.Kind()
	completed(t, events, kind, 1, 15*time.Second)

	// Completed: the same day is still a duplicate.
	if ok, err := jobs.Insert(ctx, inserter, day); err != nil || ok {
		t.Fatalf("same key after completion: %v %v", ok, err)
	}
	noMore(t, events, kind, time.Second)

	// Another key is another job.
	if ok, err := jobs.Insert(ctx, inserter, worker.MaintenanceArgs{Day: "2026-11-07"}); err != nil || !ok {
		t.Fatalf("next day: %v %v", ok, err)
	}
	completed(t, events, kind, 1, 15*time.Second)

	var rows int
	if err := e.pool.QueryRow(ctx, `select count(*) from river.river_job where kind = $1`, kind).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("%d maintenance jobs stored, want 2 (one per day)", rows)
	}
}

func TestMaintenancePurgesOnlyWhatExpired(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tenant, user := uuid.New(), uuid.New()
	err := e.db.System(ctx, func(tx pgx.Tx) error {
		for _, q := range []string{
			`insert into tenants (id, slug, name) values ('` + tenant.String() + `', 'a', 'a')`,
			`insert into users (id, email) values ('` + user.String() + `', 'a@x.test')`,
			`insert into idempotency_requests (scope, key, request_hash, status, created_at) values
			   ('s', gen_random_uuid(), '\x00', 201, now() - interval '25 hours'),
			   ('s', gen_random_uuid(), '\x00', 201, now())`,
			`insert into refresh_tokens (user_id, family_id, token_hash, expires_at) values
			   ('` + user.String() + `', gen_random_uuid(), '\x01', now() - interval '8 days'),
			   ('` + user.String() + `', gen_random_uuid(), '\x02', now() + interval '1 day')`,
		} {
			if _, err := tx.Exec(ctx, q); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	setup, _ := worker.New(e.db)
	inserter, _ := jobs.NewInserter(e.pool)
	_, events := e.start(t, setup.Workers)
	if ok, err := jobs.Insert(ctx, inserter, worker.MaintenanceFor(time.Now())); err != nil || !ok {
		t.Fatalf("insert: %v %v", ok, err)
	}
	completed(t, events, worker.MaintenanceArgs{}.Kind(), 1, 15*time.Second)

	var keys, tokens int
	err = e.db.System(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `select count(*) from idempotency_requests`).Scan(&keys); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `select count(*) from refresh_tokens`).Scan(&tokens)
	})
	if err != nil {
		t.Fatal(err)
	}
	if keys != 1 || tokens != 1 {
		t.Fatalf("left %d idempotency keys and %d refresh tokens, want 1 and 1 (the live ones)", keys, tokens)
	}
}

// The queue's pool reaches the queue and nothing else.
func TestJobsPoolSeesNoBusinessTable(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, q := range []string{`select count(*) from tenants`, `select count(*) from users`,
		`select count(*) from audit_log`, `select count(*) from ledger.movements`} {
		var n int
		if err := e.pool.QueryRow(ctx, q).Scan(&n); err == nil {
			t.Errorf("jobs pool read %q (%d rows)", q, n)
		}
	}
	var n int
	if err := e.pool.QueryRow(ctx, `select count(*) from river.river_job`).Scan(&n); err != nil {
		t.Fatalf("jobs pool cannot read its own queue: %v", err)
	}
}

// The pattern for tenant jobs: the tenant travels in the arguments, the job
// works under that tenant's RLS and sees nothing else.
type tenantProbeArgs struct {
	Tenant uuid.UUID `json:"tenant"`
}

func (tenantProbeArgs) Kind() string { return "test.tenant_probe" }

type tenantProbe struct {
	river.WorkerDefaults[tenantProbeArgs]
	db   *db.DB
	seen chan []uuid.UUID
}

func (w *tenantProbe) Work(ctx context.Context, job *river.Job[tenantProbeArgs]) error {
	ctx = jobs.TenantContext(ctx, job.Args.Tenant)
	var ids []uuid.UUID
	err := w.db.InTenant(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `select id from tenants`)
		if err != nil {
			return err
		}
		ids, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	})
	w.seen <- ids
	return err
}

func TestTenantJobSeesOnlyItsTenant(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	err := e.db.System(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `insert into tenants (id, slug, name) values ($1, 'a', 'a'), ($2, 'b', 'b')`, a, b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	probe := &tenantProbe{db: e.db, seen: make(chan []uuid.UUID, 1)}
	ws := river.NewWorkers()
	river.AddWorker(ws, probe)
	c, _ := e.start(t, ws)
	if _, err := c.Insert(ctx, tenantProbeArgs{Tenant: a}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case ids := <-probe.seen:
		if len(ids) != 1 || ids[0] != a {
			t.Fatalf("job for tenant a saw %v", ids)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("tenant job did not run")
	}
}

func TestPeriodicScheduleUsesTodaysKey(t *testing.T) {
	setup, err := worker.New(nil)
	// maintenance, the monthly fan out and the fortnightly close mark.
	if err != nil || len(setup.Periodic) != 3 {
		t.Fatalf("setup: %v %d", err, len(setup.Periodic))
	}
	// 01:30 UTC is still the previous day in Argentina (GMT-3).
	got := worker.MaintenanceFor(time.Date(2026, 11, 7, 1, 30, 0, 0, time.UTC))
	if got.Day != "2026-11-06" {
		t.Fatalf("day key: %s", got.Day)
	}
}
