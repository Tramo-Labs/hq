package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tramo-Labs/hq/eng/core/internal/devseed"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/crypto"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

// The bug frontend found live: make dev built the api without the browser
// origins, so every cookie refresh and logout from the portal was a 403
// (a reload logged the person out). This runs make dev's own api builder
// on a seeded database: login with the cookie, then refresh and logout
// with the dev portal's Origin answer 200 and 204.
func TestMakeDevAcceptsTheDevPortalOrigin(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	s, err := devseed.LoadSecrets(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kr, _ := crypto.NewKeyring(s.MasterKey)
	if err := devseed.Seed(ctx, d, kr, s, filepath.Join("..", "..", "internal", "ledger", "testdata")); err != nil {
		t.Fatal(err)
	}
	signer, err := identity.NewSigner(s.JWTSeed, "dev")
	if err != nil {
		t.Fatal(err)
	}
	ids, err := identity.NewService(d, signer, kr, identity.Argon2Params{Memory: 8 * 1024, Time: 1, Threads: 1})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ base, origins, origin string }{
		{"", "", "http://localhost:3000"}, // make dev with nothing set
		{"http://localhost:3001", "", "http://localhost:3001"},
		{"", "http://127.0.0.1:3000", "http://127.0.0.1:3000"},
	} {
		t.Setenv("DEV_APP_BASE_URL", tc.base)
		t.Setenv("CORS_ALLOWED_ORIGINS", tc.origins)
		h, err := newAPI(d, ids, filepath.Join(t.TempDir(), "outbox"))
		if err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(h)
		post := func(path, cookie, origin string, body any) *http.Response {
			var b []byte
			if body != nil {
				b, _ = json.Marshal(body)
			}
			req, _ := http.NewRequest("POST", srv.URL+path, bytes.NewReader(b))
			if body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			if cookie != "" {
				req.Header.Set("Cookie", cookie)
			}
			req.Header.Set("Origin", origin)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			return res
		}
		cookieOf := func(r *http.Response) string { return strings.SplitN(r.Header.Get("Set-Cookie"), ";", 2)[0] }

		l := post("/v1/auth/login", "", tc.origin, map[string]any{"email": "owner@alfa.test", "password": s.Passwords["owner@alfa.test"]})
		if l.StatusCode != 200 || !strings.HasPrefix(cookieOf(l), "tramo_refresh=") {
			t.Fatalf("%+v: login %d", tc, l.StatusCode)
		}
		if l.Header.Get("Access-Control-Allow-Origin") != tc.origin {
			t.Errorf("%+v: no CORS for the dev portal on login: %v", tc, l.Header)
		}
		r := post("/v1/auth/refresh", cookieOf(l), tc.origin, nil)
		if r.StatusCode != 200 {
			t.Fatalf("%+v: cookie refresh from the dev portal: %d (a reload would log the person out)", tc, r.StatusCode)
		}
		if out := post("/v1/auth/logout", cookieOf(r), tc.origin, nil); out.StatusCode != 204 {
			t.Fatalf("%+v: logout from the dev portal: %d", tc, out.StatusCode)
		}
		// Still closed to anything else.
		l = post("/v1/auth/login", "", tc.origin, map[string]any{"email": "owner@alfa.test", "password": s.Passwords["owner@alfa.test"]})
		if r := post("/v1/auth/refresh", cookieOf(l), "https://evil.test", nil); r.StatusCode != 403 {
			t.Fatalf("%+v: foreign origin refresh: %d", tc, r.StatusCode)
		}
		srv.Close()
	}
}
