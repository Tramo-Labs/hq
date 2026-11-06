package app_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/Tramo-Labs/hq/eng/core/internal/app"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
)

// principal reads the caller behind a token through GET /v1/me.
func (c client) principal(a account) identity.Principal {
	c.t.Helper()
	r := c.do("GET", "/v1/me", a.access, nil)
	if r.status != 200 {
		c.t.Fatalf("me: %d %s", r.status, r.raw)
	}
	return identity.Principal{UserID: uuid.MustParse(r.str("userId")), TenantID: uuid.MustParse(r.str("activeTenantId")), Role: r.str("role")}
}

func removeAs(deps app.Deps, p identity.Principal, user uuid.UUID) error {
	return deps.Identity.RemoveMember(identity.WithPrincipal(context.Background(), p), p, user)
}

// F11 (cliente, qa/ESCENARIOS.md): spec §1, only the owner removes people;
// spec §7, a removed person loses access within 10 minutes.
func TestRemoveMember(t *testing.T) {
	deps := newDeps(t)
	c := client{t, serve(t, deps)}
	who := c.roles(deps, "molino-f11")
	owner := c.principal(who["owner"])
	viewer := c.principal(who["viewer"])
	rm := func(a account, user uuid.UUID) resp {
		return c.do("DELETE", "/v1/tenant/members/"+user.String(), a.access, nil)
	}

	// Nobody but the owner removes anyone.
	for _, role := range []string{"approver", "viewer", "accountant", "operator"} {
		if r := rm(who[role], viewer.UserID); r.status != 403 {
			t.Errorf("%s removing a member: %d %s", role, r.status, r.raw)
		}
	}

	// The owner removes the viewer: the refresh dies now, the access token
	// lives until it expires (at most 10 minutes).
	if r := rm(who["owner"], viewer.UserID); r.status != 204 {
		t.Fatalf("owner removes viewer: %d %s", r.status, r.raw)
	}
	// The old session is revoked, not only refused while the membership is
	// missing: adding the person back does not revive it.
	if r := c.do("POST", "/v1/tenant/members", who["owner"].access, map[string]any{"email": "viewer@molino-f11.test", "role": "viewer"}, idem()...); r.status != 201 {
		t.Fatalf("add back: %d %s", r.status, r.raw)
	}
	if r := c.do("POST", "/v1/auth/refresh", "", map[string]any{"refreshToken": who["viewer"].refresh, "tokenDelivery": "body"}); r.status != 401 {
		t.Fatalf("refresh after removal: %d %s", r.status, r.raw)
	}
	if r := rm(who["owner"], viewer.UserID); r.status != 204 {
		t.Fatalf("owner removes viewer again: %d %s", r.status, r.raw)
	}
	if r := c.do("GET", "/v1/me", who["viewer"].access, nil); r.status != 200 {
		t.Fatalf("access token right after removal: %d (documented to live up to 10 minutes)", r.status)
	}
	members := c.do("GET", "/v1/tenant/members", who["owner"].access, nil)
	for _, m := range members.body["items"].([]any) {
		if m.(map[string]any)["userId"] == viewer.UserID.String() {
			t.Fatalf("viewer still listed: %s", members.raw)
		}
	}
	var audited int
	if err := deps.DB.Pool.QueryRow(context.Background(),
		`select count(*) from audit_log where tenant_id = $1 and action = 'identity.member_removed' and user_id = $2 and subject = $3`,
		owner.TenantID, owner.UserID, "users:"+viewer.UserID.String()).Scan(&audited); err != nil || audited != 2 {
		t.Fatalf("audit rows: %d %v", audited, err)
	}

	// Not a member (any more, or never): 404.
	if r := rm(who["owner"], viewer.UserID); r.status != 404 {
		t.Fatalf("second removal: %d %s", r.status, r.raw)
	}
	// The last owner can not be removed, not even by themself.
	if r := rm(who["owner"], owner.UserID); r.status != 409 || r.str("code") != "last_owner" {
		t.Fatalf("last owner removed: %d %s", r.status, r.raw)
	}
	// The data is the client's: the owner may remove the operator too
	// (lead's decision, identity README).
	if r := rm(who["owner"], c.principal(who["operator"]).UserID); r.status != 204 {
		t.Fatalf("owner removes operator: %d %s", r.status, r.raw)
	}
	if r := c.do("GET", "/v1/me/tenant", who["owner"].access, nil); r.status != 200 {
		t.Fatalf("owner after refused self removal: %d", r.status)
	}
}

// Two owners removing each other at the same time: exactly one succeeds,
// the tenant keeps an owner.
func TestRemovingOwnersConcurrentlyKeepsOne(t *testing.T) {
	deps := newDeps(t)
	c := client{t, serve(t, deps)}
	a := c.registerAndLogin("molino-own2", "a@own2.test")
	b := c.member(a, a.tenant, "b-own2", "b@own2.test", "owner")
	pa, pb := c.principal(a), c.principal(b)
	for i := 0; i < 10; i++ {
		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Add(2)
		go func() { defer wg.Done(); errs[0] = removeAs(deps, pa, pb.UserID) }()
		go func() { defer wg.Done(); errs[1] = removeAs(deps, pb, pa.UserID) }()
		wg.Wait()
		var n int64
		if err := deps.DB.Pool.QueryRow(context.Background(),
			`select count(*) from memberships where tenant_id = $1 and role = 'owner'`, pa.TenantID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 || (errs[0] == nil) == (errs[1] == nil) {
			t.Fatalf("round %d: owners left %d, errors %v", i, n, errs)
		}
		// Put the removed owner back for the next round.
		gone, keep := pb, pa
		if errs[1] == nil {
			gone, keep = pa, pb
		}
		if _, err := deps.DB.Pool.Exec(context.Background(),
			`insert into memberships (user_id, tenant_id, role) values ($1, $2, 'owner')`, gone.UserID, keep.TenantID); err != nil {
			t.Fatal(err)
		}
	}
}
