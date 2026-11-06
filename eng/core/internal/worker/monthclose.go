package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/matching"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/jobs"
)

// MonthCloseArgs is the monthly fan out: once per closed month, it enqueues
// the matching run of that month for every tenant. Its natural key is the
// month, so however often the schedule enqueues it, it runs once.
type MonthCloseArgs struct {
	Period string `json:"period"` // YYYY-MM, the month just closed in Argentina
}

// Kind is the stable job name stored in the queue.
func (MonthCloseArgs) Kind() string { return "platform.month_close" }

// InsertOpts makes the month the unique key, whatever state the job is in.
func (MonthCloseArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}, MaxAttempts: 5}
}

// MonthCloseFor returns the args for the month before the one t falls in,
// in Argentina time.
func MonthCloseFor(t time.Time) MonthCloseArgs {
	a := t.In(argentina)
	first := time.Date(a.Year(), a.Month(), 1, 0, 0, 0, 0, argentina)
	return MonthCloseArgs{Period: first.AddDate(0, -1, 0).Format("2006-01")}
}

// MonthCloseWorker lists every tenant (identity.TenantIDs, through
// tramo_system: the one cross tenant read) and enqueues matching.run_month
// for each. Those are unique by tenant and period, so a retry, or a second
// fan out of the same month, enqueues nothing new. Insert defaults to the
// client running the job.
type MonthCloseWorker struct {
	river.WorkerDefaults[MonthCloseArgs]
	DB     *db.DB
	Insert func(ctx context.Context, args river.JobArgs) (bool, error)
}

// Work enqueues the month of every tenant.
func (w *MonthCloseWorker) Work(ctx context.Context, job *river.Job[MonthCloseArgs]) error {
	insert := w.Insert
	if insert == nil {
		c, err := river.ClientFromContextSafely[pgx.Tx](ctx)
		if err != nil {
			return fmt.Errorf("platform.month_close: no queue client: %w", err)
		}
		insert = func(ctx context.Context, args river.JobArgs) (bool, error) { return jobs.Insert(ctx, c, args) }
	}
	tenants, err := identity.TenantIDs(ctx, w.DB)
	if err != nil {
		return err
	}
	enqueued := 0
	for _, id := range tenants {
		ok, err := insert(ctx, matching.MonthArgs{Tenant: id, Period: job.Args.Period})
		if err != nil {
			return fmt.Errorf("platform.month_close %s: tenant %s: %w", job.Args.Period, id, err)
		}
		if ok {
			enqueued++
		}
	}
	slog.Info("month close", "period", job.Args.Period, "tenants", len(tenants), "enqueued", enqueued)
	return nil
}
