// Command api serves the core REST API described in api/openapi.yaml.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/config"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	mux := http.NewServeMux()
	httpx.Health(mux, func(context.Context) error { return nil })
	mux.Handle("GET /docs/", http.StripPrefix("/docs/", http.FileServer(http.Dir("api"))))
	if err := httpx.Run(cfg.HTTPAddr, httpx.RequestID(mux)); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}
