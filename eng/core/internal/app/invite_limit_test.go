package app_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/ratelimit"
)

// D-19: the portal accepts an invitation without a password first and asks
// for one only on 400 password_required (a new person). That answer is a
// step, not a rejection: it is not audited as one and does not count
// toward the per IP cap on accepts. Real rejections do both.
func TestPasswordRequiredIsAStepNotARejection(t *testing.T) {
	deps := newDeps(t)
	deps.Identity.Limits.InvitePerIP = ratelimit.Rule{Limit: 3, Window: time.Hour}
	c := client{t, serve(t, deps)}
	own := c.registerAndLogin("molino-lim", "owner@lim.test")
	if r := c.do("POST", "/v1/tenant/members", own.access, map[string]any{"email": "nueva@lim.test", "role": "viewer"}, idem()...); r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	for i := 0; i < 5; i++ { // more than the cap
		if r := c.join("nueva@lim.test", nil); r.status != 400 || r.str("code") != "password_required" {
			t.Fatalf("try %d without a password: %d %s", i, r.status, r.raw)
		}
	}
	if r := c.join("nueva@lim.test", map[string]any{"password": "a long enough password"}); r.status != 200 {
		t.Fatalf("accept with a password after the step: %d %s", r.status, r.raw)
	}
	var steps int
	if err := deps.DB.Pool.QueryRow(context.Background(),
		`select count(*) from audit_log where action = 'identity.invite_rejected' and detail->>'reason' = 'password required'`).Scan(&steps); err != nil {
		t.Fatal(err)
	}
	if steps != 0 {
		t.Fatalf("%d password_required steps audited as rejections", steps)
	}
}

func TestRealInviteRejectionsCountAndAreAudited(t *testing.T) {
	deps := newDeps(t)
	deps.Identity.Limits.InvitePerIP = ratelimit.Rule{Limit: 3, Window: time.Hour}
	c := client{t, serve(t, deps)}
	unknown := strings.Repeat("A", 43)
	for i := 0; i < 3; i++ {
		if r := accept(c, unknown, "a long enough password"); r.status != 401 || r.str("code") != "invalid_invite" {
			t.Fatalf("unknown token %d: %d %s", i, r.status, r.raw)
		}
	}
	if r := accept(c, unknown, "a long enough password"); r.status != 429 {
		t.Fatalf("past the cap: %d %s", r.status, r.raw)
	}
	var rejected int
	if err := deps.DB.Pool.QueryRow(context.Background(),
		`select count(*) from audit_log where action = 'identity.invite_rejected' and detail->>'reason' = 'unknown'`).Scan(&rejected); err != nil {
		t.Fatal(err)
	}
	if rejected != 3 {
		t.Fatalf("%d unknown token rejections audited, want 3", rejected)
	}
}

// burst sends n requests at once and counts the answers by status.
func burst(n int, do func() resp) map[int]int {
	var wg sync.WaitGroup
	var mu sync.Mutex
	got := map[int]int{}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := do()
			mu.Lock()
			got[r.status]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	return got
}

// qa-2's measure: 25 accepts at once with unknown tokens, cap 10. The cap
// is taken and counted in one step, so exactly 10 get through.
func TestInviteCapHoldsUnderABurst(t *testing.T) {
	deps := newDeps(t)
	deps.Identity.Limits.InvitePerIP = ratelimit.Rule{Limit: 10, Window: time.Hour}
	c := client{t, serve(t, deps)}
	got := burst(25, func() resp { return accept(c, strings.Repeat("A", 43), "a long enough password") })
	if got[401] != 10 || got[429] != 15 {
		t.Fatalf("25 at once against a cap of 10: %v", got)
	}
}

// Password steps at once consume nothing: after a burst of them the whole
// cap is still there for real attempts. While in flight each holds a unit
// (counting first is what keeps the cap under a burst), so steps beyond the
// cap at the same instant may get 429; none is a 401 and none stays counted.
func TestPasswordStepsAtOnceConsumeNothing(t *testing.T) {
	deps := newDeps(t)
	deps.Identity.Limits.InvitePerIP = ratelimit.Rule{Limit: 3, Window: time.Hour}
	c := client{t, serve(t, deps)}
	own := c.registerAndLogin("molino-limp", "owner@limp.test")
	if r := c.do("POST", "/v1/tenant/members", own.access, map[string]any{"email": "nueva@limp.test", "role": "viewer"}, idem()...); r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	tok := c.invitesTo("nueva@limp.test")[0]
	got := burst(10, func() resp {
		return c.do("POST", "/v1/auth/invite/accept", "", map[string]any{"token": tok, "tokenDelivery": "body"})
	})
	if got[400] < 3 || got[400]+got[429] != 10 {
		t.Fatalf("10 password steps at once against a cap of 3: %v", got)
	}
	got = burst(5, func() resp { return accept(c, strings.Repeat("B", 43), "a long enough password") })
	if got[401] != 3 || got[429] != 2 {
		t.Fatalf("after the steps the cap is not whole: %v", got)
	}
}

// Login had the same split: the failures per email and IP were checked
// before and counted after the password, so a burst of guesses could pass
// the cap. Exactly the cap get an answer about the password.
func TestLoginFailureCapHoldsUnderABurst(t *testing.T) {
	deps := newDeps(t)
	deps.Identity.Limits.LoginPerIP = ratelimit.Rule{Limit: 1000, Window: time.Hour}
	deps.Identity.Limits.FailPerEmailIP = ratelimit.Rule{Limit: 5, Window: time.Hour}
	c := client{t, serve(t, deps)}
	c.registerAndLogin("molino-limb", "owner@limb.test")
	got := burst(20, func() resp {
		return c.do("POST", "/v1/auth/login", "", map[string]any{"email": "owner@limb.test", "password": "a wrong guess, long", "tokenDelivery": "body"})
	})
	if got[401] != 5 || got[429] != 15 {
		t.Fatalf("20 wrong passwords at once against a cap of 5: %v", got)
	}
}

// qa-2's S4 probe: a right password on a tenant the person is not in (403
// not_a_member) is not a guess, so the failure taken before checking it is
// given back. With one failure left, three such logins leave it there: the
// next wrong password still gets its 401, and only the one after is 429.
func TestRightPasswordOnAnotherTenantIsNotAFailure(t *testing.T) {
	deps := newDeps(t)
	deps.Identity.Limits.LoginPerIP = ratelimit.Rule{Limit: 1000, Window: time.Hour}
	deps.Identity.Limits.FailPerEmailIP = ratelimit.Rule{Limit: 5, Window: time.Hour}
	c := client{t, serve(t, deps)}
	me := c.registerAndLogin("molino-s4", "owner@s4.test")
	other := c.registerAndLogin("otro-s4", "owner@otro-s4.test")
	login := func(password, tenant string) resp {
		body := map[string]any{"email": me.email, "password": password, "tokenDelivery": "body"}
		if tenant != "" {
			body["tenantId"] = tenant
		}
		return c.do("POST", "/v1/auth/login", "", body)
	}
	for i := 0; i < 4; i++ {
		if r := login("a wrong guess, long", ""); r.status != 401 {
			t.Fatalf("wrong %d: %d %s", i, r.status, r.raw)
		}
	}
	for i := 0; i < 3; i++ {
		if r := login(me.password, other.tenant); r.status != 403 || r.str("code") != "not_a_member" {
			t.Fatalf("right password, other tenant %d: %d %s", i, r.status, r.raw)
		}
	}
	if r := login("a wrong guess, long", ""); r.status != 401 {
		t.Fatalf("the last failure was spent by right passwords: %d %s", r.status, r.raw)
	}
	if r := login("a wrong guess, long", ""); r.status != 429 {
		t.Fatalf("past the cap: %d %s", r.status, r.raw)
	}
}

// Only a usable invitation reaches the password step. Without a password,
// an unknown, used, revoked or expired token, or one whose pending
// membership was removed, still gets its rejection, counted and audited,
// never the free 400 password_required.
func TestOnlyAUsableInvitationReachesThePasswordStep(t *testing.T) {
	deps := newDeps(t)
	deps.Identity.Limits.InvitePerIP = ratelimit.Rule{Limit: 100, Window: time.Hour}
	c := client{t, serve(t, deps)}
	own := c.registerAndLogin("molino-step", "owner@step.test")
	add := func(email string) string {
		t.Helper()
		r := c.do("POST", "/v1/tenant/members", own.access, map[string]any{"email": email, "role": "viewer"}, idem()...)
		if r.status != 201 {
			t.Fatalf("add %s: %d %s", email, r.status, r.raw)
		}
		return r.str("userId")
	}
	noPassword := func(token string) resp {
		return c.do("POST", "/v1/auth/invite/accept", "", map[string]any{"token": token, "tokenDelivery": "body"})
	}
	exec := func(sql, email string) {
		t.Helper()
		if _, err := deps.DB.Pool.Exec(context.Background(), sql, email); err != nil {
			t.Fatal(err)
		}
	}
	byUser := ` where user_id = (select id from users where email = $1)`

	add("usada@step.test")
	exec(`update invites set used_at = now()`+byUser, "usada@step.test")
	add("revocada@step.test")
	exec(`update invites set revoked_at = now()`+byUser, "revocada@step.test")
	add("vencida@step.test")
	exec(`update invites set expires_at = now() - interval '1 minute'`+byUser, "vencida@step.test")
	removed := add("sacada@step.test")
	if r := c.do("DELETE", "/v1/tenant/members/"+removed, own.access, nil); r.status != 204 {
		t.Fatalf("remove: %d %s", r.status, r.raw)
	}

	cases := map[string]struct {
		token  string
		status int
		code   string
	}{
		"unknown": {strings.Repeat("A", 43), 401, "invalid_invite"},
		"used":    {c.invitesTo("usada@step.test")[0], 401, "invalid_invite"},
		"revoked": {c.invitesTo("revocada@step.test")[0], 401, "invalid_invite"},
		"expired": {c.invitesTo("vencida@step.test")[0], 401, "invalid_invite"},
		"removed": {c.invitesTo("sacada@step.test")[0], 403, "not_a_member"},
	}
	for name, tc := range cases {
		if r := noPassword(tc.token); r.status != tc.status || r.str("code") != tc.code {
			t.Errorf("%s token without a password: %d %s, want %d %s", name, r.status, r.raw, tc.status, tc.code)
		}
	}
	var audited int
	if err := deps.DB.Pool.QueryRow(context.Background(),
		`select count(*) from audit_log where action = 'identity.invite_rejected'`).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != len(cases) {
		t.Fatalf("%d rejections audited, want %d", audited, len(cases))
	}
}
