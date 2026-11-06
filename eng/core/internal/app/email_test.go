package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/ratelimit"
)

// F25: one tenant must never write another tenant's view of a person.
// Adding an existing account, with any case or spaces, finds it and never
// rewrites the stored email, not even on a 409. Emails are trimmed and
// lowercased on every input.
func TestAddingAMemberNeverRewritesTheStoredEmail(t *testing.T) {
	deps := newDeps(t)
	c := client{t, serve(t, deps)}
	alfa := c.registerAndLogin("molino-f25-a", "owner@f25a.test")
	approver := c.member(alfa, alfa.tenant, "molino-f25-x", "approver@f25a.test", "approver")
	beta := c.registerAndLogin("molino-f25-b", "owner@f25b.test")

	stored := func() string {
		var e string
		if err := deps.DB.Pool.QueryRow(context.Background(), `select email::text from users where lower(email::text) = 'approver@f25a.test'`).Scan(&e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	if r := c.do("POST", "/v1/tenant/members", beta.access, map[string]any{"email": "  APPROVER@F25A.TEST ", "role": "viewer"}, idem()...); r.status != 201 || r.str("email") != "approver@f25a.test" {
		t.Fatalf("beta adds the approver: %d %s", r.status, r.raw)
	}
	if r := c.do("POST", "/v1/tenant/members", alfa.access, map[string]any{"email": "Approver@F25a.Test", "role": "viewer"}, idem()...); r.status != 409 || r.str("code") != "already_member" {
		t.Fatalf("alfa re-adds with other case: %d %s", r.status, r.raw)
	}
	if e := stored(); e != "approver@f25a.test" {
		t.Fatalf("stored email rewritten to %q", e)
	}
	if me := c.do("GET", "/v1/me", approver.access, nil); me.str("email") != "approver@f25a.test" {
		t.Fatalf("approver's me: %s", me.raw)
	}

	// An account stored before emails were lowercased keeps its row as it
	// is: adding it from another tenant finds it and writes nothing.
	if _, err := deps.DB.Pool.Exec(context.Background(), `insert into users (email) values ('Legacy@F25.Test')`); err != nil {
		t.Fatal(err)
	}
	if r := c.do("POST", "/v1/tenant/members", beta.access, map[string]any{"email": "legacy@f25.test", "role": "viewer"}, idem()...); r.status != 201 {
		t.Fatalf("add the legacy account: %d %s", r.status, r.raw)
	}
	var legacy string
	var accounts int
	if err := deps.DB.Pool.QueryRow(context.Background(), `select min(email::text), count(*) from users where lower(email::text) = 'legacy@f25.test'`).Scan(&legacy, &accounts); err != nil || legacy != "Legacy@F25.Test" || accounts != 1 {
		t.Fatalf("legacy account: %q, %d rows (%v); want untouched and one", legacy, accounts, err)
	}

	// Registration and login lowercase too: one account whatever the case.
	r := c.do("POST", "/v1/auth/register", "", map[string]any{
		"email": " Mixta@F25.Test ", "password": "a long enough password", "tenantName": "Molino mixta", "tenantSlug": "molino-f25-m",
	}, idem()...)
	if r.status != 201 {
		t.Fatalf("register: %d %s", r.status, r.raw)
	}
	if l := c.do("POST", "/v1/auth/login", "", map[string]any{"email": "MIXTA@f25.test", "password": "a long enough password", "tokenDelivery": "body"}); l.status != 200 {
		t.Fatalf("login with other case: %d %s", l.status, l.raw)
	}
	var e string
	if err := deps.DB.Pool.QueryRow(context.Background(), `select email::text from users where lower(email::text) = 'mixta@f25.test'`).Scan(&e); err != nil || e != "mixta@f25.test" {
		t.Fatalf("registered email stored as %q (%v)", e, err)
	}
}

// F30: an email with an invisible or bidi character would create a second
// account that looks identical to the real one. Refused with 400, on every
// input that takes an email.
func TestEmailsRefuseInvisibleCharacters(t *testing.T) {
	deps := newDeps(t)
	deps.Identity.Limits.RegisterPerIP = ratelimit.Rule{Limit: 100, Window: time.Hour}
	c := client{t, serve(t, deps)}
	own := c.registerAndLogin("molino-f30", "owner@f30.test")
	// Spaces of any kind inside too (batch 6: NBSP gave 201).
	for _, e := range []string{"owner\u200b@f30.test", "owner@f30\u202e.test", "own\u2028er@f30.test", "owner\u3164@f30.test", "owner\u00ad@f30.test",
		"owner\u00a0@f30.test", "owner @f30.test", "ow\u2009ner@f30.test", "owner@f30\u3000.test", "owner\u202f@f30.test", "owner\u205f@f30.test", "owner\u1680@f30.test"} {
		if r := c.do("POST", "/v1/tenant/members", own.access, map[string]any{"email": e, "role": "viewer"}, idem()...); r.status != 400 {
			t.Errorf("add %q: %d %s", e, r.status, r.raw)
		}
		r := c.do("POST", "/v1/auth/register", "", map[string]any{
			"email": e, "password": "a long enough password", "tenantName": "Molino x", "tenantSlug": "molino-f30-x",
		}, idem()...)
		if r.status != 400 {
			t.Errorf("register %q: %d %s", e, r.status, r.raw)
		}
	}
}
