package app_test

import (
	"testing"
)

// F27: a company name follows the person's name rule. Invisible only
// names, blank looking letters and the bidi override that flips how the
// rest of a line reads (in the portal and in the invitation email) are
// refused with 400 invalid_tenant_name; a normal name is trimmed.
func TestTenantNameFollowsTheNameRule(t *testing.T) {
	c := client{t, server(t)}
	register := func(i int, name string) resp {
		return c.do("POST", "/v1/auth/register", "", map[string]any{
			"email": "f27-" + string(rune('a'+i)) + "@f27.test", "password": "a long enough password",
			"tenantName": name, "tenantSlug": "molino-f27-" + string(rune('a'+i)),
		}, idem()...)
	}
	for i, name := range []string{"\u200b\u200b\u200b", "\u3164\u3164", "  ", "Tramo Soporte \u202e)ved( ofla oniloM", "......"} {
		if r := register(i, name); r.status != 400 || r.str("code") != "invalid_tenant_name" {
			t.Errorf("tenant name %q: %d %s", name, r.status, r.raw)
		}
	}
	r := register(9, "  Molino Alfa  ")
	if r.status != 201 {
		t.Fatalf("a normal name: %d %s", r.status, r.raw)
	}
	l := c.do("POST", "/v1/auth/login", "", map[string]any{"email": "f27-j@f27.test", "password": "a long enough password", "tokenDelivery": "body"})
	me := c.do("GET", "/v1/me", l.str("accessToken"), nil)
	ms, _ := me.body["memberships"].([]any)
	if len(ms) != 1 || ms[0].(map[string]any)["tenantName"] != "Molino Alfa" {
		t.Fatalf("stored tenant name: %s", me.raw)
	}
}
