// Command arca-gateway is the only process that holds the ARCA certificate
// private key. It exposes the gRPC service in proto/arca/v1 to core and
// talks SOAP to ARCA (WSAA, WSFEv1, WSCDC, ws_sr_constancia_inscripcion).
// With ARCA_ENV=sim it answers from the local simulator.
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
	slog.Info("arca-gateway up", "addr", cfg.GRPCAddr, "arca_env", cfg.ArcaEnv)
	<-ctx.Done()
}
