package app_test

import (
	"context"
	"encoding/csv"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// get returns status, headers and raw body without JSON decoding (CSV).
func (c client) get(path, token string) (int, http.Header, string) {
	c.t.Helper()
	req, _ := http.NewRequest("GET", c.srv.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, string(b)
}

func stepsByKind(t *testing.T, r resp) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	steps, _ := r.body["steps"].([]any)
	for _, s := range steps {
		m := s.(map[string]any)
		out[m["kind"].(string)] = m
	}
	if len(out) != 3 {
		t.Fatalf("steps: %s", r.raw)
	}
	return out
}

func TestPortalOnboardingStatus(t *testing.T) {
	c := client{t, server(t)}
	a := c.registerAndLogin("molino-portal", "owner@portal.test")

	r := c.do("GET", "/v1/me/tenant", a.access, nil)
	if r.status != 200 || r.str("id") != a.tenant || r.str("role") != "owner" || r.body["openExceptions"] != float64(0) {
		t.Fatalf("me/tenant: %d %s", r.status, r.raw)
	}
	steps := stepsByKind(t, r)
	for kind, want := range map[string]string{"arca": "pending", "bank": "pending", "erp": "pending"} {
		if steps[kind]["status"] != want {
			t.Errorf("%s: %v", kind, steps[kind])
		}
	}

	// A statement accepted on an account verifies the bank step, and only it.
	acct := c.do("POST", "/v1/ledger/accounts", a.access, map[string]any{"bank": "bancor", "name": "Cuenta corriente"}, idem()...)
	if acct.status != 201 {
		t.Fatalf("account: %d %s", acct.status, acct.raw)
	}
	up := c.upload("/v1/ledger/accounts/"+acct.str("id")+"/statements", a.access, "oct.csv", ledgerFixture(t, "bancor_ok.csv"), uuid.NewString())
	if up.status != 201 || up.str("status") != "accepted" {
		t.Fatalf("upload: %d %s", up.status, up.raw)
	}
	steps = stepsByKind(t, c.do("GET", "/v1/me/tenant", a.access, nil))
	if steps["bank"]["status"] != "verified" || steps["arca"]["status"] != "pending" || steps["erp"]["status"] != "pending" {
		t.Fatalf("after statement: %v", steps)
	}

	// A quarantined statement alone does not verify the bank step.
	b := c.registerAndLogin("molino-q", "owner@q.test")
	acctB := c.do("POST", "/v1/ledger/accounts", b.access, map[string]any{"bank": "bancor", "name": "CC"}, idem()...)
	c.upload("/v1/ledger/accounts/"+acctB.str("id")+"/statements", b.access, "bad.csv", ledgerFixture(t, "bancor_bad_balance.csv"), uuid.NewString())
	if s := stepsByKind(t, c.do("GET", "/v1/me/tenant", b.access, nil)); s["bank"]["status"] != "pending" {
		t.Fatalf("quarantined statement verified the bank step: %v", s["bank"])
	}
}

// A CUIT on file is not an ARCA verification (producto's note): the step
// stays pending, with the detail that says why, and the CUIT is shown.
func TestArcaStepStaysPendingWithCUITOnFile(t *testing.T) {
	c := client{t, server(t)}
	r := c.do("POST", "/v1/auth/register", "", map[string]any{
		"email": "owner@cuit.test", "password": "a long enough password",
		"tenantName": "Molino con CUIT", "tenantSlug": "con-cuit", "cuit": "30000000015",
	}, idem()...)
	if r.status != 201 {
		t.Fatalf("register with cuit: %d %s", r.status, r.raw)
	}
	l := c.do("POST", "/v1/auth/login", "", map[string]any{"email": "owner@cuit.test", "password": "a long enough password", "tokenDelivery": "body"})
	me := c.do("GET", "/v1/me/tenant", l.str("accessToken"), nil)
	if me.str("cuit") != "30000000015" {
		t.Fatalf("cuit not shown: %s", me.raw)
	}
	arca := stepsByKind(t, me)["arca"]
	if arca["status"] != "pending" || arca["detail"] != "CUIT on file; ARCA verification is not connected yet" || arca["verifiedAt"] != nil {
		t.Fatalf("arca step with a CUIT on file: %v", arca)
	}
}

func TestConsoleListsOnlyTheCallersTenants(t *testing.T) {
	deps := newDeps(t)
	c := client{t, serve(t, deps)}
	a := c.registerAndLogin("alfa", "owner@alfa.test")
	b := c.registerAndLogin("beta", "owner@beta.test")
	c.registerAndLogin("gamma", "owner@gamma.test") // the operator has nothing here

	if r := c.do("GET", "/v1/tenants", a.access, nil); r.status != 403 {
		t.Fatalf("owner on the console list: %d", r.status)
	}
	// An operator of alfa and beta (granted directly: only operators grant operator).
	op := c.registerAndLogin("tramo-ops", "ops@tramo.test")
	for _, tenant := range []string{a.tenant, b.tenant} {
		if _, err := deps.DB.Pool.Exec(context.Background(),
			`insert into memberships (user_id, tenant_id, role) select id, $1, 'operator' from users where email = 'ops@tramo.test'`, tenant); err != nil {
			t.Fatal(err)
		}
	}
	r := c.do("GET", "/v1/tenants", op.access, nil)
	items, _ := r.body["items"].([]any)
	if r.status != 200 || len(items) != 2 {
		t.Fatalf("console: %d %s", r.status, r.raw)
	}
	seen := map[string]string{}
	for _, it := range items {
		m := it.(map[string]any)
		seen[m["id"].(string)] = m["role"].(string)
	}
	if seen[a.tenant] != "operator" || seen[b.tenant] != "operator" || len(seen) != 2 {
		t.Fatalf("console tenants: %v", seen)
	}
	// No tenant id from the request is honoured: the query string is ignored.
	if r := c.do("GET", "/v1/me/tenant?tenantId="+b.tenant, a.access, nil); r.str("id") != a.tenant {
		t.Fatalf("tenant from the query string: %s", r.raw)
	}
}

func TestExportsAreTheTenantsOwnAndFormulaSafe(t *testing.T) {
	c := client{t, server(t)}
	a := c.registerAndLogin("alfa", "owner@alfa.test")
	b := c.registerAndLogin("beta", "owner@beta.test")
	acct := c.do("POST", "/v1/ledger/accounts", a.access, map[string]any{"bank": "bancor", "name": "CC"}, idem()...)
	c.upload("/v1/ledger/accounts/"+acct.str("id")+"/statements", a.access, "oct.csv", ledgerFixture(t, "bancor_ok.csv"), uuid.NewString())

	list := c.do("GET", "/v1/exports", a.access, nil)
	byKind := map[string]map[string]any{}
	for _, it := range list.body["items"].([]any) {
		m := it.(map[string]any)
		byKind[m["kind"].(string)] = m
	}
	if byKind["movements"]["rows"] != float64(10) || byKind["movements"]["available"] != true ||
		byKind["matches"]["available"] != true || byKind["matches"]["rows"] != float64(0) {
		t.Fatalf("exports: %s", list.raw)
	}

	status, hdr, body := c.get("/v1/exports/movements", a.access)
	rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if status != 200 || err != nil || len(rows) != 11 || rows[0][0] != "date" || !strings.Contains(hdr.Get("Content-Type"), "text/csv") {
		t.Fatalf("movements csv: %d %v %d rows %s", status, err, len(rows), hdr.Get("Content-Type"))
	}
	if !strings.Contains(hdr.Get("Content-Disposition"), "movements.csv") {
		t.Fatalf("disposition: %s", hdr.Get("Content-Disposition"))
	}
	// Tenant b downloads nothing of tenant a.
	if _, _, other := c.get("/v1/exports/movements", b.access); strings.Count(other, "\n") != 1 {
		t.Fatalf("tenant b got tenant a's movements:\n%s", other)
	}
	// The audit export is the tenant's own trail.
	as, _, audit := c.get("/v1/exports/audit", a.access)
	if as != 200 || !strings.Contains(audit, "identity.register") || strings.Contains(audit, b.tenant) {
		t.Fatalf("audit csv: %d\n%s", as, audit)
	}
	// A date range bounds the rows; an inverted one is refused.
	if s, _, _ := c.get("/v1/exports/movements?from=2026-12-01&to=2026-11-01", a.access); s != 400 {
		t.Fatalf("inverted range: %d", s)
	}

	// Matches export and metrics are built by datos now (no longer 501):
	// header only and no data while nothing has run.
	if s, _, m := c.get("/v1/exports/matches", a.access); s != 200 || strings.Count(m, "\n") != 1 {
		t.Errorf("matches export before any run: %d %q", s, m)
	}
	if r := c.do("GET", "/v1/metrics?period=2026-11", a.access, nil); r.status != 200 {
		t.Errorf("metrics: %d %s", r.status, r.raw)
	}
}
