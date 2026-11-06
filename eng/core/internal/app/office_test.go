package app_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/ratelimit"
)

// D-20 (a): six people of one office, behind one address, join one after
// another (preview, then accept) and sign in, and all six get in, with
// per IP caps of 3: a valid invitation's preview, a successful accept and
// a right login spend nothing.
func TestAnOfficeJoinsFromOneAddress(t *testing.T) {
	deps := newDeps(t)
	deps.Identity.Limits.InvitePerIP = ratelimit.Rule{Limit: 3, Window: time.Hour}
	deps.Identity.Limits.LoginPerIP = ratelimit.Rule{Limit: 3, Window: time.Hour}
	c := client{t, serve(t, deps)}
	own := c.registerAndLogin("molino-ofi", "owner@ofi.test")
	for i := 0; i < 6; i++ {
		email := fmt.Sprintf("persona%d@ofi.test", i)
		if r := c.do("POST", "/v1/tenant/members", own.access, map[string]any{"email": email, "role": "viewer"}, idem()...); r.status != 201 {
			t.Fatalf("add %s: %d %s", email, r.status, r.raw)
		}
		tok := c.invitesTo(email)[0]
		if r := preview(c, tok); r.status != 200 {
			t.Fatalf("preview %s: %d %s", email, r.status, r.raw)
		}
		if r := c.join(email, nil); r.status != 400 { // the password step
			t.Fatalf("step %s: %d %s", email, r.status, r.raw)
		}
		if r := c.join(email, map[string]any{"password": "a long enough password"}); r.status != 200 {
			t.Fatalf("%s did not get in: %d %s", email, r.status, r.raw)
		}
		if r := c.do("POST", "/v1/auth/login", "", map[string]any{"email": email, "password": "a long enough password", "tokenDelivery": "body"}); r.status != 200 {
			t.Fatalf("%s did not sign in: %d %s", email, r.status, r.raw)
		}
	}
}

// D-20 (c): the per IP login limit counts wrong passwords only. Right
// ones, sequential and many more than the cap, all get in; N wrong ones
// get 401 and the next is 429.
func TestLoginPerIPCountsWrongPasswordsOnly(t *testing.T) {
	deps := newDeps(t)
	deps.Identity.Limits.LoginPerIP = ratelimit.Rule{Limit: 3, Window: time.Hour}
	c := client{t, serve(t, deps)}
	var accounts []account
	for i := 0; i < 3; i++ {
		accounts = append(accounts, c.registerAndLogin(fmt.Sprintf("molino-lip%d", i), fmt.Sprintf("p%d@lip.test", i)))
	}
	login := func(email, password string) resp {
		return c.do("POST", "/v1/auth/login", "", map[string]any{"email": email, "password": password, "tokenDelivery": "body"})
	}
	for round := 0; round < 3; round++ { // 9 right logins against a cap of 3
		for _, a := range accounts {
			if r := login(a.email, a.password); r.status != 200 {
				t.Fatalf("right login %s round %d: %d %s", a.email, round, r.status, r.raw)
			}
		}
	}
	for i, a := range accounts { // one wrong each: the pair cap is not reached
		if r := login(a.email, "a wrong guess, long"); r.status != 401 {
			t.Fatalf("wrong %d: %d %s", i, r.status, r.raw)
		}
	}
	if r := login(accounts[0].email, "a wrong guess, long"); r.status != 429 {
		t.Fatalf("past the per IP cap: %d %s", r.status, r.raw)
	}
}

// A person locked out, by the pair's cap or by magic link mode, who keeps
// retrying is refused without the password being checked, and that spends
// nothing of the per IP limit: someone else behind the same address still
// signs in. The pair's or the email's limit holds the locked out one.
func TestALockedOutPersonRetryingSpendsNothingOfTheIP(t *testing.T) {
	login := func(c client, email, password string) resp {
		return c.do("POST", "/v1/auth/login", "", map[string]any{"email": email, "password": password, "tokenDelivery": "body"})
	}
	t.Run("pair cap", func(t *testing.T) {
		c := client{t, server(t)} // default limits: 30 per IP, 5 per pair
		locked := c.registerAndLogin("molino-lk1", "bloqueada@lk1.test")
		other := c.registerAndLogin("molino-lk1b", "otra@lk1.test")
		for i := 0; i < 5; i++ {
			login(c, locked.email, "a wrong guess, long")
		}
		for i := 0; i < 40; i++ {
			if r := login(c, locked.email, "a wrong guess, long"); r.status != 429 {
				t.Fatalf("retry %d: %d %s", i, r.status, r.raw)
			}
		}
		if r := login(c, other.email, other.password); r.status != 200 {
			t.Fatalf("someone else from the same address: %d %s", r.status, r.raw)
		}
	})
	t.Run("magic link mode", func(t *testing.T) {
		deps := newDeps(t)
		deps.Identity.Limits.AttackFails = ratelimit.Rule{Limit: 3, Window: time.Hour}
		deps.Identity.Limits.AttackIPs = 1
		c := client{t, serve(t, deps)}
		locked := c.registerAndLogin("molino-lk2", "bloqueada@lk2.test")
		other := c.registerAndLogin("molino-lk2b", "otra@lk2.test")
		for i := 0; i < 3; i++ {
			login(c, locked.email, "a wrong guess, long")
		}
		for i := 0; i < 40; i++ {
			if r := login(c, locked.email, locked.password); r.status != 401 || r.str("code") != "magic_link_required" {
				t.Fatalf("retry %d in the mode: %d %s", i, r.status, r.raw)
			}
		}
		if r := login(c, other.email, other.password); r.status != 200 {
			t.Fatalf("someone else from the same address: %d %s", r.status, r.raw)
		}
	})
}

// What D-20 must not open: over the cap, a guess is not even checked, so
// the right password gets 429 too, on the pair and on the per IP limit.
func TestOverTheCapTheRightPasswordIsRefusedToo(t *testing.T) {
	deps := newDeps(t)
	deps.Identity.Limits.LoginPerIP = ratelimit.Rule{Limit: 1000, Window: time.Hour}
	deps.Identity.Limits.FailPerEmailIP = ratelimit.Rule{Limit: 3, Window: time.Hour}
	c := client{t, serve(t, deps)}
	a := c.registerAndLogin("molino-otc", "owner@otc.test")
	login := func(password string) resp {
		return c.do("POST", "/v1/auth/login", "", map[string]any{"email": a.email, "password": password, "tokenDelivery": "body"})
	}
	for i := 0; i < 3; i++ {
		if r := login("a wrong guess, long"); r.status != 401 {
			t.Fatalf("wrong %d: %d %s", i, r.status, r.raw)
		}
	}
	if r := login(a.password); r.status != 429 {
		t.Fatalf("right password over the pair cap: %d %s", r.status, r.raw)
	}

	deps2 := newDeps(t)
	deps2.Identity.Limits.LoginPerIP = ratelimit.Rule{Limit: 3, Window: time.Hour}
	c2 := client{t, serve(t, deps2)}
	b := c2.registerAndLogin("molino-otc2", "owner@otc2.test")
	for i := 0; i < 3; i++ { // wrong passwords of unknown emails: the IP's cap
		if r := c2.do("POST", "/v1/auth/login", "", map[string]any{"email": fmt.Sprintf("x%d@otc2.test", i), "password": "a wrong guess, long", "tokenDelivery": "body"}); r.status != 401 {
			t.Fatalf("wrong %d: %d %s", i, r.status, r.raw)
		}
	}
	if r := c2.do("POST", "/v1/auth/login", "", map[string]any{"email": b.email, "password": b.password, "tokenDelivery": "body"}); r.status != 429 {
		t.Fatalf("right password over the per IP cap: %d %s", r.status, r.raw)
	}
}
