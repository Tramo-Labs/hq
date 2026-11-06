package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Tramo-Labs/hq/eng/core/internal/app"
)

// grant gives email (registered with a tenant of its own) role in tenant
// directly in the database, for roles no HTTP caller can grant, and logs it
// into that tenant.
func (c client) grant(deps app.Deps, tenant, slug, email, role string) account {
	c.t.Helper()
	m := c.registerAndLogin(slug, email)
	if _, err := deps.DB.Pool.Exec(context.Background(),
		`insert into memberships (user_id, tenant_id, role) select id, $1, $2::role from users where email = $3`,
		tenant, role, email); err != nil {
		c.t.Fatal(err)
	}
	l := c.do("POST", "/v1/auth/login", "", map[string]any{"email": email, "password": m.password, "tenantId": tenant, "tokenDelivery": "body"})
	if l.status != 200 || l.str("role") != role {
		c.t.Fatalf("login %s into %s: %d %s", email, tenant, l.status, l.raw)
	}
	m.access, m.refresh, m.tenant = l.str("accessToken"), l.str("refreshToken"), tenant
	return m
}

// roles builds one account per role in a fresh tenant.
func (c client) roles(deps app.Deps, slug string) map[string]account {
	c.t.Helper()
	own := c.registerAndLogin(slug, "owner@"+slug+".test")
	return map[string]account{
		"owner":      own,
		"approver":   c.member(own, own.tenant, slug+"-ap", "approver@"+slug+".test", "approver"),
		"viewer":     c.member(own, own.tenant, slug+"-vw", "viewer@"+slug+".test", "viewer"),
		"accountant": c.grant(deps, own.tenant, slug+"-ac", "accountant@"+slug+".test", "accountant"),
		"operator":   c.grant(deps, own.tenant, slug+"-op", "operator@"+slug+".test", "operator"),
	}
}

// F6 (cliente, qa/ESCENARIOS.md): spec §1 gives "Descargar todo" and "Ver el
// audit log" only to owner, accountant and operator.
func TestExportsAreOnlyForOwnerAccountantAndOperator(t *testing.T) {
	deps := newDeps(t)
	c := client{t, serve(t, deps)}
	who := c.roles(deps, "molino-f6")
	allowed := map[string]bool{"owner": true, "accountant": true, "operator": true, "approver": false, "viewer": false}
	paths := []string{"/v1/exports", "/v1/exports/movements", "/v1/exports/matches", "/v1/exports/audit"}
	for role, ok := range allowed {
		for _, p := range paths {
			status, _, body := c.get(p, who[role].access)
			switch {
			case ok && status != 200:
				t.Errorf("%s %s: %d %s", role, p, status, body)
			case !ok && status != 403:
				t.Errorf("%s %s: %d, want 403", role, p, status)
			}
		}
	}
}

// F5 (cliente, qa/ESCENARIOS.md): spec §1, "toda acción de esta tabla queda
// en el audit log con quién, cuándo y desde dónde". Every authorization
// denial is audited with the caller, the resolved client IP, the operation
// and the resource, and nothing from the request body.
func TestEveryDenialIsAudited(t *testing.T) {
	deps := newDeps(t)
	c := client{t, serve(t, deps)}
	own := c.registerAndLogin("molino-f5", "owner@f5.test")
	viewer := c.member(own, own.tenant, "viewer-f5", "viewer@f5.test", "viewer")
	viewerID := c.do("GET", "/v1/me", viewer.access, nil).str("userId")
	acct := c.do("POST", "/v1/ledger/accounts", own.access, map[string]any{"bank": "bancor", "name": "CC"}, idem()...)

	attempts := []struct {
		op, path string
		do       func() int
	}{
		{"createAccount", "/v1/ledger/accounts", func() int {
			return c.do("POST", "/v1/ledger/accounts", viewer.access, map[string]any{"bank": "galicia", "name": "Cuenta secreta"}, idem()...).status
		}},
		{"uploadStatement", "/v1/ledger/accounts/" + acct.str("id") + "/statements", func() int {
			return c.upload("/v1/ledger/accounts/"+acct.str("id")+"/statements", viewer.access, "oct.csv", ledgerFixture(t, "bancor_ok.csv"), uuid.NewString()).status
		}},
		{"runMatching", "/v1/matching/runs", func() int {
			return c.do("POST", "/v1/matching/runs", viewer.access, map[string]any{"period": "2026-10"}, idem()...).status
		}},
		{"addMember", "/v1/tenant/members", func() int {
			return c.do("POST", "/v1/tenant/members", viewer.access, map[string]any{"email": "intruso@f5.test", "role": "owner"}, idem()...).status
		}},
		{"getExport", "/v1/exports/audit", func() int { s, _, _ := c.get("/v1/exports/audit", viewer.access); return s }},
		{"listTenants", "/v1/tenants", func() int { return c.do("GET", "/v1/tenants", viewer.access, nil).status }},
	}
	for _, a := range attempts {
		if s := a.do(); s != 403 {
			t.Fatalf("%s by the viewer: %d, want 403", a.op, s)
		}
	}

	type row struct{ user, ip, subject, detail string }
	got := map[string]row{}
	rs, err := deps.DB.Pool.Query(context.Background(), `
		select coalesce(user_id::text, ''), coalesce(host(ip), ''), subject, coalesce(detail::text, ''), coalesce(request_id, '')
		from audit_log where tenant_id = $1 and action = 'authz.denied'`, own.tenant)
	if err != nil {
		t.Fatal(err)
	}
	for rs.Next() {
		var r row
		var req string
		if err := rs.Scan(&r.user, &r.ip, &r.subject, &r.detail, &req); err != nil {
			t.Fatal(err)
		}
		if req == "" {
			t.Errorf("denial without request id: %+v", r)
		}
		got[r.subject] = r
	}
	rs.Close()
	if len(got) != len(attempts) {
		t.Fatalf("audited denials: %d, want %d: %v", len(got), len(attempts), got)
	}
	for _, a := range attempts {
		r, ok := got["operation:"+a.op]
		if !ok {
			t.Errorf("%s denial not audited", a.op)
			continue
		}
		if r.user != viewerID || r.ip != "127.0.0.1" {
			t.Errorf("%s: user %q ip %q", a.op, r.user, r.ip)
		}
		if !strings.Contains(r.detail, `"path": "`+a.path+`"`) || !strings.Contains(r.detail, `"role": "viewer"`) {
			t.Errorf("%s detail: %s", a.op, r.detail)
		}
		for _, leak := range []string{"Cuenta secreta", "intruso", "2026-10", "BANCOR", "@"} {
			if strings.Contains(r.detail, leak) {
				t.Errorf("%s detail carries request data %q: %s", a.op, leak, r.detail)
			}
		}
	}
	// The owner reads them in the audit export.
	if _, _, csvBody := c.get("/v1/exports/audit", own.access); strings.Count(csvBody, "authz.denied") != len(attempts) {
		t.Errorf("audit export:\n%s", csvBody)
	}
}
