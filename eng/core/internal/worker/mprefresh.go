package worker

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/riverqueue/river"

	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/mercadopago"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/crypto"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/jobs"
)

// MPRefreshArgs is the daily renewal of Mercado Pago tokens. Its key is
// the Argentine day, so the hourly schedule runs it once a day.
type MPRefreshArgs struct {
	Day string `json:"day"` // YYYY-MM-DD
}

// Kind is the stable job name stored in the queue.
func (MPRefreshArgs) Kind() string { return "mercadopago.refresh" }

// InsertOpts makes the day the unique key.
func (MPRefreshArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}, MaxAttempts: 3}
}

// MPRefreshFor returns the args for the Argentine day t falls in.
func MPRefreshFor(t time.Time) MPRefreshArgs {
	return MPRefreshArgs{Day: t.In(argentina).Format(time.DateOnly)}
}

// MPRefreshWorker lists every tenant (identity.TenantIDs, ids only) and,
// under each tenant's own scope, renews its tokens when they are close to
// expiring (mercadopago.Service.Refresh). A tenant without a connection
// costs one indexed read. One tenant's failure does not stop the others;
// the job fails at the end so River retries it, and renewing twice is
// harmless (a token far from expiry is left alone).
type MPRefreshWorker struct {
	river.WorkerDefaults[MPRefreshArgs]
	DB      *db.DB
	Keyring *crypto.Keyring
	Config  mercadopago.Config
	HTTP    *http.Client
	Now     func() time.Time
}

// Work renews what is due.
func (w *MPRefreshWorker) Work(ctx context.Context, job *river.Job[MPRefreshArgs]) error {
	if !w.Config.Configured() {
		return nil
	}
	ids, err := identity.TenantIDs(ctx, w.DB)
	if err != nil {
		return err
	}
	svc := &mercadopago.Service{DB: w.DB, Keyring: w.Keyring, Config: w.Config, HTTP: w.HTTP, Now: w.Now}
	renewed, failed := 0, 0
	var first error
	for _, id := range ids {
		ok, err := svc.Refresh(jobs.TenantContext(ctx, id))
		switch {
		case err != nil:
			failed++
			if first == nil {
				first = fmt.Errorf("mercadopago.refresh: tenant %s: %w", id, err)
			}
		case ok:
			renewed++
		}
	}
	slog.Info("mercadopago refresh", "day", job.Args.Day, "tenants", len(ids), "renewed", renewed, "failed", failed)
	return first
}
