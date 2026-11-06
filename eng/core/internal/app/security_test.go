package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// member registers email with a tenant of its own, has inviter add it to
// tenant with role, accepts the invitation (D-19) and logs it into that
// tenant.
func (c client) member(inviter account, tenant, slug, email, role string) account {
	c.t.Helper()
	m := c.registerAndLogin(slug, email)
	if r := c.do("POST", "/v1/tenant/members", inviter.access, map[string]any{"email": email, "role": role}, idem()...); r.status != 201 {
		c.t.Fatalf("add %s as %s: %d %s", email, role, r.status, r.raw)
	}
	if r := c.join(email, nil); r.status != 200 {
		c.t.Fatalf("accept %s: %d %s", email, r.status, r.raw)
	}
	l := c.do("POST", "/v1/auth/login", "", map[string]any{"email": email, "password": m.password, "tenantId": tenant, "tokenDelivery": "body"})
	if l.status != 200 || l.str("role") != role {
		c.t.Fatalf("login %s into %s: %d %s", email, tenant, l.status, l.raw)
	}
	m.access, m.refresh, m.tenant = l.str("accessToken"), l.str("refreshToken"), tenant
	return m
}

// F1, qa's surviving mutation: the key is bound to the role too. The same
// user, demoted from approver to viewer, repeating the key after a refresh
// gets 403 and no replay of what the approver got.
func TestIdempotencyKeyDoesNotOutliveARoleChange(t *testing.T) {
	deps := newDeps(t)
	c := client{t, serve(t, deps)}
	own := c.registerAndLogin("molino-f1r", "owner@f1r.test")
	ap := c.member(own, own.tenant, "approver-f1r", "approver@f1r.test", "approver")
	acct := c.do("POST", "/v1/ledger/accounts", own.access, map[string]any{"bank": "bancor", "name": "CC"}, idem()...)
	path := "/v1/ledger/accounts/" + acct.str("id") + "/statements"
	key := uuid.NewString()
	if r := c.upload(path, ap.access, "oct.csv", ledgerFixture(t, "bancor_ok.csv"), key); r.status != 201 {
		t.Fatalf("approver upload: %d %s", r.status, r.raw)
	}
	if _, err := deps.DB.Pool.Exec(context.Background(),
		`update memberships set role = 'viewer' where tenant_id = $1 and user_id = (select id from users where email = $2)`,
		own.tenant, ap.email); err != nil {
		t.Fatal(err)
	}
	ref := c.do("POST", "/v1/auth/refresh", "", map[string]any{"refreshToken": ap.refresh, "tokenDelivery": "body"})
	if ref.status != 200 || ref.str("role") != "viewer" {
		t.Fatalf("refresh after demotion: %d %s", ref.status, ref.raw)
	}
	r := c.upload(path, ref.str("accessToken"), "oct.csv", ledgerFixture(t, "bancor_ok.csv"), key)
	if r.status != 403 || r.header.Get("Idempotent-Replayed") != "" {
		t.Fatalf("demoted user repeating the key: %d %s (replayed=%q)", r.status, r.raw, r.header.Get("Idempotent-Replayed"))
	}
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

	// 3) Same tenant, same role, same key and body: two approvers. Only the
	// user tells them apart, so the second runs its own handler, no replay.
	approver2 := c.member(own, own.tenant, "approver2-f1", "approver2@f1.test", "approver")
	mv2 := items[len(items)-1].(map[string]any)["movementId"].(string)
	k4 := []string{"Idempotency-Key", uuid.NewString()}
	first := c.do("POST", "/v1/matching/movements/"+mv2+"/decision", approver.access, dec, k4...)
	if first.status != 201 {
		t.Fatalf("first approver: %d %s", first.status, first.raw)
	}
	second := c.do("POST", "/v1/matching/movements/"+mv2+"/decision", approver2.access, dec, k4...)
	if second.header.Get("Idempotent-Replayed") != "" || second.raw == first.raw {
		t.Fatalf("second approver got the first one's answer: %d %s (replayed=%q)", second.status, second.raw, second.header.Get("Idempotent-Replayed"))
	}

	// The same caller still gets its own replay.
	if r := c.do("POST", "/v1/ledger/accounts", own.access, body, k...); r.status != 201 || r.header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("owner's own replay: %d %v", r.status, r.header)
	}
}
