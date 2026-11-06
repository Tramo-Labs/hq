// Command api serves the core REST API described in api/openapi.yaml.
//
//	api           serve until SIGTERM (migrates first only if MIGRATE_ON_START,
//	              which defaults to true in dev and false elsewhere)
//	api migrate   apply pending migrations with MIGRATE_DATABASE_URL (or
//	              DATABASE_URL) and exit; needs no other configuration
//
// In production the migrate step runs with a privileged role and the api
// with tramo_runtime, which cannot touch a table by itself (ADR 0005).
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/Tramo-Labs/hq/eng/core/internal/app"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/config"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/crypto"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	run := serve
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		run = migrate
	}
	if err := run(); err != nil {
		slog.Error("api", "err", err)
		os.Exit(1)
	}
}

// migrate applies the embedded migrations and exits. It reads only the
// database URL: the privileged credentials never meet the app secrets.
func migrate() error {
	url := os.Getenv("MIGRATE_DATABASE_URL")
	if url == "" {
		url = os.Getenv("DATABASE_URL")
	}
	if url == "" {
		return errors.New("MIGRATE_DATABASE_URL or DATABASE_URL is required")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, url)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Migrate(ctx); err != nil {
		return err
	}
	slog.Info("migrations applied")
	return nil
}

func serve() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	slog.Info("config", "cfg", cfg)
	if len(cfg.Ephemeral) > 0 {
		slog.Warn("dev: secrets generated for this run only; tokens and encrypted data will not survive a restart",
			"secrets", cfg.Ephemeral)
	}
	ctx := context.Background()
	d, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer d.Close()
	if cfg.MigrateOnStart {
		if err := d.Migrate(ctx); err != nil {
			return err
		}
	}
	signer, err := identity.NewSigner(cfg.JWTSeed, cfg.JWTKeyID)
	if err != nil {
		return err
	}
	keyring, err := crypto.NewKeyring(cfg.MasterKey)
	if err != nil {
		return err
	}
	ids, err := identity.NewService(d, signer, keyring, identity.DefaultArgon2)
	if err != nil {
		return err
	}
	deps := app.Deps{DB: d, Identity: ids, TrustedProxies: cfg.TrustedProxies, DocsDir: cfg.DocsDir}
	h, err := app.New(deps, app.NewHandlers(deps))
	if err != nil {
		return err
	}
	return httpx.Run(cfg.HTTPAddr, h)
}
