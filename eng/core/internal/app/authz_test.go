package app_test

import (
	"context"
	"testing"

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
