package httpx_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

func TestRequestMeta(t *testing.T) {
	var got httpx.Meta
	h := httpx.RequestMeta(nil)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = httpx.MetaFrom(r.Context())
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.7:51000"
	r.Header.Set("X-Request-Id", "abc-123")
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if got.RequestID != "abc-123" || w.Header().Get("X-Request-Id") != "abc-123" {
		t.Fatalf("request id: %q", got.RequestID)
	}
	if got.IP.String() != "203.0.113.7" {
		t.Fatalf("untrusted proxy header used: %s", got.IP)
	}

	// Malformed ids (log injection, huge values) are replaced.
	r.Header.Set("X-Request-Id", "bad\nid")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got.RequestID == "bad\nid" || len(got.RequestID) != 24 {
		t.Fatalf("malformed id kept: %q", got.RequestID)
	}

}

// X-Forwarded-For counts only when the TCP peer is a configured trusted
// proxy. A client that reaches the api directly cannot pick its own IP
// (which would let it dodge per IP throttles), even when proxies are
// configured.
func TestForwardedForOnlyFromTrustedProxies(t *testing.T) {
	proxies := []netip.Prefix{netip.MustParsePrefix("172.30.0.10/32"), netip.MustParsePrefix("fd00:30::/64")}
	var got httpx.Meta
	h := httpx.RequestMeta(proxies)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = httpx.MetaFrom(r.Context())
	}))
	for name, tc := range map[string]struct {
		peer, xff, want string
	}{
		"direct client spoofing":       {"203.0.113.7:51000", "198.51.100.1", "203.0.113.7"},
		"direct client, many hops":     {"203.0.113.7:51000", "6.6.6.6, 198.51.100.1", "203.0.113.7"},
		"trusted proxy, last hop wins": {"172.30.0.10:40000", "6.6.6.6, 198.51.100.1", "198.51.100.1"},
		"trusted proxy over ipv6":      {"[fd00:30::5]:40000", "2001:db8::1", "2001:db8::1"},
		"neighbour of the proxy":       {"172.30.0.11:40000", "198.51.100.1", "172.30.0.11"},
		"trusted proxy, junk header":   {"172.30.0.10:40000", "not an ip", "172.30.0.10"},
		"trusted proxy, no header":     {"172.30.0.10:40000", "", "172.30.0.10"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = tc.peer
		if tc.xff != "" {
			r.Header.Set("X-Forwarded-For", tc.xff)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		if got.IP.String() != tc.want {
			t.Errorf("%s: client ip %s, want %s", name, got.IP, tc.want)
		}
	}
}

func TestProblemAndRecover(t *testing.T) {
	h := httpx.RequestID(httpx.Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom with secret detail")
	})))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != 500 || w.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("got %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	var p httpx.Problem
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Code != httpx.CodeInternal || p.Status != 500 || p.RequestID == "" {
		t.Fatalf("problem: %+v", p)
	}
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("panic value leaked to the client")
	}
}

func TestReadinessHidesCause(t *testing.T) {
	mux := http.NewServeMux()
	httpx.Health(mux, func(context.Context) error { return errors.New("dial tcp 10.0.0.5: password authentication failed") })
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "password") {
		t.Fatalf("readyz: %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("healthz: %d", w.Code)
	}
}
