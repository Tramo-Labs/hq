package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
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
	if r := c.do("POST", mpath, own.access, map[string]any{"action": "other"}, idem()...); r.status != 400 || r.str("code") != "note_required" {
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

// The quarantine detail of a balance that does not close carries amounts of
// the file: the tenant sees them, to fix the file, and they are its own
// data. They never reach the logs (architecture section 6: no amounts in
// logs) nor the audit log.
// F27: an account name with nothing visible, or with a bidi control that
// flips how the rest of a line reads, is 400 invalid_name, like a person's
// name; a valid one is stored trimmed. Escapes so a review can see them.
func TestAccountNameMustBeVisible(t *testing.T) {
	c := client{t: t, srv: server(t)}
	own := c.registerAndLogin("molino-nombres", "owner@nombres.test")
	for _, name := range []string{"\u200b\u200b", "\u3164", "Cuenta \u202elaicnes", "   "} {
		r := c.do("POST", "/v1/ledger/accounts", own.access, map[string]any{"bank": "bancor", "name": name}, idem()...)
		if r.status != 400 || r.str("code") != "invalid_name" {
			t.Errorf("name %q: %d %s", name, r.status, r.raw)
		}
	}
	r := c.do("POST", "/v1/ledger/accounts", own.access, map[string]any{"bank": "bancor", "name": "  Cuenta sueldos  "}, idem()...)
	if r.status != 201 || r.str("name") != "Cuenta sueldos" {
		t.Errorf("valid name: %d %s", r.status, r.raw)
	}
	if l := c.do("GET", "/v1/ledger/accounts", own.access, nil); len(l.body["items"].([]any)) != 1 {
		t.Errorf("refused names were stored: %s", l.raw)
	}
}

func TestQuarantineDetailIsShownNeverLogged(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	deps := newDeps(t)
	c := client{t: t, srv: serve(t, deps)}
	own := c.registerAndLogin("molino-logs", "owner@logs.test")
	acct := c.do("POST", "/v1/ledger/accounts", own.access, map[string]any{"bank": "bancor", "name": "Cuenta"}, idem()...)
	path := "/v1/ledger/accounts/" + acct.str("id") + "/statements"
	bad := c.upload(path, own.access, "bad.csv", ledgerFixture(t, "bancor_bad_balance.csv"), uuid.NewString())
	// The amounts the detail names (synthetic fixture).
	amounts := []string{"1164214.50", "1164215.50"}
	for _, a := range amounts {
		if !strings.Contains(bad.str("detail"), a) {
			t.Fatalf("the tenant does not see the amounts: %s", bad.raw)
		}
	}
	if r := c.do("GET", "/v1/ledger/files?status=quarantined", own.access, nil); !strings.Contains(r.raw, amounts[0]) {
		t.Errorf("files list without the detail: %s", r.raw)
	}
	// Every audit row, detail included, read from the table itself.
	var audit string
	ctx := context.Background()
	if err := deps.DB.System(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select coalesce(string_agg(action || ' ' || subject || ' ' || coalesce(detail::text, ''), E'\n'), '')
		                           from audit_log`).Scan(&audit)
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(audit, "ledger.file_quarantined") {
		t.Fatalf("audit without the quarantine: %s", audit)
	}
	for _, a := range amounts {
		if strings.Contains(logs.String(), a) {
			t.Errorf("amount %s in the logs", a)
		}
		if strings.Contains(audit, a) {
			t.Errorf("amount %s in the audit log", a)
		}
	}
	// The request loggers derive from slog.Default (platform httpx); prove
	// that the buffer is what they write to, or the check proves nothing.
	httpx.Logger(context.Background()).Info("probe")
	if !strings.Contains(logs.String(), `"probe"`) {
		t.Error("the request logger does not write to the captured buffer")
	}
}

// F12: the files the tenant uploaded (when, who, what happened) and the
// movements of a month, for every role of the tenant and no one else.
func TestFilesAndMovementsLists(t *testing.T) {
	c := client{t: t, srv: server(t)}
	own := c.registerAndLogin("molino-listas", "owner@listas.test")
	other := c.registerAndLogin("otro-listas", "owner@otro-listas.test")
	viewer := c.registerAndLogin("viewer-listas", "viewer@listas.test")
	if r := c.do("POST", "/v1/tenant/members", own.access, map[string]any{"email": viewer.email, "role": "viewer"}, idem()...); r.status != 201 {
		t.Fatalf("add viewer: %d %s", r.status, r.raw)
	}
	l := c.do("POST", "/v1/auth/login", "", map[string]any{"email": viewer.email, "password": viewer.password,
		"tenantId": own.tenant, "tokenDelivery": "body"})
	if l.status != 200 || l.str("role") != "viewer" {
		t.Fatalf("viewer login: %d %s", l.status, l.raw)
	}
	viewerToken := l.str("accessToken")

	acct := c.do("POST", "/v1/ledger/accounts", own.access, map[string]any{"bank": "bancor", "name": "Cuenta corriente"}, idem()...)
	path := "/v1/ledger/accounts/" + acct.str("id") + "/statements"
	if r := c.upload(path, own.access, "oct.csv", ledgerFixture(t, "bancor_ok.csv"), uuid.NewString()); r.str("status") != "accepted" {
		t.Fatalf("upload: %s", r.raw)
	}
	if r := c.upload(path, own.access, "bad.csv", ledgerFixture(t, "bancor_bad_balance.csv"), uuid.NewString()); r.str("status") != "quarantined" {
		t.Fatalf("bad upload: %s", r.raw)
	}

	ar, _ := time.LoadLocation("America/Argentina/Buenos_Aires")
	thisMonth := time.Now().In(ar).Format("2006-01")
	files := c.do("GET", "/v1/ledger/files?period="+thisMonth, viewerToken, nil)
	items, _ := files.body["items"].([]any)
	if files.status != 200 || len(items) != 2 || files.body["truncated"] != false {
		t.Fatalf("files as the viewer: %d %s", files.status, files.raw)
	}
	byStatus := map[string]map[string]any{}
	for _, it := range items {
		f := it.(map[string]any)
		byStatus[f["status"].(string)] = f
	}
	ok, bad := byStatus["accepted"], byStatus["quarantined"]
	if ok == nil || ok["filename"] != "oct.csv" || ok["movements"] != float64(10) || ok["from"] != "2026-10-01" ||
		ok["uploadedByEmail"] != own.email || ok["accountName"] != "Cuenta corriente" || ok["layoutId"] != "bancor/csv-v1" {
		t.Errorf("accepted file: %v", ok)
	}
	if bad == nil || bad["reason"] != "balance_mismatch" || bad["movements"] != nil || bad["uploadedByEmail"] != own.email {
		t.Errorf("quarantined file: %v", bad)
	}
	if r := c.do("GET", "/v1/ledger/files?status=quarantined", own.access, nil); len(r.body["items"].([]any)) != 1 {
		t.Errorf("status filter: %s", r.raw)
	}
	if r := c.do("GET", "/v1/ledger/files?period=2025-01", own.access, nil); len(r.body["items"].([]any)) != 0 {
		t.Errorf("another month: %s", r.raw)
	}
	if r := c.do("GET", "/v1/ledger/files", other.access, nil); len(r.body["items"].([]any)) != 0 {
		t.Errorf("other tenant sees files: %s", r.raw)
	}

	mv := c.do("GET", "/v1/ledger/movements?period=2026-10", viewerToken, nil)
	ms, _ := mv.body["items"].([]any)
	if mv.status != 200 || len(ms) != 10 || mv.body["truncated"] != false {
		t.Fatalf("movements as the viewer: %d %s", mv.status, mv.raw)
	}
	first := ms[0].(map[string]any)
	if first["date"] != "2026-10-01" || first["fileId"] != ok["id"] || first["line"] == nil || first["description"] == "" ||
		first["balance"] == nil || first["bank"] != "bancor" {
		t.Errorf("first movement: %v", first)
	}
	if r := c.do("GET", "/v1/ledger/movements?period=2026-10&accountId="+uuid.NewString(), own.access, nil); len(r.body["items"].([]any)) != 0 {
		t.Errorf("another account: %s", r.raw)
	}
	if r := c.do("GET", "/v1/ledger/movements?period=2026-11", own.access, nil); len(r.body["items"].([]any)) != 0 {
		t.Errorf("another month: %s", r.raw)
	}
	if r := c.do("GET", "/v1/ledger/movements", own.access, nil); r.status != 400 {
		t.Errorf("without period: %d", r.status)
	}
	if r := c.do("GET", "/v1/ledger/movements?period=2026-10", other.access, nil); len(r.body["items"].([]any)) != 0 {
		t.Errorf("other tenant sees movements: %s", r.raw)
	}
}
