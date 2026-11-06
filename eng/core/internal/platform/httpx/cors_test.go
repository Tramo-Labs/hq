package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

func corsHandler(t *testing.T, list string) (http.Handler, *int) {
	t.Helper()
	origins, err := httpx.ParseOrigins(list, true)
	if err != nil {
		t.Fatal(err)
	}
	hits := 0
	return httpx.CORS(origins)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	})), &hits
}

var corsHeaders = []string{
	"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials",
	"Access-Control-Allow-Methods", "Access-Control-Allow-Headers",
	"Access-Control-Expose-Headers", "Access-Control-Max-Age",
}

// D-18: the portal calls the api from the browser. Only the listed origins
// get CORS headers, echoed exactly (never *), with credentials, the three
// request headers the web sends and the two it reads.
func TestCORSAllowsOnlyListedOrigins(t *testing.T) {
	h, hits := corsHandler(t, "https://app.tramo.test, https://console.tramo.test")

	r := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	r.Header.Set("Origin", "https://app.tramo.test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://app.tramo.test" {
		t.Errorf("allow origin %q", got)
	}
	if w.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("credentials not allowed")
	}
	if got := w.Header().Get("Access-Control-Expose-Headers"); got != "Retry-After, X-Request-Id, Content-Disposition" {
		t.Errorf("expose headers %q", got)
	}
	if w.Header().Get("Vary") != "Origin" {
		t.Errorf("vary %q", w.Header().Get("Vary"))
	}
	if *hits != 1 {
		t.Fatalf("handler ran %d times", *hits)
	}

	// Preflight: answered here, cached, never reaches the api.
	r = httptest.NewRequest(http.MethodOptions, "/v1/auth/login", nil)
	r.Header.Set("Origin", "https://console.tramo.test")
	r.Header.Set("Access-Control-Request-Method", "POST")
	r.Header.Set("Access-Control-Request-Headers", "content-type, idempotency-key")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Errorf("preflight status %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://console.tramo.test" {
		t.Errorf("preflight allow origin %q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Headers"); got != "Authorization, Content-Type, Idempotency-Key" {
		t.Errorf("allow headers %q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, PUT, PATCH, DELETE" {
		t.Errorf("allow methods %q", got)
	}
	if got := w.Header().Get("Access-Control-Max-Age"); got != "600" {
		t.Errorf("max age %q", got)
	}
	if w.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("preflight without credentials")
	}
	if *hits != 1 {
		t.Fatalf("preflight reached the handler")
	}
}

func TestCORSGivesForeignOriginsNothing(t *testing.T) {
	h, hits := corsHandler(t, "https://app.tramo.test")
	for _, origin := range []string{
		"https://evil.test",
		"https://app.tramo.test.evil.test",
		"http://app.tramo.test", // scheme matters
		"https://app.tramo.test:8443",
		"https://sub.app.tramo.test",
		"null",
		"*",
	} {
		for _, preflight := range []bool{false, true} {
			method := http.MethodPost
			if preflight {
				method = http.MethodOptions
			}
			r := httptest.NewRequest(method, "/v1/auth/refresh", nil)
			r.Header.Set("Origin", origin)
			if preflight {
				r.Header.Set("Access-Control-Request-Method", "POST")
			}
			w := httptest.NewRecorder()
			before := *hits
			h.ServeHTTP(w, r)
			for _, name := range corsHeaders {
				if v := w.Header().Get(name); v != "" {
					t.Errorf("%s (preflight %v): %s: %q", origin, preflight, name, v)
				}
			}
			if preflight && *hits != before {
				t.Errorf("%s: a foreign preflight reached the handler", origin)
			}
			if preflight && w.Code != http.StatusForbidden {
				t.Errorf("%s: foreign preflight status %d", origin, w.Code)
			}
		}
	}

	// No Origin at all (curl, server to server): passes untouched.
	r := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("no origin: %d %q", w.Code, w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestParseOriginsRefusesWildcardsAndPaths(t *testing.T) {
	for _, bad := range []string{
		"*", "https://*.tramo.test", "https://app.tramo.test/", "https://app.tramo.test/v1",
		"app.tramo.test", "https://user@app.tramo.test", "https://app.tramo.test?x=1",
		"ftp://app.tramo.test", "null",
	} {
		if _, err := httpx.ParseOrigins(bad, true); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := httpx.ParseOrigins("http://app.tramo.test", false); err == nil {
		t.Error("http accepted outside dev")
	}
	o, err := httpx.ParseOrigins(" HTTPS://App.Tramo.Test , http://localhost:3000", true)
	if err != nil {
		t.Fatal(err)
	}
	if !o.Allowed("https://app.tramo.test") || !o.Allowed("http://localhost:3000") || o.Allowed("http://localhost:3001") {
		t.Errorf("parsed %v", o)
	}
	if empty, _ := httpx.ParseOrigins("", false); empty.Allowed("") || empty.Allowed("https://app.tramo.test") {
		t.Error("an empty list allows something")
	}
}
