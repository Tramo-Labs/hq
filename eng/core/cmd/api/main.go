// Command api serves the core REST API described in api/openapi.yaml.
// It applies pending migrations at startup, then serves until SIGTERM.
package main

import (
	"context"
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
	if err := run(); err != nil {
		slog.Error("api", "err", err)
		os.Exit(1)
	}
}

func run() error {
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
	if err := d.Migrate(ctx); err != nil {
		return err
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
	deps := app.Deps{DB: d, Identity: ids, TrustProxy: cfg.TrustProxyHeaders, DocsDir: cfg.DocsDir}
	h, err := app.New(deps, app.NewHandlers(deps))
	if err != nil {
		return err
	}
	return httpx.Run(cfg.HTTPAddr, h)
}
