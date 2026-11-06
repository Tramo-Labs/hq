package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/Tramo-Labs/hq/eng/core/internal/matching"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/jobs"
	"github.com/Tramo-Labs/hq/eng/core/internal/worker"
)

// The period closed is the previous month in Argentina (GMT-3): the first
// hours of a month in UTC are still the last day of the month before.
func TestMonthCloseClosesThePreviousArgentineMonth(t *testing.T) {
	for at, want := range map[time.Time]string{
		time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC):   "2026-10", // 00:00 on 1 Nov in Argentina
		time.Date(2026, 11, 1, 2, 59, 0, 0, time.UTC):  "2026-09", // still 31 Oct there
		time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC): "2026-10",
		time.Date(2027, 1, 1, 3, 0, 0, 0, time.UTC):    "2026-12",
	} {
		if got := worker.MonthCloseFor(at).Period; got != want {
			t.Errorf("%s: period %s, want %s", at, got, want)
		}
	}
}

// Once a month the fan out enqueues the monthly matching run of every
// tenant. Neither a second fan out of the same month (queued twice, again
// after it ran, or its Work run again as a retry would) nor anything else
// makes a tenant's month run twice.
func TestMonthCloseEnqueuesEveryTenantOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tenants := []uuid.UUID{uuid.New(), uuid.New()}
	for i, id := range tenants {
		if _, err := e.db.Pool.Exec(ctx, `insert into tenants (id, slug, name) values ($1, $2, 'x')`, id, "molino-mc-"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	setup, err := worker.New(e.db)
	if err != nil {
		t.Fatal(err)
	}
	inserter, err := jobs.NewInserter(e.pool)
	if err != nil {
		t.Fatal(err)
	}
	close := worker.MonthCloseArgs{Period: "2026-10"}
	if ok, err := jobs.Insert(ctx, inserter, close); err != nil || !ok {
		t.Fatalf("first fan out: %v %v", ok, err)
	}
	if ok, err := jobs.Insert(ctx, inserter, close); err != nil || ok {
		t.Fatalf("the same month queued twice: %v %v", ok, err)
	}

	_, events := e.start(t, setup.Workers)
	completed(t, events, close.Kind(), 1, 15*time.Second)
	completed(t, events, matching.MonthArgs{}.Kind(), len(tenants), 15*time.Second)

	// Again after it ran: skipped. Its Work run again (a retry): the month
	// runs already exist, nothing new.
	if ok, err := jobs.Insert(ctx, inserter, close); err != nil || ok {
		t.Fatalf("the month queued again after it ran: %v %v", ok, err)
	}
	w := &worker.MonthCloseWorker{DB: e.db, Insert: func(ctx context.Context, a river.JobArgs) (bool, error) {
		return jobs.Insert(ctx, inserter, a)
	}}
	if err := w.Work(ctx, &river.Job[worker.MonthCloseArgs]{Args: close}); err != nil {
		t.Fatal(err)
	}
	noMore(t, events, matching.MonthArgs{}.Kind(), 2*time.Second)

	// One run of October per tenant, under its own scope.
	for _, id := range tenants {
		var n int
		if err := e.db.Pool.QueryRow(ctx, `select count(*) from matching.runs where tenant_id = $1 and period = '2026-10'`, id).Scan(&n); err != nil || n != 1 {
			t.Fatalf("tenant %s: %d runs of 2026-10 (%v), want 1", id, n, err)
		}
	}
}
