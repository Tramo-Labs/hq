package identity

import (
	"strings"
	"testing"
)

// F31 (qa/ESCENARIOS.md): the product's emails are in Spanish with voseo,
// roles included, with the same names the portal shows.
func TestEmailsAreInSpanish(t *testing.T) {
	english := []string{"Your ", "You were", "Sign in", "If you did not", "expires in", "invited"}
	check := func(name, text string, want ...string) {
		t.Helper()
		for _, w := range english {
			if strings.Contains(text, w) {
				t.Errorf("%s still has English %q:\n%s", name, w, text)
			}
		}
		for _, w := range want {
			if !strings.Contains(text, w) {
				t.Errorf("%s lacks %q:\n%s", name, w, text)
			}
		}
	}
	for role, label := range map[string]string{RoleOwner: "dueño", RoleApprover: "aprobador", RoleViewer: "solo lectura", RoleAccountant: "contador", RoleOperator: "operador"} {
		m := inviteMessage("https://app.test", "a@b.test", "Molino Alfa", role, "TOKEN", false)
		check("invitation as "+role, m.Subject+"\n"+m.Body, "Molino Alfa", "con el rol "+label, "Elegí tu contraseña", "https://app.test/invitacion#token=TOKEN", "72 horas")
	}
	m := inviteMessage("https://app.test", "a@b.test", "Molino Alfa", RoleViewer, "TOKEN", true)
	check("invitation to an account", m.Subject+"\n"+m.Body, "Aceptá", "con tu cuenta")
	if strings.Contains(m.Body, "contraseña") {
		t.Errorf("an account is asked for a password:\n%s", m.Body)
	}
	l := magicLinkMessage("https://app.test", "a@b.test", "TOKEN")
	check("magic link", l.Subject+"\n"+l.Body, "Entrá", "https://app.test/ingresar#token=TOKEN", "15 minutos")
}
