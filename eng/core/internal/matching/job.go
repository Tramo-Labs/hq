package matching

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/jobs"
)

// RunSummary is the outcome of running a month.
type RunSummary struct {
	RunID      uuid.UUID
	Period     string
	Matches    int
	New        int // matches this run stored for the first time
	Pending    int
	Exceptions int
}

// RunMonth runs the engine over the movements of one month of the tenant
// in the context and stores the run. It is the only implementation of a
// month run: the API (POST /v1/matching/runs) and the monthly job both call
// it. Running the same month again stores no match twice.
func (s *PGStore) RunMonth(ctx context.Context, period string, cfg Config) (RunSummary, error) {
	from, to, err := monthRange(period)
	if err != nil {
		return RunSummary{}, err
	}
	in, err := s.LoadInput(ctx, from, to, cfg)
	if err != nil {
		return RunSummary{}, err
	}
	r := Run(cfg, in)
	runID, n, err := s.SaveRun(ctx, period, cfg, r)
	if err != nil {
		return RunSummary{}, err
	}
	return RunSummary{RunID: runID, Period: period, Matches: len(r.Matches), New: n,
		Pending: len(r.Pending), Exceptions: len(r.Exceptions)}, nil
}

// MonthArgs is the monthly run of one tenant. Tenant and period are the
// natural key: however many times a month is enqueued, it runs once (a
// completed job included). Only ids live here: River stores args in clear.
type MonthArgs struct {
	Tenant uuid.UUID `json:"tenant"`
	Period string    `json:"period"` // YYYY-MM
}

// Kind is the stable job name stored in the queue.
func (MonthArgs) Kind() string { return "matching.run_month" }

// InsertOpts makes tenant and period the unique key, whatever state the
// job is in.
func (MonthArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}, MaxAttempts: 5}
}

// MonthWorker runs MonthArgs under the tenant of its arguments.
type MonthWorker struct {
	river.WorkerDefaults[MonthArgs]
	Store  *PGStore
	Config Config
}

// Work scopes the context to the job's tenant (the only way a job gets a
// scope) and runs the month with RunMonth. A retry is safe: matches are
// stored once by their ID; at most it adds another run row with the same
// result.
func (w *MonthWorker) Work(ctx context.Context, job *river.Job[MonthArgs]) error {
	if job.Args.Tenant == uuid.Nil {
		return fmt.Errorf("matching.run_month: no tenant")
	}
	sum, err := w.Store.RunMonth(jobs.TenantContext(ctx, job.Args.Tenant), job.Args.Period, w.Config)
	if err != nil {
		return err
	}
	slog.Info("month run", "tenant", job.Args.Tenant, "period", sum.Period, "matches", sum.Matches,
		"new", sum.New, "pending", sum.Pending, "exceptions", sum.Exceptions)
	return nil
}

// EnqueueMonth asks for the monthly run of the tenant in the context. It
// returns false when that month was already enqueued (or ran).
func EnqueueMonth(ctx context.Context, c *jobs.Client, period string) (bool, error) {
	sc, ok := db.ScopeFrom(ctx)
	if !ok {
		return false, db.ErrNoScope
	}
	if _, _, err := monthRange(period); err != nil {
		return false, err
	}
	return jobs.Insert(ctx, c, MonthArgs{Tenant: sc.TenantID, Period: period})
}
