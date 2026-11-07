// Command worker runs the background jobs (River on Postgres): the daily
// platform maintenance today, statement parsing, matching, ARCA calls and
// ERP writes as modules add them. Jobs are unique by their natural key and
// safe to retry; see internal/platform/jobs and internal/worker.
//
// It connects with the same DATABASE_URL as the api (tramo_runtime in
// production); the queue's own pool switches to tramo_jobs. It never
// migrates: `api migrate` does.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/mercadopago"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/config"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/crypto"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/jobs"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/mail"
	"github.com/Tramo-Labs/hq/eng/core/internal/worker"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("worker", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	d, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer d.Close()
	pool, err := jobs.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Magic links are sent by the worker (D-17): it gets the outbox and the
	// app's address, the api only enqueues.
	opts := worker.Options{AppBaseURL: cfg.AppBaseURL}
	if cfg.MailOutbox != "" {
		opts.Mailer = &mail.Outbox{Path: cfg.MailOutbox}
	}
	// Mercado Pago tokens are renewed here; they are sealed with the
	// tenant's data key, so the worker needs the keyring too.
	if opts.Keyring, err = crypto.NewKeyring(cfg.MasterKey); err != nil {
		return err
	}
	opts.MercadoPago = mercadopago.Config{ClientID: cfg.MPClientID, ClientSecret: cfg.MPClientSecret,
		RedirectURI: cfg.MPRedirectURI, AuthURL: cfg.MPAuthURL, APIURL: cfg.MPAPIURL}
	setup, err := worker.New(d, opts)
	if err != nil {
		return err
	}
	client, err := jobs.NewWorker(pool, jobs.WorkerConfig{
		Workers: setup.Workers, Periodic: setup.Periodic,
		MaxWorkers: config.Int("WORKER_CONCURRENCY", 10), Logger: slog.Default(),
	})
	if err != nil {
		return err
	}
	if err := client.Start(ctx); err != nil {
		return err
	}
	slog.Info("worker up", "env", cfg.Env)
	<-ctx.Done()

	// Let running jobs finish for up to 30 seconds; River rescues anything
	// left mid way on the next start (jobs are retry safe).
	shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.Stop(shutdown); err != nil {
		slog.Warn("worker stop", "err", err)
	}
	slog.Info("worker down")
	return nil
}
