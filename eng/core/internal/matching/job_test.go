package matching

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db/dbtest"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/jobs"
)

// jobEnv runs a real River worker with only the monthly matching job on
// the test database, and an inserter like the api's.
type jobEnv struct {
	pgEnv
	inserter *jobs.Client
	events   <-chan *river.Event
}

func newJobEnv(t *testing.T) jobEnv {
	t.Helper()
	e := newPGEnv(t)
	pool, err := jobs.NewPool(context.Background(), dbtest.URL(dbtest.DatabaseName(t, e.d)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	inserter, err := jobs.NewInserter(pool)
	if err != nil {
		t.Fatal(err)
	}
	ws := river.NewWorkers()
	if err := river.AddWorkerSafely(ws, &MonthWorker{Store: e.store, Config: DefaultConfig}); err != nil {
		t.Fatal(err)
	}
	c, err := jobs.NewWorker(pool, jobs.WorkerConfig{Workers: ws, FetchPoll: 50 * time.Millisecond, TestOnlyMode: true})
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
	return jobEnv{pgEnv: e, inserter: inserter, events: events}
}

// waitRuns waits for n completed month runs and fails on a failed one.
func (e jobEnv) waitRuns(t *testing.T, n int) {
	t.Helper()
	deadline := time.After(20 * time.Second)
	for got := 0; got < n; {
		select {
		case ev := <-e.events:
			if ev.Job.Kind != (MonthArgs{}).Kind() {
				continue
			}
			if ev.Kind == river.EventKindJobFailed {
				t.Fatalf("month run failed: %v", ev.Job.Errors)
			}
			got++
		case <-deadline:
			t.Fatalf("%d of %d month runs completed", got, n)
		}
	}
}

// noMoreRuns checks that no other month run completes for a while.
func (e jobEnv) noMoreRuns(t *testing.T) {
	t.Helper()
	deadline := time.After(1500 * time.Millisecond)
	for {
		select {
		case ev := <-e.events:
			if ev.Job.Kind == (MonthArgs{}).Kind() {
				t.Fatalf("a month ran again (job %d, args %s)", ev.Job.ID, ev.Job.EncodedArgs)
			}
		case <-deadline:
			return
		}
	}
}

func (e jobEnv) runs(t *testing.T, ctx context.Context, period string) int {
	t.Helper()
	var n int
	if err := e.d.InTenant(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select count(*) from matching.runs where period = $1`, period).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// While River keeps the job, the same month enqueued twice runs once, and
// enqueued again after it completed, still nothing. It is the same run as
// the API's: same matches stored. Once River deletes the completed job
// (after 24 hours) the key is free again: the durable once per month is
// the fan out's (platform.month_closes), and a second run stores no new
// match.
func TestMonthJobRunsOncePerTenantAndMonth(t *testing.T) {
	e := newJobEnv(t)
	first, err := EnqueueMonth(e.ctx, e.inserter, "2026-10")
	if err != nil || !first {
		t.Fatalf("first enqueue: %v %v", first, err)
	}
	if again, err := EnqueueMonth(e.ctx, e.inserter, "2026-10"); err != nil || again {
		t.Fatalf("second enqueue of the same month: %v %v", again, err)
	}
	e.waitRuns(t, 1)
	e.noMoreRuns(t)
	if after, err := EnqueueMonth(e.ctx, e.inserter, "2026-10"); err != nil || after {
		t.Fatalf("enqueue after the run completed: %v %v", after, err)
	}
	e.noMoreRuns(t)

	if n := e.runs(t, e.ctx, "2026-10"); n != 1 {
		t.Errorf("%d runs stored for the month; want 1", n)
	}
	// What RunMonth stores for the Bancor month: three matches (reference,
	// amount and date, tolerance).
	if n := e.count(t, e.ctx, `select count(*) from matching.matches`); n != 3 {
		t.Errorf("%d matches stored by the job; want 3", n)
	}
	// River's cleaner deletes completed jobs after its retention; do it
	// now. The key is free: the month enqueues and runs again, and the
	// second run stores no new match.
	if _, err := e.d.Pool.Exec(context.Background(),
		`delete from river.river_job where kind = 'matching.run_month' and state = 'completed'`); err != nil {
		t.Fatal(err)
	}
	if again, err := EnqueueMonth(e.ctx, e.inserter, "2026-10"); err != nil || !again {
		t.Fatalf("enqueue once River forgot the job: %v %v; want enqueued", again, err)
	}
	e.waitRuns(t, 1)
	if n := e.runs(t, e.ctx, "2026-10"); n != 2 {
		t.Errorf("%d runs stored for the month; want 2", n)
	}
	if n := e.count(t, e.ctx, `select count(*) from matching.matches`); n != 3 {
		t.Errorf("%d matches after the second run; want still 3", n)
	}
	// Another month of the same tenant is another key.
	if other, err := EnqueueMonth(e.ctx, e.inserter, "2026-11"); err != nil || !other {
		t.Fatalf("another month: %v %v", other, err)
	}
	e.waitRuns(t, 1)

	if _, err := EnqueueMonth(context.Background(), e.inserter, "2026-10"); err == nil {
		t.Error("enqueue without a tenant scope accepted")
	}
	if _, err := EnqueueMonth(e.ctx, e.inserter, "octubre"); err == nil {
		t.Error("bad period accepted")
	}
}

// A tenant's job processes only that tenant's month: tenant b's run of
// October finds none of tenant a's movements and leaves tenant a alone.
func TestMonthJobStaysInItsTenant(t *testing.T) {
	e := newJobEnv(t)
	if ok, err := EnqueueMonth(e.ctx, e.inserter, "2026-10"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	e.waitRuns(t, 1)
	// Same month, tenant b: a different key, so it runs, under b's scope.
	if ok, err := EnqueueMonth(e.ctxB, e.inserter, "2026-10"); err != nil || !ok {
		t.Fatalf("tenant b's month: %v %v", ok, err)
	}
	e.waitRuns(t, 1)

	if n := e.runs(t, e.ctxB, "2026-10"); n != 1 {
		t.Errorf("tenant b runs: %d; want 1", n)
	}
	if n := e.count(t, e.ctxB, `select count(*) from matching.matches`); n != 0 {
		t.Errorf("tenant b's run matched %d of tenant a's movements", n)
	}
	if n := e.count(t, e.ctxB, `select count(*) from matching.pending`); n != 0 {
		t.Errorf("tenant b's run saw %d of tenant a's movements", n)
	}
	if n := e.runs(t, e.ctx, "2026-10"); n != 1 {
		t.Errorf("tenant a runs: %d; want 1 (b's job must not run a's month)", n)
	}
	if n := e.count(t, e.ctx, `select count(*) from matching.matches`); n != 3 {
		t.Errorf("tenant a matches: %d; want 3", n)
	}
}
