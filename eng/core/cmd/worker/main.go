// Command worker runs the background jobs (statement parsing, matching,
// ARCA calls, ERP writes). Jobs are unique by their natural key and safe to
// retry; see eng/ARQUITECTURA.md section 7.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/config"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("worker up", "env", cfg.Env)
	<-ctx.Done()
	slog.Info("worker down")
}
