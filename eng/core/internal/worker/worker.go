// Package worker assembles the background jobs of core: which job kinds
// exist, their workers and the periodic schedule. cmd/worker runs it; the
// queue itself is internal/platform/jobs (River). See README.md.
package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/idempotency"
)

// MaintenanceArgs is the daily platform clean up. Its natural key is the
// day: however many times it is enqueued for one day (periodic schedule on
// several workers, a manual run, a retry of the enqueue), it runs once.
type MaintenanceArgs struct {
	Day string `json:"day"` // YYYY-MM-DD, Argentina time
}

// Kind is the stable job name stored in the queue.
func (MaintenanceArgs) Kind() string { return "platform.maintenance" }

// InsertOpts makes the day the unique key, whatever state the job is in
// (a completed one included, so the same day never runs twice).
func (MaintenanceArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}, MaxAttempts: 5}
}

var argentina = time.FixedZone("ART", -3*60*60)

// MaintenanceFor returns the args for the day of t in Argentina.
func MaintenanceFor(t time.Time) MaintenanceArgs {
	return MaintenanceArgs{Day: t.In(argentina).Format("2006-01-02")}
}

// MaintenanceWorker purges expired idempotency keys and refresh tokens.
type MaintenanceWorker struct {
	river.WorkerDefaults[MaintenanceArgs]
	DB *db.DB
}

// Work runs the clean up. Both deletes are idempotent, so a retry is safe.
func (w *MaintenanceWorker) Work(ctx context.Context, job *river.Job[MaintenanceArgs]) error {
	keys, err := idempotency.Purge(ctx, w.DB)
	if err != nil {
		return err
	}
	tokens, err := identity.PurgeExpiredRefreshTokens(ctx, w.DB)
	if err != nil {
		return err
	}
	slog.Info("maintenance done", "day", job.Args.Day, "idempotency_keys", keys, "refresh_tokens", tokens)
	return nil
}

// Setup is everything the worker process registers.
type Setup struct {
	Workers  *river.Workers
	Periodic []*river.PeriodicJob
}

// New registers the workers and the schedule. Modules add their jobs here.
func New(d *db.DB) (Setup, error) {
	ws := river.NewWorkers()
	if err := river.AddWorkerSafely(ws, &MaintenanceWorker{DB: d}); err != nil {
		return Setup{}, err
	}
	periodic := []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return MaintenanceFor(time.Now()), nil },
			// Hourly attempts, one run per day: the unique key does the rest.
			&river.PeriodicJobOpts{RunOnStart: true}),
	}
	return Setup{Workers: ws, Periodic: periodic}, nil
}
