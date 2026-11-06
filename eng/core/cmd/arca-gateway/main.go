// Command arca-gateway is the only process that holds the ARCA certificate
// private key. It exposes the gRPC service in proto/arca/v1 to core and
// talks SOAP to ARCA (WSAA, ws_sr_constancia_inscripcion, WSCDC).
// With ARCA_ENV=sim it answers from the local simulator.
//
// It deliberately does not load core's configuration: it never sees
// DATABASE_URL, MASTER_KEY or JWT_SIGNING_KEY, and core never sees ARCA_KEY.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/Tramo-Labs/hq/eng/core/internal/arca"
	"github.com/Tramo-Labs/hq/eng/core/internal/arca/gateway"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("arca-gateway", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := arca.LoadConfig()
	if err != nil {
		return err
	}
	addr := os.Getenv("GRPC_ADDR")
	if addr == "" {
		addr = ":9090"
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := gateway.Run(ctx, cfg, lis); err != nil {
		return err
	}
	slog.Info("arca-gateway stopped")
	return nil
}
