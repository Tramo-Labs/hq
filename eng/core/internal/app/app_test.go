package app_test

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	apigen "github.com/Tramo-Labs/hq/eng/core/gen/api"
	"github.com/Tramo-Labs/hq/eng/core/internal/app"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/crypto"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db/dbtest"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/mail"
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
	// Mail lands in an outbox, so every test can follow an invitation.
	ids.Mailer, ids.AppBaseURL = &mail.Outbox{Path: filepath.Join(t.TempDir(), "outbox")}, "https://app.example.test"
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
	if ob, ok := deps.Identity.Mailer.(*mail.Outbox); ok {
		outboxes.Store(srv.URL, ob.Path)
		t.Cleanup(func() { outboxes.Delete(srv.URL) })
	}
	return srv
}

// outboxes maps a test server's URL to the outbox its mail goes to.
var outboxes sync.Map

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

// D-18: the wired router answers the portal's preflight and gives a foreign
// origin no CORS headers.
func TestCORSOnTheRouter(t *testing.T) {
	deps := newDeps(t)
	origins, err := httpx.ParseOrigins("https://app.tramo.test", false)
	if err != nil {
		t.Fatal(err)
	}
	deps.CORSOrigins = origins
	srv := serve(t, deps)
	do := func(method, origin string) *http.Response {
		req, _ := http.NewRequest(method, srv.URL+"/v1/auth/login", nil)
		req.Header.Set("Origin", origin)
		if method == http.MethodOptions {
			req.Header.Set("Access-Control-Request-Method", "POST")
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	res := do(http.MethodOptions, "https://app.tramo.test")
	if res.StatusCode != 204 || res.Header.Get("Access-Control-Allow-Origin") != "https://app.tramo.test" ||
		res.Header.Get("Access-Control-Allow-Credentials") != "true" || res.Header.Get("X-Request-Id") == "" {
		t.Errorf("portal preflight: %d %v", res.StatusCode, res.Header)
	}
	res = do(http.MethodPost, "https://app.tramo.test")
	if res.Header.Get("Access-Control-Expose-Headers") != "Retry-After, X-Request-Id, Content-Disposition" {
		t.Errorf("portal request: %v", res.Header)
	}
	for _, m := range []string{http.MethodOptions, http.MethodPost} {
		res = do(m, "https://evil.test")
		if v := res.Header.Get("Access-Control-Allow-Origin"); v != "" {
			t.Errorf("%s from a foreign origin: allow origin %q", m, v)
		}
	}
}

// An export downloaded from the portal keeps its file name: the browser may
// read Content-Disposition only if CORS exposes it (frontend found the
// generic name, D-18).
func TestExportFileNameIsReadableFromThePortal(t *testing.T) {
	c := serveWithPortal(t)
	own := c.registerAndLogin("molino-cd", "owner@cd.test")
	r := c.do("GET", "/v1/exports/audit", own.access, nil, "Origin", portalOrigin)
	if r.status != 200 || !strings.Contains(r.header.Get("Content-Disposition"), `filename="audit.csv"`) {
		t.Fatalf("export: %d %v", r.status, r.header)
	}
	exposed := strings.Split(r.header.Get("Access-Control-Expose-Headers"), ", ")
	if !slices.Contains(exposed, "Content-Disposition") || r.header.Get("Access-Control-Allow-Origin") != portalOrigin {
		t.Fatalf("the portal cannot read the file name: %v", r.header)
	}
}
