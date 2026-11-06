package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/Tramo-Labs/hq/eng/core/internal/worker"
)

// The job marks the latest fortnight over in Argentina (GMT-3).
func TestCloseDueForTheLastFortnightOver(t *testing.T) {
	for at, want := range map[time.Time]string{
		time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC):  "2026-11-01",
		time.Date(2026, 11, 16, 2, 59, 0, 0, time.UTC): "2026-10-16",
		time.Date(2026, 12, 1, 3, 0, 0, 0, time.UTC):   "2026-11-16",
	} {
		if got := worker.CloseDueFor(at).PeriodStart; got != want {
			t.Errorf("%s: %s, want %s", at, got, want)
		}
	}
}

// Every tenant gets the period marked once, however often the job runs,
// and nothing is closed by it. A period that is not over is refused.
func TestCloseDueMarksEveryTenantOnceAndClosesNothing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tenants := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for i, id := range tenants {
		if _, err := e.db.Pool.Exec(ctx, `insert into tenants (id, slug, name) values ($1, $2, 'x')`, id, "molino-cd-"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	w := &worker.CloseDueWorker{DB: e.db, Now: func() time.Time { return time.Date(2026, 11, 17, 15, 0, 0, 0, time.UTC) }}
	job := &river.Job[worker.CloseDueArgs]{Args: worker.CloseDueArgs{PeriodStart: "2026-11-01"}}
	for i := 0; i < 2; i++ {
		if err := w.Work(ctx, job); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	var marks, distinct, closed int
	if err := e.db.Pool.QueryRow(ctx, `select count(*), count(distinct tenant_id) from closes.due
		where period_start = '2026-11-01' and period_end = '2026-11-16'`).Scan(&marks, &distinct); err != nil {
		t.Fatal(err)
	}
	if marks != len(tenants) || distinct != len(tenants) {
		t.Fatalf("%d marks over %d tenants, want one each for %d", marks, distinct, len(tenants))
	}
	if err := e.db.Pool.QueryRow(ctx, `select count(*) from closes.closes`).Scan(&closed); err != nil || closed != 0 {
		t.Fatalf("the job closed %d periods: %v", closed, err)
	}
	if err := w.Work(ctx, &river.Job[worker.CloseDueArgs]{Args: worker.CloseDueArgs{PeriodStart: "2026-11-16"}}); err == nil {
		t.Fatal("marked a fortnight that is not over")
	}
	if err := w.Work(ctx, &river.Job[worker.CloseDueArgs]{Args: worker.CloseDueArgs{PeriodStart: "2026-11-02"}}); err == nil {
		t.Fatal("marked a date that starts no period")
	}
}
