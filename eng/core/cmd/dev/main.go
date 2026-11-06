// Command dev runs the whole core on one machine without Docker, for the
// portal and testers: embedded Postgres 17, migrations, the synthetic seed,
// the api on localhost:8080, the job worker, and the ARCA gateway on the
// simulator. Ctrl-C stops everything. `make dev` runs it.
//
// Dev only: it refuses to start unless APP_ENV is dev (or unset) and
// ARCA_ENV is sim (or unset). Data and dev secrets live in DEV_DATA_DIR
// (default ~/.local/share/tramo-dev), outside the repo, so they survive
// restarts; delete that directory to start over.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/tabwriter"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/Tramo-Labs/hq/eng/core/internal/app"
	"github.com/Tramo-Labs/hq/eng/core/internal/arca"
	"github.com/Tramo-Labs/hq/eng/core/internal/arca/gateway"
	"github.com/Tramo-Labs/hq/eng/core/internal/devseed"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/crypto"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/jobs"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/mail"
	"github.com/Tramo-Labs/hq/eng/core/internal/worker"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "make dev:", err)
		os.Exit(1)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func run() error {
	if err := devseed.Guard(os.Getenv("APP_ENV"), os.Getenv("ARCA_ENV")); err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	dir := env("DEV_DATA_DIR", filepath.Join(home, ".local", "share", "tramo-dev"))
	port := uint32(54320)
	if v := os.Getenv("DEV_PG_PORT"); v != "" {
		if _, err := fmt.Sscan(v, &port); err != nil {
			return fmt.Errorf("DEV_PG_PORT: %w", err)
		}
	}
	httpAddr := env("DEV_HTTP_ADDR", "127.0.0.1:8080")
	grpcAddr := env("DEV_GRPC_ADDR", "127.0.0.1:9090")
	fixtures := env("DEV_FIXTURES", filepath.Join("internal", "ledger", "testdata"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	secrets, err := devseed.LoadSecrets(dir)
	if err != nil {
		return err
	}

	// Postgres: data kept in dir/pg-data (reused across runs); the runtime
	// dir is wiped by embedded-postgres on every start, so it is separate.
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V17).Port(port).
		Username("postgres").Password("postgres").Database("postgres").
		RuntimePath(filepath.Join(dir, "pg-runtime")).DataPath(filepath.Join(dir, "pg-data")).
		StartTimeout(90 * time.Second).Logger(io.Discard))
	slog.Info("starting postgres", "port", port, "data", filepath.Join(dir, "pg-data"))
	if err := pg.Start(); err != nil {
		return fmt.Errorf("postgres: %w (is something else on port %d? set DEV_PG_PORT)", err, port)
	}
	defer func() {
		slog.Info("stopping postgres")
		_ = pg.Stop()
	}()
	url := func(database string) string {
		return fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/%s?sslmode=disable", port, database)
	}
	if err := ensureDatabase(ctx, url("postgres"), "tramo"); err != nil {
		return err
	}

	d, err := db.Open(ctx, url("tramo"))
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Migrate(ctx); err != nil {
		return err
	}
	kr, err := crypto.NewKeyring(secrets.MasterKey)
	if err != nil {
		return err
	}
	if err := devseed.Seed(ctx, d, kr, secrets, fixtures); err != nil {
		return err
	}

	// ARCA gateway on the simulator.
	acfg, err := arca.LoadConfig() // ARCA_ENV is sim (Guard)
	if err != nil {
		return err
	}
	glis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return fmt.Errorf("gateway listen %s: %w", grpcAddr, err)
	}
	gwDone := make(chan error, 1)
	go func() { gwDone <- gateway.Run(ctx, acfg, glis) }()

	// Job worker (River), same as cmd/worker.
	jpool, err := jobs.NewPool(ctx, url("tramo"))
	if err != nil {
		return err
	}
	defer jpool.Close()
	// The worker sends the magic links (D-17) to the same private outbox.
	setup, err := worker.New(d, worker.Options{Mailer: &mail.Outbox{Path: filepath.Join(dir, "outbox")},
		AppBaseURL: env("DEV_APP_BASE_URL", "http://localhost:3000")})
	if err != nil {
		return err
	}
	wk, err := jobs.NewWorker(jpool, jobs.WorkerConfig{Workers: setup.Workers, Periodic: setup.Periodic, Logger: slog.Default()})
	if err != nil {
		return err
	}
	if err := wk.Start(ctx); err != nil {
		return err
	}

	// API.
	signer, err := identity.NewSigner(secrets.JWTSeed, "dev")
	if err != nil {
		return err
	}
	ids, err := identity.NewService(d, signer, kr, identity.DefaultArgon2)
	if err != nil {
		return err
	}
	ids.Enqueue = func(ctx context.Context, args river.JobArgs) error {
		_, err := jobs.Insert(ctx, wk, args)
		return err
	}
	h, err := newAPI(d, ids, filepath.Join(dir, "outbox"))
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: httpAddr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	apiDone := make(chan error, 1)
	go func() { apiDone <- srv.ListenAndServe() }()

	banner(httpAddr, grpcAddr, dir, secrets)

	select {
	case <-ctx.Done():
	case err := <-apiDone:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("api: %w", err)
		}
	case err := <-gwDone:
		return fmt.Errorf("gateway: %w", err)
	}
	slog.Info("stopping")
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	_ = wk.Stop(shutdown)
	<-gwDone
	return nil
}

// newAPI builds the api handler exactly as make dev serves it. Emails land
// in a private outbox file; nothing is ever sent. The browser origins
// allowed (CORS and the refresh Origin check, ADR 0007) are
// CORS_ALLOWED_ORIGINS, or else DEV_APP_BASE_URL's origin
// (http://localhost:3000 by default): without them every cookie refresh
// and logout from the portal would be a 403.
func newAPI(d *db.DB, ids *identity.Service, outbox string) (http.Handler, error) {
	base := env("DEV_APP_BASE_URL", "http://localhost:3000")
	ids.Mailer, ids.AppBaseURL = &mail.Outbox{Path: outbox}, base
	slog.Info("emails go to a local file (mode 0600), never sent", "outbox", outbox)
	origins, err := devOrigins(base)
	if err != nil {
		return nil, err
	}
	slog.Info("browser origins allowed", "origins", origins.List())
	deps := app.Deps{DB: d, Identity: ids, CORSOrigins: origins, DocsDir: "api"}
	return app.New(deps, app.NewHandlers(deps))
}

func devOrigins(base string) (httpx.Origins, error) {
	list := os.Getenv("CORS_ALLOWED_ORIGINS")
	if list == "" {
		u, err := url.Parse(base)
		if err != nil || u.Host == "" {
			return httpx.Origins{}, fmt.Errorf("DEV_APP_BASE_URL %q is not a URL", base)
		}
		list = u.Scheme + "://" + u.Host
	}
	o, err := httpx.ParseOrigins(list, true)
	if err != nil {
		return httpx.Origins{}, fmt.Errorf("CORS_ALLOWED_ORIGINS: %w", err)
	}
	return o, nil
}

func ensureDatabase(ctx context.Context, adminURL, name string) error {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	var exists bool
	if err := conn.QueryRow(ctx, `select exists (select 1 from pg_database where datname = $1)`, name).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = conn.Exec(ctx, "create database "+name)
	return err
}

func banner(httpAddr, grpcAddr, dir string, s devseed.Secrets) {
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintf(w, "\nTramo core (dev): http://%s  docs http://%s/docs/  gateway %s (ARCA simulator)\n", httpAddr, httpAddr, grpcAddr)
	fmt.Fprintf(w, "Synthetic data only. Passwords are dev only, random per %s.\n\n", dir)
	fmt.Fprintln(w, "EMAIL\tPASSWORD\tROLES")
	for _, u := range devseed.Users {
		roles := ""
		for _, t := range devseed.Tenants {
			if r, ok := u.Roles[t.Slug]; ok {
				if roles != "" {
					roles += ", "
				}
				roles += r + " @ " + t.Slug
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", u.Email, s.Passwords[u.Email], roles)
	}
	fmt.Fprintln(w, "\nCtrl-C stops everything.")
	_ = w.Flush()
}
