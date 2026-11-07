package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/Tramo-Labs/hq/eng/core/internal/app"
	"github.com/Tramo-Labs/hq/eng/core/internal/inbox"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
)

// What the inbox hands to ingestion, end to end: under the tenant's scope
// and no user, a mailed file is routed or kept with no account, and the
// Archivos list and the upload's answer say where each file came from and
// went.
func TestMailedStatementsAreRouted(t *testing.T) {
	deps := newDeps(t)
	c := client{t: t, srv: serve(t, deps)}
	own := c.registerAndLogin("molino-casilla", "owner@casilla.test")
	job := db.WithScope(context.Background(), db.Scope{TenantID: uuid.MustParse(own.tenant)})
	ingest := app.LedgerInbox(deps.DB)
	mail := func(name string) string {
		t.Helper()
		r, err := ingest.IngestUnassigned(job, inbox.Unassigned{Filename: name, Data: ledgerFixture(t, name), Sender: "x@casilla.test"})
		if err != nil {
			t.Fatal(err)
		}
		return string(r.Status) + " " + r.Reason + " " + r.AccountID + " " + r.Routed
	}

	acct := c.do("POST", "/v1/ledger/accounts", own.access, map[string]any{"bank": "bancor", "name": "Cuenta corriente"}, idem()...)
	if acct.status != 201 {
		t.Fatalf("create account: %d %s", acct.status, acct.raw)
	}
	id := acct.str("id")
	// The account has no statement yet: its first one by mail is not routed.
	if got := mail("bancor_ok.csv"); got != "quarantined unrouted  " {
		t.Errorf("first statement by mail: %q", got)
	}
	// Unreadable: its reading reason, no account.
	if got := mail("galicia_changed_layout.csv"); got != "quarantined unknown_layout  " {
		t.Errorf("unreadable by mail: %q", got)
	}
	up := c.upload("/v1/ledger/accounts/"+id+"/statements", own.access, "oct.csv", ledgerFixture(t, "bancor_ok.csv"), uuid.NewString())
	if up.str("status") != "accepted" || up.str("accountId") != id || up.str("source") != "upload" || up.body["routed"] != nil {
		t.Fatalf("upload by hand: %d %s", up.status, up.raw)
	}
	if got := mail("bancor_nov.csv"); got != "accepted  "+id+" single_account" {
		t.Errorf("next statement by mail: %q", got)
	}

	files := c.do("GET", "/v1/ledger/files", own.access, nil)
	items, _ := files.body["items"].([]any)
	if files.status != 200 || len(items) != 4 {
		t.Fatalf("files: %d %s", files.status, files.raw)
	}
	rows := map[string]map[string]any{}
	for _, it := range items {
		f, _ := it.(map[string]any)
		rows[f["filename"].(string)+"|"+f["status"].(string)] = f
	}
	check := func(key string, want map[string]any) {
		t.Helper()
		f := rows[key]
		if f == nil {
			t.Errorf("%s: missing", key)
			return
		}
		for k, v := range want {
			if f[k] != v {
				t.Errorf("%s: %s = %v; want %v (%v)", key, k, f[k], v, f)
			}
		}
	}
	check("bancor_ok.csv|quarantined", map[string]any{"reason": "unrouted", "accountId": nil, "accountName": nil,
		"bank": "bancor", "source": "mail", "routed": nil, "uploadedBy": nil})
	check("galicia_changed_layout.csv|quarantined", map[string]any{"reason": "unknown_layout", "accountId": nil,
		"bank": nil, "source": "mail", "routed": nil})
	check("oct.csv|accepted", map[string]any{"accountId": id, "source": "upload", "routed": nil})
	check("bancor_nov.csv|accepted", map[string]any{"accountId": id, "bank": "bancor", "source": "mail",
		"routed": "single_account", "uploadedBy": nil})
}
