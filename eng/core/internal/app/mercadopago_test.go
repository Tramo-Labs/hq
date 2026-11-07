package app_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/Tramo-Labs/hq/eng/core/internal/mercadopago"
	"github.com/Tramo-Labs/hq/eng/core/internal/mercadopago/mpfake"
)

func TestMercadoPagoOverHTTP(t *testing.T) {
	fake := mpfake.New()
	t.Cleanup(fake.Close)
	deps := newDeps(t)
	deps.MercadoPago = mercadopago.Config{ClientID: fake.ClientID, ClientSecret: fake.ClientSecret,
		RedirectURI: fake.RedirectURI, APIURL: fake.URL}
	deps.MercadoPagoHTTP = fake.Client()
	c := client{t, serve(t, deps)}
	who := c.roles(deps, "molino-mp")
	own := who["owner"].access

	if r := c.do("GET", "/v1/integrations/mercadopago", own, nil); r.status != 200 || r.str("status") != "disconnected" || r.body["configured"] != true {
		t.Fatalf("before connecting: %d %s", r.status, r.raw)
	}
	for _, role := range []string{"approver", "viewer", "accountant", "operator"} {
		if r := c.do("POST", "/v1/integrations/mercadopago/authorize", who[role].access, nil, idem()...); r.status != 403 {
			t.Errorf("%s authorizes: %d", role, r.status)
		}
	}
	if r := c.do("POST", "/v1/integrations/mercadopago/authorize", own, nil); r.status != 400 {
		t.Fatalf("authorize without Idempotency-Key: %d", r.status)
	}
	a := c.do("POST", "/v1/integrations/mercadopago/authorize", own, nil, idem()...)
	if a.status != 201 || !strings.HasPrefix(a.str("authorizeUrl"), mercadopago.DefaultAuthURL+"?") {
		t.Fatalf("authorize: %d %s", a.status, a.raw)
	}
	u, _ := url.Parse(a.str("authorizeUrl"))
	q := u.Query()
	code, ok := fake.Grant(q.Get("client_id"), q.Get("redirect_uri"), q.Get("code_challenge"), q.Get("code_challenge_method"), 77)
	if !ok {
		t.Fatalf("Mercado Pago would refuse: %s", u)
	}
	for _, c2 := range []struct{ code, state, want string }{
		{"TG-x", "unknown-state", "state_invalid"},
		{"TG-wrong", q.Get("state"), "mp_code_rejected"},
	} {
		r := c.do("POST", "/v1/integrations/mercadopago/connect", own, map[string]any{"code": c2.code, "state": c2.state}, idem()...)
		if r.str("code") != c2.want {
			t.Errorf("connect %s: %d %s, want %s", c2.code, r.status, r.raw, c2.want)
		}
	}
	r := c.do("POST", "/v1/integrations/mercadopago/connect", own, map[string]any{"code": code, "state": q.Get("state")}, idem()...)
	if r.status != 201 || r.str("status") != "connected" || r.str("mpUserId") != "77" {
		t.Fatalf("connect: %d %s", r.status, r.raw)
	}
	if strings.Contains(r.raw, "APP_USR-") || strings.Contains(r.raw, "TG-") {
		t.Fatalf("a token in the answer: %s", r.raw)
	}
	if r := c.do("POST", "/v1/integrations/mercadopago/connect", own, map[string]any{"code": code, "state": q.Get("state")}, idem()...); r.status != 409 || r.str("code") != "state_used" {
		t.Fatalf("same state again: %d %s", r.status, r.raw)
	}
	if r := c.do("GET", "/v1/integrations/mercadopago", who["accountant"].access, nil); r.str("status") != "connected" {
		t.Fatalf("accountant sees: %s", r.raw)
	}
	if r := c.do("GET", "/v1/integrations/mercadopago", who["viewer"].access, nil); r.status != 403 {
		t.Fatalf("viewer sees: %d", r.status)
	}
	if r := c.do("DELETE", "/v1/integrations/mercadopago", who["accountant"].access, nil); r.status != 403 {
		t.Fatalf("accountant disconnects: %d", r.status)
	}
	if r := c.do("DELETE", "/v1/integrations/mercadopago", own, nil); r.status != 200 || r.str("status") != "disconnected" || r.body["revokeInMercadoPago"] != true {
		t.Fatalf("disconnect: %d %s", r.status, r.raw)
	}
}

// Without an app, the owner gets 501 not_configured and others still 403.
func TestMercadoPagoNotConfigured(t *testing.T) {
	deps := newDeps(t)
	c := client{t, serve(t, deps)}
	who := c.roles(deps, "molino-mp2")
	if r := c.do("POST", "/v1/integrations/mercadopago/authorize", who["owner"].access, nil, idem()...); r.status != 501 || r.str("code") != "not_configured" {
		t.Fatalf("owner: %d %s", r.status, r.raw)
	}
	if r := c.do("POST", "/v1/integrations/mercadopago/authorize", who["viewer"].access, nil, idem()...); r.status != 403 {
		t.Fatalf("viewer: %d", r.status)
	}
	if r := c.do("GET", "/v1/integrations/mercadopago", who["owner"].access, nil); r.body["configured"] != false {
		t.Fatalf("configured: %s", r.raw)
	}
}
