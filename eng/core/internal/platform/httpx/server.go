// Package httpx holds the pieces every HTTP server in core shares:
// health endpoints, graceful shutdown and the request id middleware.
package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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

// RequestID is RequestMeta without trusted proxies.
func RequestID(next http.Handler) http.Handler { return RequestMeta(nil)(next) }

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

// WithMeta stores m in ctx, for work that does not come through HTTP (a
// command line tool) but is still audited with a request id.
func WithMeta(ctx context.Context, m Meta) context.Context {
	return context.WithValue(ctx, metaKey{}, m)
}

// RequestMeta gives every request an id (a well formed incoming
// X-Request-Id is kept), echoes it, and stores it with the client IP in the
// context. The client IP is the TCP peer. Only when that peer is one of
// trustedProxies (our own Caddy) is X-Forwarded-For used, and then only its
// last hop, the one the proxy appended; anything a client wrote before it
// is ignored.
func RequestMeta(trustedProxies []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-Id")
			if !validRequestID(id) {
				var b [12]byte
				_, _ = rand.Read(b[:])
				id = hex.EncodeToString(b[:])
			}
			w.Header().Set("X-Request-Id", id)
			m := Meta{RequestID: id, IP: clientIP(r, trustedProxies)}
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

func clientIP(r *http.Request, trustedProxies []netip.Prefix) netip.Addr {
	peer := peerIP(r.RemoteAddr)
	if !trusted(peer, trustedProxies) {
		return peer
	}
	// Walk the hops from the right, the ones our proxies appended. Each
	// trusted hop is one of our own proxies (Caddy, the web's Next server)
	// and is skipped; the first hop that is not ours is the client.
	// Anything to its left was written by the client and never read. A hop
	// that does not parse stops the walk at the last proxy seen.
	ip := peer
	xff := r.Header.Values("X-Forwarded-For")
	for i := len(xff) - 1; i >= 0; i-- {
		hops := strings.Split(xff[i], ",")
		for j := len(hops) - 1; j >= 0; j-- {
			a, err := netip.ParseAddr(strings.TrimSpace(hops[j]))
			if err != nil {
				return ip
			}
			ip = a.Unmap()
			if !trusted(ip, trustedProxies) {
				return ip
			}
		}
	}
	return ip
}

func peerIP(remote string) netip.Addr {
	if ap, err := netip.ParseAddrPort(remote); err == nil {
		return ap.Addr().Unmap()
	}
	a, _ := netip.ParseAddr(remote)
	return a.Unmap()
}

func trusted(peer netip.Addr, proxies []netip.Prefix) bool {
	if !peer.IsValid() {
		return false
	}
	for _, p := range proxies {
		if p.Contains(peer) {
			return true
		}
	}
	return false
}

// Narrowest allowed trusted proxy ranges: a proxy list names a few hosts,
// never a slice of the internet.
const (
	minProxyBits4 = 8
	minProxyBits6 = 32
)

// ParseTrustedProxies reads a comma separated list of IPs or CIDRs. IPv4
// ranges broader than /8 and IPv6 broader than /32 are refused. IPv4-mapped
// IPv6 entries (::ffff:a.b.c.d, ::ffff:0:0/96 is all of IPv4) are turned
// into the IPv4 range they cover, so they match peers (which are unmapped)
// and get the IPv4 floor.
func ParseTrustedProxies(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		p, err := netip.ParsePrefix(f)
		if err != nil {
			a, aerr := netip.ParseAddr(f)
			if aerr != nil {
				return nil, fmt.Errorf("trusted proxy %q is not an IP or CIDR", f)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		if p.Addr().Is4In6() {
			if p.Bits() < 96 {
				return nil, fmt.Errorf("trusted proxy %q covers more than the IPv4-mapped range", f)
			}
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		floor := minProxyBits6
		if p.Addr().Is4() {
			floor = minProxyBits4
		}
		if p.Bits() < floor {
			return nil, fmt.Errorf("trusted proxy %q is too broad (narrowest allowed /%d)", f, floor)
		}
		out = append(out, p.Masked())
	}
	return out, nil
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
