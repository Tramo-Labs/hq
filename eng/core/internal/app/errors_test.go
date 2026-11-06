package app_test

import (
	"strings"
	"testing"
)

// F17 (cliente, qa/ESCENARIOS.md): a 400 says which parameter or field is
// wrong in one short, stable line. Never the schema, never the value sent.
func TestBadRequestDetailIsShortAndStable(t *testing.T) {
	c := client{t, server(t)}
	a := c.registerAndLogin("molino-f17", "owner@f17.test")
	reg := func(extra map[string]any) map[string]any {
		b := map[string]any{"email": "x@f17.test", "password": "a long enough password", "tenantName": "Molino", "tenantSlug": "molino-x"}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	cases := []struct {
		name, method, path, token string
		body                      map[string]any
		detail, secret            string
	}{
		{"query out of pattern", "GET", "/v1/metrics?period=2026-13", a.access, nil,
			`invalid parameter "period"`, "2026-13"},
		{"missing query", "GET", "/v1/metrics", a.access, nil,
			`invalid parameter "period"`, ""},
		{"path not a uuid", "GET", "/v1/documents/invoices/not-a-uuid", a.access, nil,
			`invalid parameter "invoiceId"`, "not-a-uuid"},
		{"enum", "GET", "/v1/exports/secrets", a.access, nil,
			`invalid parameter "kind"`, "secrets"},
		{"body field pattern", "POST", "/v1/auth/register", "", reg(map[string]any{"tenantSlug": "Zz"}),
			"invalid request body: /tenantSlug", "Zz"},
		{"body unknown field", "POST", "/v1/auth/register", "", reg(map[string]any{"claveFiscal": "hunter2"}),
			"invalid request body: unknown property", "hunter2"},
		{"body missing field", "POST", "/v1/auth/register", "", map[string]any{"email": "x@f17.test"},
			"invalid request body: /password", ""},
	}
	for _, tc := range cases {
		r := c.do(tc.method, tc.path, tc.token, tc.body, idem()...)
		d := r.str("detail")
		if r.status != 400 || r.str("code") != "bad_request" {
			t.Errorf("%s: %d %s", tc.name, r.status, r.raw)
			continue
		}
		if d != tc.detail {
			t.Errorf("%s: detail %q, want %q", tc.name, d, tc.detail)
		}
		if strings.ContainsAny(d, "\n{") || len(d) > 120 ||
			strings.Contains(d, "Schema") || strings.Contains(d, "Value") ||
			(tc.secret != "" && strings.Contains(r.raw, tc.secret)) {
			t.Errorf("%s: leaks or too long: %s", tc.name, r.raw)
		}
	}
}

// F18 (cliente, qa/ESCENARIOS.md): the right password on a tenant the user
// does not belong to says so, instead of "invalid email or password". A
// wrong password still says nothing about memberships.
func TestLoginIntoAForeignTenantSaysNotAMember(t *testing.T) {
	c := client{t, server(t)}
	molino := c.registerAndLogin("molino-f18", "owner@f18.test")
	rival := c.registerAndLogin("rival-f18", "rival@f18.test")

	r := c.do("POST", "/v1/auth/login", "", map[string]any{"email": rival.email, "password": rival.password,
		"tenantId": molino.tenant, "tokenDelivery": "body"})
	if r.status != 403 || r.str("code") != "not_a_member" || r.str("detail") != "not a member of that tenant" || r.str("accessToken") != "" {
		t.Fatalf("right password, foreign tenant: %d %s", r.status, r.raw)
	}
	// Wrong password with the same tenant: the generic answer, so the
	// membership answer is only reachable with the right password.
	w := c.do("POST", "/v1/auth/login", "", map[string]any{"email": rival.email, "password": "not the password",
		"tenantId": molino.tenant, "tokenDelivery": "body"})
	if w.status != 401 || w.str("code") != "unauthorized" {
		t.Fatalf("wrong password, foreign tenant: %d %s", w.status, w.raw)
	}
	// Own tenant still works.
	if ok := c.do("POST", "/v1/auth/login", "", map[string]any{"email": rival.email, "password": rival.password,
		"tenantId": rival.tenant, "tokenDelivery": "body"}); ok.status != 200 {
		t.Fatalf("own tenant: %d %s", ok.status, ok.raw)
	}
}
