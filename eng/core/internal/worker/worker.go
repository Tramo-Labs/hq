// Package worker assembles the background jobs of core: which job kinds
// exist, their workers and the periodic schedule. cmd/worker runs it; the
// queue itself is internal/platform/jobs (River). See README.md.
package worker

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/riverqueue/river"

	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/matching"
	"github.com/Tramo-Labs/hq/eng/core/internal/mercadopago"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/crypto"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/idempotency"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/mail"
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

// Work runs the clean up. Every delete is idempotent, so a retry is safe.
func (w *MaintenanceWorker) Work(ctx context.Context, job *river.Job[MaintenanceArgs]) error {
	keys, err := idempotency.Purge(ctx, w.DB)
	if err != nil {
		return err
	}
	tokens, err := identity.PurgeExpiredRefreshTokens(ctx, w.DB)
	if err != nil {
		return err
	}
	invites, err := identity.PurgeExpiredInvites(ctx, w.DB)
	if err != nil {
		return err
	}
	links, err := identity.PurgeExpiredMagicLinks(ctx, w.DB)
	if err != nil {
		return err
	}
	requests, err := identity.PurgeStaleMagicLinkRequests(ctx, w.DB)
	if err != nil {
		return err
	}
	slog.Info("maintenance done", "day", job.Args.Day, "idempotency_keys", keys, "refresh_tokens", tokens,
		"invites", invites, "magic_links", links, "magic_link_requests", requests)
	return nil
}

// Setup is everything the worker process registers.
type Setup struct {
	Workers  *river.Workers
	Periodic []*river.PeriodicJob
}

// Options are what some jobs need beyond the database.
type Options struct {
	// Mailer and AppBaseURL send the magic links (identity). A nil Mailer
	// logs that a link was not delivered, like invitations.
	Mailer     mail.Sender
	AppBaseURL string
	// MercadoPago and Keyring renew the Mercado Pago tokens. An app not
	// configured registers no renewal.
	MercadoPago     mercadopago.Config
	MercadoPagoHTTP *http.Client
	Keyring         *crypto.Keyring
}

// New registers the workers and the schedule. Modules add their jobs here.
func New(d *db.DB, opts ...Options) (Setup, error) {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	ws := river.NewWorkers()
	if err := river.AddWorkerSafely(ws, &MaintenanceWorker{DB: d}); err != nil {
		return Setup{}, err
	}
	// identity: magic links are sent here, never in the request (D-17).
	if err := river.AddWorkerSafely(ws, &identity.MagicLinkWorker{DB: d, Mailer: o.Mailer, AppBaseURL: o.AppBaseURL}); err != nil {
		return Setup{}, err
	}
	// datos: the monthly matching run of one tenant (internal/matching).
	if err := river.AddWorkerSafely(ws, &matching.MonthWorker{Store: &matching.PGStore{DB: d}, Config: matching.DefaultConfig}); err != nil {
		return Setup{}, err
	}
	// platform: the monthly fan out of matching.run_month to every tenant.
	if err := river.AddWorkerSafely(ws, &MonthCloseWorker{DB: d}); err != nil {
		return Setup{}, err
	}
	// closes: the fortnightly "para cerrar" mark of every tenant.
	if err := river.AddWorkerSafely(ws, &CloseDueWorker{DB: d}); err != nil {
		return Setup{}, err
	}
	// mercadopago: the daily token renewal (does nothing when no app is set).
	if err := river.AddWorkerSafely(ws, &MPRefreshWorker{DB: d, Keyring: o.Keyring, Config: o.MercadoPago, HTTP: o.MercadoPagoHTTP}); err != nil {
		return Setup{}, err
	}
	periodic := []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return MaintenanceFor(time.Now()), nil },
			// Hourly attempts, one run per day: the unique key does the rest.
			&river.PeriodicJobOpts{RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return MonthCloseFor(time.Now()), nil },
			// Hourly attempts, one run per closed month: the month is the key.
			&river.PeriodicJobOpts{RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return CloseDueFor(time.Now()), nil },
			// Hourly attempts, one run per period over: the period is the key.
			&river.PeriodicJobOpts{RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return MPRefreshFor(time.Now()), nil },
			// Hourly attempts, one run per day: the day is the key.
			&river.PeriodicJobOpts{RunOnStart: true}),
	}
	return Setup{Workers: ws, Periodic: periodic}, nil
}
