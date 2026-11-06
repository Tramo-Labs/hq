package app_test

import (
	"testing"

	"github.com/google/uuid"
)

// member registers email with a tenant of its own, has inviter add it to
// tenant with role, and logs it into that tenant. (An invited user without
// an account cannot log in yet: F8.)
func (c client) member(inviter account, tenant, slug, email, role string) account {
	c.t.Helper()
	m := c.registerAndLogin(slug, email)
	if r := c.do("POST", "/v1/tenant/members", inviter.access, map[string]any{"email": email, "role": role}, idem()...); r.status != 201 {
		c.t.Fatalf("add %s as %s: %d %s", email, role, r.status, r.raw)
	}
	l := c.do("POST", "/v1/auth/login", "", map[string]any{"email": email, "password": m.password, "tenantId": tenant, "tokenDelivery": "body"})
	if l.status != 200 || l.str("role") != role {
		c.t.Fatalf("login %s into %s: %d %s", email, tenant, l.status, l.raw)
	}
	m.access, m.refresh, m.tenant = l.str("accessToken"), l.str("refreshToken"), tenant
	return m
}

// F1 (cliente, qa/ESCENARIOS.md): an Idempotency-Key reused by another user
// of the same tenant must not replay the first user's response and skip the
// role check.
func TestIdempotencyKeyIsBoundToTheCallerNotOnlyTheTenant(t *testing.T) {
	c := client{t, server(t)}
	own := c.registerAndLogin("molino-f1", "owner@f1.test")
	viewer := c.member(own, own.tenant, "viewer-f1", "viewer@f1.test", "viewer")
	approver := c.member(own, own.tenant, "approver-f1", "approver@f1.test", "approver")

	// 1) Same key, same body: owner creates an account, the viewer replays.
	k := []string{"Idempotency-Key", uuid.NewString()}
	body := map[string]any{"bank": "galicia", "name": "Cuenta secreta"}
	if r := c.do("POST", "/v1/ledger/accounts", own.access, body, k...); r.status != 201 {
		t.Fatalf("owner create: %d %s", r.status, r.raw)
	}
	r := c.do("POST", "/v1/ledger/accounts", viewer.access, body, k...)
	if r.status != 403 || r.header.Get("Idempotent-Replayed") != "" {
		t.Fatalf("viewer with the owner's key: %d %s (replayed=%q)", r.status, r.raw, r.header.Get("Idempotent-Replayed"))
	}

	// 2) A decision: the approver decides, the viewer replays the key.
	acct := c.do("POST", "/v1/ledger/accounts", own.access, map[string]any{"bank": "bancor", "name": "CC"}, idem()...)
	c.upload("/v1/ledger/accounts/"+acct.str("id")+"/statements", own.access, "oct.csv", ledgerFixture(t, "bancor_ok.csv"), uuid.NewString())
	c.do("POST", "/v1/matching/runs", own.access, map[string]any{"period": "2026-10"}, idem()...)
	exc := c.do("GET", "/v1/matching/pending?tray=exception", own.access, nil)
	items, _ := exc.body["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("no exceptions to decide: %s", exc.raw)
	}
	mv := items[0].(map[string]any)["movementId"].(string)
	k3 := []string{"Idempotency-Key", uuid.NewString()}
	dec := map[string]any{"action": "other", "note": "honorarios"}
	if r := c.do("POST", "/v1/matching/movements/"+mv+"/decision", approver.access, dec, k3...); r.status != 201 {
		t.Fatalf("approver decision: %d %s", r.status, r.raw)
	}
	r = c.do("POST", "/v1/matching/movements/"+mv+"/decision", viewer.access, dec, k3...)
	// No replay and no decision for the viewer. (Matching checks its own key
	// before the role and answers 422 here; 403 once datos reorders it.)
	if r.status == 201 || r.header.Get("Idempotent-Replayed") != "" || (r.status != 403 && r.status != 422) {
		t.Fatalf("viewer with the approver's key: %d %s", r.status, r.raw)
	}

	// The same caller still gets its own replay.
	if r := c.do("POST", "/v1/ledger/accounts", own.access, body, k...); r.status != 201 || r.header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("owner's own replay: %d %v", r.status, r.header)
	}
}
