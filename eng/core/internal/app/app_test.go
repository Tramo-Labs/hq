package app_test

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	apigen "github.com/Tramo-Labs/hq/eng/core/gen/api"
	"github.com/Tramo-Labs/hq/eng/core/internal/app"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/crypto"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db/dbtest"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

// fastArgon keeps the suite quick under -race; production uses DefaultArgon2.
var fastArgon = identity.Argon2Params{Memory: 8 * 1024, Time: 1, Threads: 1}

func newDeps(t *testing.T) app.Deps {
	t.Helper()
	d := dbtest.New(t)
	seed, master := make([]byte, 32), make([]byte, 32)
	_, _ = rand.Read(seed)
	_, _ = rand.Read(master)
	signer, err := identity.NewSigner(seed, "test")
	if err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.NewKeyring(master)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := identity.NewService(d, signer, kr, fastArgon)
	if err != nil {
		t.Fatal(err)
	}
	return app.Deps{DB: d, Identity: ids}
}

func serve(t *testing.T, deps app.Deps) *httptest.Server {
	t.Helper()
	h, err := app.New(deps, app.NewHandlers(deps))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func server(t *testing.T) *httptest.Server { return serve(t, newDeps(t)) }

func TestPlatformEndpoints(t *testing.T) {
	srv := server(t)
	for path, want := range map[string]int{"/healthz": 204, "/readyz": 204} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want || res.Header.Get("X-Request-Id") == "" {
			t.Errorf("%s: %d, request id %q", path, res.StatusCode, res.Header.Get("X-Request-Id"))
		}
	}

	res, err := http.Get(srv.URL + "/v1/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var p httpx.Problem
	_ = json.NewDecoder(res.Body).Decode(&p)
	if res.StatusCode != 404 || p.Code != httpx.CodeNotFound || res.Header.Get("Content-Type") != "application/problem+json" {
		t.Fatalf("unknown route: %d %+v", res.StatusCode, p)
	}
}

// Every POST that answers 201 must declare the IdempotencyKey parameter.
// This keeps eng/ARQUITECTURA.md section 7 true as modules add paths.
func TestEveryCreatingPostRequiresIdempotencyKey(t *testing.T) {
	spec, err := apigen.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	for path, item := range spec.Paths.Map() {
		op := item.Post
		if op == nil || op.Responses.Value("201") == nil {
			continue
		}
		found := false
		for _, p := range append(item.Parameters, op.Parameters...) {
			if p.Value != nil && p.Value.In == "header" && p.Value.Name == "Idempotency-Key" && p.Value.Required {
				found = true
			}
		}
		if !found {
			t.Errorf("POST %s creates (201) but does not require Idempotency-Key", path)
		}
	}
}

// Error responses declared in the spec use the shared Problem schema.
func TestErrorResponsesUseProblem(t *testing.T) {
	spec, err := apigen.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			for code, r := range op.Responses.Map() {
				if !strings.HasPrefix(code, "4") && !strings.HasPrefix(code, "5") {
					continue
				}
				if r.Ref != "" && r.Ref != "#/components/responses/Problem" {
					t.Errorf("%s %s %s: uses %s", method, path, code, r.Ref)
				}
				if r.Ref == "" && r.Value.Content.Get("application/problem+json") == nil {
					t.Errorf("%s %s %s: not application/problem+json", method, path, code)
				}
			}
		}
	}
}
