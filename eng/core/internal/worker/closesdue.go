package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/Tramo-Labs/hq/eng/core/internal/closes"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/jobs"
)

// CloseDueArgs is the fortnightly mark: once a period is over, every
// tenant gets it marked "para cerrar". It never closes anything; an
// operator does (spec §6). The period's first day is the unique key.
type CloseDueArgs struct {
	PeriodStart string `json:"period_start"` // YYYY-MM-DD
}

// Kind is the stable job name stored in the queue.
func (CloseDueArgs) Kind() string { return "closes.mark_due" }

// InsertOpts makes the period the unique key, whatever state the job is in.
func (CloseDueArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}, MaxAttempts: 5}
}

// CloseDueFor returns the args for the latest period of the default
// cadence that is over at t, in Argentina's time.
func CloseDueFor(t time.Time) CloseDueArgs {
	return CloseDueArgs{PeriodStart: closes.LastOver(t, closes.DefaultCadence).Start.Format(time.DateOnly)}
}

// CloseDueWorker lists every tenant (identity.TenantIDs, the one cross
// tenant read) and marks the period for each, under that tenant's scope.
// The mark is unique by tenant and period, so a retry or a second run after
// River forgot the job marks nothing twice, and no durable fan out mark is
// needed.
type CloseDueWorker struct {
	river.WorkerDefaults[CloseDueArgs]
	DB  *db.DB
	Now func() time.Time
}

// Work marks the period for every tenant.
func (w *CloseDueWorker) Work(ctx context.Context, job *river.Job[CloseDueArgs]) error {
	start, err := time.Parse(time.DateOnly, job.Args.PeriodStart)
	if err != nil {
		return river.JobCancel(fmt.Errorf("closes.mark_due: period %q: %w", job.Args.PeriodStart, err))
	}
	p, err := closes.PeriodStarting(start, closes.DefaultCadence)
	if err != nil {
		return river.JobCancel(fmt.Errorf("closes.mark_due: %s: %w", job.Args.PeriodStart, err))
	}
	ids, err := identity.TenantIDs(ctx, w.DB)
	if err != nil {
		return err
	}
	svc := &closes.Service{DB: w.DB, Now: w.Now}
	marked := 0
	for _, id := range ids {
		isNew, err := svc.MarkDue(jobs.TenantContext(ctx, id), p)
		if err != nil {
			return fmt.Errorf("closes.mark_due %s: tenant %s: %w", job.Args.PeriodStart, id, err)
		}
		if isNew {
			marked++
		}
	}
	slog.Info("closes mark due", "period_start", job.Args.PeriodStart, "tenants", len(ids), "marked", marked)
	return nil
}
