package app_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/Tramo-Labs/hq/eng/core/internal/matching"
)

// A statement that brings new movements dated in a closed fortnight does
// not enter: it is quarantined as period_closed and names the period, in
// the upload's answer and in the "Archivos" list. A statement of an open
// period still enters.
func TestStatementInAClosedPeriod(t *testing.T) {
	deps := newDeps(t)
	deps.Now = closeClock
	deps.CloseNumbers = matching.CloseNumbers{}
	c := client{t, serve(t, deps)}
	who := c.roles(deps, "molino-cerrado")
	own, op := who["owner"].access, who["operator"].access

	acct := c.do("POST", "/v1/ledger/accounts", own, map[string]any{"bank": "bancor", "name": "Cuenta corriente"}, idem()...)
	if acct.status != 201 {
		t.Fatalf("create account: %d %s", acct.status, acct.raw)
	}
	path := "/v1/ledger/accounts/" + acct.str("id") + "/statements"
	// October 1 to 10.
	if r := c.upload(path, own, "oct.csv", ledgerFixture(t, "bancor_ok.csv"), uuid.NewString()); r.str("status") != "accepted" {
		t.Fatalf("upload: %d %s", r.status, r.raw)
	}
	if r := c.do("POST", "/v1/matching/runs", op, map[string]any{"period": "2026-10"}, idem()...); r.status != 201 {
		t.Fatalf("run: %d %s", r.status, r.raw)
	}
	if r := c.do("POST", "/v1/closes", op, map[string]any{"periodStart": "2026-10-01"}, idem()...); r.status != 201 {
		t.Fatalf("close Oct 1-15: %d %s", r.status, r.raw)
	}

	// The next statement chains on Oct 10's balance and brings Oct 12.
	late := []byte("Fecha,Concepto,Comprobante,Débito,Crédito,Saldo\n" +
		`12/10/2026,"TRANSFERENCIA RECIBIDA CLIENTE TRES",000110,,"1.000,00","1.665.434,73"` + "\n")
	r := c.upload(path, own, "oct-late.csv", late, uuid.NewString())
	if r.status != 201 || r.str("status") != "quarantined" || r.str("reason") != "period_closed" || r.body["new"] != float64(0) {
		t.Fatalf("statement into a closed period: %d %s", r.status, r.raw)
	}
	cp, _ := r.body["closedPeriod"].(map[string]any)
	if cp["from"] != "2026-10-01" || cp["to"] != "2026-10-15" {
		t.Errorf("closedPeriod in the answer: %v", r.body["closedPeriod"])
	}

	files := c.do("GET", "/v1/ledger/files?status=quarantined", own, nil)
	items, _ := files.body["items"].([]any)
	if files.status != 200 || len(items) != 1 {
		t.Fatalf("quarantined files: %d %s", files.status, files.raw)
	}
	row, _ := items[0].(map[string]any)
	cp, _ = row["closedPeriod"].(map[string]any)
	if row["reason"] != "period_closed" || cp["from"] != "2026-10-01" || cp["to"] != "2026-10-15" {
		t.Errorf("the Archivos row does not name the closed period: %v", row)
	}

	// November is open: its statement enters, and only the ten movements of
	// October plus November's are stored.
	if r := c.upload(path, own, "nov.csv", ledgerFixture(t, "bancor_nov.csv"), uuid.NewString()); r.str("status") != "accepted" {
		t.Errorf("statement of an open period: %d %s", r.status, r.raw)
	}
	accepted := c.do("GET", "/v1/ledger/files?status=accepted", own, nil)
	acceptedItems, _ := accepted.body["items"].([]any)
	if len(acceptedItems) != 2 {
		t.Errorf("accepted files: %s", accepted.raw)
	}
	for _, it := range acceptedItems {
		if f, _ := it.(map[string]any); f["closedPeriod"] != nil {
			t.Errorf("an accepted file names a closed period: %v", f)
		}
		if f, _ := it.(map[string]any); f["filename"] == "oct-late.csv" {
			t.Errorf("the late statement entered: %v", f)
		}
	}
}
