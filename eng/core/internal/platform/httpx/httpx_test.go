package httpx_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

func TestRequestMeta(t *testing.T) {
	var got httpx.Meta
	h := httpx.RequestMeta(false)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
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

	// Behind our proxy the last hop wins; a client supplied first hop does not.
	trusted := httpx.RequestMeta(true)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = httpx.MetaFrom(r.Context())
	}))
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 198.51.100.1")
	trusted.ServeHTTP(httptest.NewRecorder(), r)
	if got.IP.String() != "198.51.100.1" {
		t.Fatalf("trusted proxy ip: %s", got.IP)
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
