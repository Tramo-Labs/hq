package app_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// upload sends a multipart file with an Idempotency-Key.
func (c client) upload(path, token, filename string, data []byte, key string) resp {
	c.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	// A fixed boundary makes a retry send the same bytes, as a network
	// retry of the same request does.
	_ = mw.SetBoundary("tramo-test-boundary-0123456789")
	fw, _ := mw.CreateFormFile("file", filename)
	fw.Write(data)
	mw.Close()
	req, _ := http.NewRequest("POST", c.srv.URL+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, header: res.Header, raw: string(raw)}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

func ledgerFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../ledger/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// From an account to the trays, through the HTTP API.
func TestDataPathsEndToEnd(t *testing.T) {
	c := client{t: t, srv: server(t)}
	own := c.registerAndLogin("molino-datos", "owner@datos.test")
	other := c.registerAndLogin("otro-datos", "owner@otro.test")

	acct := c.do("POST", "/v1/ledger/accounts", own.access, map[string]any{"bank": "bancor", "name": "Cuenta corriente"}, idem()...)
	if acct.status != 201 || acct.str("id") == "" {
		t.Fatalf("create account: %d %s", acct.status, acct.raw)
	}
	path := "/v1/ledger/accounts/" + acct.str("id") + "/statements"

	if r := c.upload(path, own.access, "oct.csv", ledgerFixture(t, "bancor_ok.csv"), ""); r.status != 400 {
		t.Errorf("upload without Idempotency-Key: %d", r.status)
	}
	key := uuid.NewString()
	first := c.upload(path, own.access, "oct.csv", ledgerFixture(t, "bancor_ok.csv"), key)
	if first.status != 201 || first.str("status") != "accepted" || first.body["new"] != float64(10) {
		t.Fatalf("upload: %d %s", first.status, first.raw)
	}
	if replay := c.upload(path, own.access, "oct.csv", ledgerFixture(t, "bancor_ok.csv"), key); replay.status != 201 || replay.raw != first.raw {
		t.Errorf("same key replay: %d %s", replay.status, replay.raw)
	}
	if dup := c.upload(path, own.access, "oct.csv", ledgerFixture(t, "bancor_ok.csv"), uuid.NewString()); dup.status != 200 || dup.str("status") != "duplicate" {
		t.Errorf("same file, new key: %d %s", dup.status, dup.raw)
	}
	bad := c.upload(path, own.access, "bad.csv", ledgerFixture(t, "bancor_bad_balance.csv"), uuid.NewString())
	if bad.status != 201 || bad.str("status") != "quarantined" || bad.str("reason") != "balance_mismatch" {
		t.Errorf("bad balance: %d %s", bad.status, bad.raw)
	}
	q := c.do("GET", "/v1/ledger/quarantine", own.access, nil)
	if items, _ := q.body["items"].([]any); q.status != 200 || len(items) != 1 {
		t.Errorf("quarantine: %d %s", q.status, q.raw)
	}

	// Another tenant: does not see the account, cannot upload to it.
	if r := c.upload(path, other.access, "oct.csv", ledgerFixture(t, "bancor_ok.csv"), uuid.NewString()); r.status != 404 {
		t.Errorf("other tenant upload: %d %s", r.status, r.raw)
	}
	if r := c.do("GET", "/v1/ledger/accounts", other.access, nil); len(r.body["items"].([]any)) != 0 {
		t.Errorf("other tenant sees accounts: %s", r.raw)
	}

	run := c.do("POST", "/v1/matching/runs", own.access, map[string]any{"period": "2026-10"}, idem()...)
	if run.status != 201 || run.body["exceptions"] != float64(10) || run.body["matches"] != float64(0) {
		t.Fatalf("run: %d %s", run.status, run.raw)
	}
	if r := c.do("POST", "/v1/matching/runs", own.access, map[string]any{"period": "octubre"}, idem()...); r.status != 400 {
		t.Errorf("bad period: %d", r.status)
	}
	exc := c.do("GET", "/v1/matching/pending?tray=exception", own.access, nil)
	if items, _ := exc.body["items"].([]any); exc.status != 200 || len(items) != 10 {
		t.Errorf("exceptions: %d %s", exc.status, exc.raw)
	}
	if r := c.do("GET", "/v1/matching/pending?tray=exception", other.access, nil); len(r.body["items"].([]any)) != 0 {
		t.Errorf("other tenant sees exceptions: %s", r.raw)
	}
	// The owner (client) explains one exception, then reverts it.
	items, _ := exc.body["items"].([]any)
	firstExc, _ := items[0].(map[string]any)
	mv, _ := firstExc["movementId"].(string)
	// Each exception says which movement it is.
	if firstExc["description"] == "" || firstExc["bank"] != "bancor" || firstExc["date"] == nil || firstExc["amount"] == nil ||
		firstExc["accountName"] != "Cuenta corriente" {
		t.Errorf("exception without its movement: %v", firstExc)
	}
	mpath := "/v1/matching/movements/" + mv + "/decision"
	if r := c.do("POST", mpath, own.access, map[string]any{"action": "other"}, idem()...); r.status != 400 {
		t.Errorf("other without a note: %d %s", r.status, r.raw)
	}
	if r := c.do("POST", mpath, own.access, map[string]any{"action": "other", "note": "gasto bancario"}, idem()...); r.status != 201 || r.body["revision"] != float64(1) {
		t.Errorf("other: %d %s", r.status, r.raw)
	}
	if r := c.do("GET", "/v1/matching/pending?tray=exception", own.access, nil); len(r.body["items"].([]any)) != 9 {
		t.Errorf("decided movement still in the client's tray: %s", r.raw)
	}
	if r := c.do("POST", mpath, own.access, map[string]any{"action": "other", "note": "otra"}, idem()...); r.status != 409 {
		t.Errorf("second decision: %d %s", r.status, r.raw)
	}
	if r := c.do("POST", mpath, own.access, map[string]any{"action": "revert"}, idem()...); r.status != 201 || r.body["revision"] != float64(2) {
		t.Errorf("revert: %d %s", r.status, r.raw)
	}
	if r := c.do("POST", "/v1/matching/movements/"+uuid.NewString()+"/decision", own.access,
		map[string]any{"action": "other", "note": "x"}, idem()...); r.status != 404 {
		t.Errorf("unknown movement: %d %s", r.status, r.raw)
	}
	if r := c.do("POST", mpath, other.access, map[string]any{"action": "other", "note": "x"}, idem()...); r.status != 404 {
		t.Errorf("other tenant decided on a's movement: %d %s", r.status, r.raw)
	}

	// The results panel: every number is measured or no data, says what it
	// counts, and its list opens.
	met := c.do("GET", "/v1/metrics?period=2026-10", own.access, nil)
	items, _ = met.body["items"].([]any)
	if met.status != 200 || len(items) == 0 {
		t.Fatalf("metrics: %d %s", met.status, met.raw)
	}
	for _, it := range items {
		m := it.(map[string]any)
		key, status, list := m["key"].(string), m["status"].(string), m["list"].(string)
		if (status == "no_data") != (m["value"] == nil) || m["source"] == "" {
			t.Errorf("metric %s: status %s value %v source %q", key, status, m["value"], m["source"])
		}
		if code, _, _ := c.get(list, own.access); code != 200 {
			t.Errorf("metric %s: list %s answers %d", key, list, code)
		}
	}
	// No match is automatic here (no items): measured 0%, not no data.
	for _, it := range items {
		if m := it.(map[string]any); m["key"] == "automation_rate" && (m["status"] != "measured" || m["value"] != float64(0)) {
			t.Errorf("automation_rate %v", m)
		}
	}
	if code, _, body := c.get("/v1/exports/matches?from=2026-10-01&to=2026-10-31", own.access); code != 200 ||
		!strings.HasPrefix(body, "match_id,rule,rule_version,tray") {
		t.Errorf("matches export: %d %q", code, body)
	}
	if r := c.do("GET", "/v1/metrics?period=2026-10", other.access, nil); r.status != 200 {
		t.Errorf("other tenant metrics: %d", r.status)
	} else {
		for _, it := range r.body["items"].([]any) {
			if m := it.(map[string]any); m["status"] != "no_data" {
				t.Errorf("other tenant sees a measured %v", m)
			}
		}
	}

	// F20: the client never sees Suggested (spec section 1); it sees results.
	for _, path := range []string{"/v1/matching/matches?tray=suggested", "/v1/matching/pending?tray=suggested"} {
		if r := c.do("GET", path, own.access, nil); r.status != 403 {
			t.Errorf("owner %s: %d %s", path, r.status, r.raw)
		}
	}
	if r := c.do("GET", "/v1/matching/matches?tray=auto", own.access, nil); r.status != 200 {
		t.Errorf("owner automatic results: %d %s", r.status, r.raw)
	}
	if r := c.do("POST", "/v1/matching/matches/00000000000000000000000000000000/decision", own.access,
		map[string]any{"action": "approve"}, idem()...); r.status != 404 {
		t.Errorf("decision on an unknown match: %d %s", r.status, r.raw)
	}

	if r := c.upload("/v1/documents/invoices", own.access, "x.pdf", []byte("not a document"), uuid.NewString()); r.status != 400 {
		t.Errorf("unreadable invoice: %d %s", r.status, r.raw)
	}
	if r := c.do("GET", "/v1/documents/invoices/"+uuid.NewString(), own.access, nil); r.status != 404 {
		t.Errorf("unknown invoice: %d", r.status)
	}
}
