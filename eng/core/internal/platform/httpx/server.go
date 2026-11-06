// Package httpx holds the pieces every HTTP server in core shares:
// health endpoints, graceful shutdown and the request id middleware.
package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// Health registers the liveness and readiness endpoints. ready is called on
// each readiness probe and should check the database and the gateway.
func Health(mux *http.ServeMux, ready func(context.Context) error) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := ready(ctx); err != nil {
			Logger(r.Context()).Warn("not ready", "err", err)
			WriteProblem(w, r, http.StatusServiceUnavailable, "not_ready", "")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// RequestID is RequestMeta without trusting proxy headers.
func RequestID(next http.Handler) http.Handler { return RequestMeta(false)(next) }

// Meta is what every request carries for logs and the audit log.
type Meta struct {
	RequestID string
	IP        netip.Addr
}

type metaKey struct{}

// MetaFrom returns the request metadata stored by RequestMeta.
func MetaFrom(ctx context.Context) (Meta, bool) {
	m, ok := ctx.Value(metaKey{}).(Meta)
	return m, ok
}

// RequestMeta gives every request an id (a well formed incoming
// X-Request-Id is kept), echoes it, and stores it with the client IP in the
// context. With trustProxy the IP is the last X-Forwarded-For hop, which is
// the one our own reverse proxy (Caddy) appended; otherwise RemoteAddr.
func RequestMeta(trustProxy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-Id")
			if !validRequestID(id) {
				var b [12]byte
				_, _ = rand.Read(b[:])
				id = hex.EncodeToString(b[:])
			}
			w.Header().Set("X-Request-Id", id)
			m := Meta{RequestID: id, IP: clientIP(r, trustProxy)}
			ctx := context.WithValue(r.Context(), metaKey{}, m)
			log := slog.Default().With("request_id", id)
			next.ServeHTTP(w, r.WithContext(WithLogger(ctx, log)))
		})
	}
}

func validRequestID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func clientIP(r *http.Request, trustProxy bool) netip.Addr {
	if trustProxy {
		if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
			hops := strings.Split(xff[len(xff)-1], ",")
			if a, err := netip.ParseAddr(strings.TrimSpace(hops[len(hops)-1])); err == nil {
				return a
			}
		}
	}
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		return ap.Addr()
	}
	a, _ := netip.ParseAddr(r.RemoteAddr)
	return a
}

type loggerKey struct{}

// WithLogger stores a request scoped logger in the context.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

// Logger returns the request scoped logger or the default one.
func Logger(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// Run serves until SIGINT or SIGTERM, then drains for up to 15 seconds.
func Run(addr string, h http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("listening", "addr", addr)
	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}
