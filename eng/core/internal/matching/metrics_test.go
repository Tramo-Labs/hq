package matching

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/documents"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/ledger"
)

var metricsNow = time.Date(2026, 11, 6, 15, 0, 0, 0, time.UTC)

func byKey(t *testing.T, ms []Metric) map[string]Metric {
	t.Helper()
	out := map[string]Metric{}
	for _, m := range ms {
		if _, dup := out[m.Key]; dup {
			t.Fatalf("metric %s twice", m.Key)
		}
		if m.Source == "" || m.List == "" || m.Label == "" {
			t.Errorf("metric %s without label, source or list: %+v", m.Key, m)
		}
		out[m.Key] = m
	}
	return out
}

func value(t *testing.T, m Metric) float64 {
	t.Helper()
	if m.Value == nil {
		t.Fatalf("%s: no data (%s); want a measured value", m.Key, m.Source)
	}
	return *m.Value
}

func noData(t *testing.T, m Metric) {
	t.Helper()
	if m.Value != nil {
		t.Errorf("%s = %v; want no data, not a number (%s)", m.Key, *m.Value, m.Source)
	}
}

func mustMetrics(t *testing.T, e pgEnv, period string) []Metric {
	t.Helper()
	ms, err := e.store.Metrics(e.ctx, period, metricsNow)
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

// Before the month runs there is nothing to measure: no data everywhere,
// never zero. After the run, the numbers of the synthetic Bancor statement.
func TestPGMetricsOfAMonth(t *testing.T) {
	e := newPGEnv(t)
	for _, m := range mustMetrics(t, e, "2026-10") {
		noData(t, m)
	}

	r, _ := e.run(t)
	k := byKey(t, mustMetrics(t, e, "2026-10"))
	// 10 movements; the cheque (reference) and the first transfer (amount
	// and date) are automatic.
	if v := value(t, k["automation_rate"]); v != 20 {
		t.Errorf("automation_rate %v; want 20", v)
	}
	if !strings.Contains(k["automation_rate"].Source, "(2)") || !strings.Contains(k["automation_rate"].Source, "(10)") {
		t.Errorf("source does not say what it counts: %q", k["automation_rate"].Source)
	}
	if k["automation_rate"].List != "/v1/matching/matches?tray=auto&period=2026-10" {
		t.Errorf("list %q", k["automation_rate"].List)
	}
	// Nothing decided yet: no data, not 0%.
	noData(t, k["suggested_acceptance_rate"])
	// Seven movements with no candidate: commission, VAT, debits tax,
	// supplier payment, two twin transfers, SIRCREB. Oldest from Oct 2.
	if v := value(t, k["open_exception_movements"]); v != 7 {
		t.Errorf("open_exception_movements %v; want 7", v)
	}
	if v := value(t, k["open_exception_matches"]); v != 0 {
		t.Errorf("open_exception_matches %v; want 0 (measured)", v)
	}
	if v := value(t, k["oldest_open_exception_days"]); v != 35 {
		t.Errorf("oldest_open_exception_days %v; want 35 (Oct 2 to Nov 6)", v)
	}
	for _, reason := range pendingReasons {
		if v := value(t, k["pending_"+string(reason)]); v != 0 {
			t.Errorf("pending_%s %v; want 0", reason, v)
		}
	}
	// No invoice ever uploaded: no data.
	noData(t, k["invoices_processed"])
	noData(t, k["invoices_held"])

	// The operator approves the suggestion; the client says one movement is
	// something else.
	if _, err := e.decideAs(t, identity.RoleOperator, suggested(r), Approve, ""); err != nil {
		t.Fatal(err)
	}
	fee := e.movementsByDescription(t, "COMISION MANTENIMIENTO CUENTA")[0]
	if _, err := e.decideMovementAs(t, identity.RoleApprover, fee, MovementOther, "gasto bancario"); err != nil {
		t.Fatal(err)
	}
	e.setMembership(t, e.a, identity.RoleOperator) // the Suggested metrics are the operator's
	k = byKey(t, mustMetrics(t, e, "2026-10"))
	if v := value(t, k["suggested_acceptance_rate"]); v != 100 {
		t.Errorf("suggested_acceptance_rate %v; want 100", v)
	}
	if v := value(t, k["open_exception_movements"]); v != 6 {
		t.Errorf("open_exception_movements %v; want 6 after the client's decision", v)
	}
	if v := value(t, k["pending_client_other"]); v != 1 {
		t.Errorf("pending_client_other %v; want 1", v)
	}
	// A reverted approval does not count as accepted.
	if _, err := e.decideAs(t, identity.RoleOperator, suggested(r), Revert, ""); err != nil {
		t.Fatal(err)
	}
	noData(t, byKey(t, mustMetrics(t, e, "2026-10"))["suggested_acceptance_rate"])
}

// A month without movements is no data, not 0%.
func TestPGMetricsEmptyMonth(t *testing.T) {
	e := newPGEnv(t)
	e.run(t)
	k := byKey(t, mustMetrics(t, e, "2026-09"))
	noData(t, k["automation_rate"])
	if !strings.Contains(k["automation_rate"].Source, "Sin movimientos") {
		t.Errorf("source %q", k["automation_rate"].Source)
	}
	if _, err := e.store.Metrics(e.ctx, "octubre", metricsNow); err == nil {
		t.Error("bad period accepted")
	}
}

func TestPGMetricsInvoices(t *testing.T) {
	e := newPGEnv(t)
	ds := &documents.PGStore{DB: e.d}
	inv := &documents.Invoice{Fields: map[documents.Name]documents.Field{}}
	if _, err := ds.Save(e.ctx, []byte("synthetic document"), inv); err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(time.FixedZone("ART", -3*3600))
	k := byKey(t, mustMetrics(t, e, now.Format("2006-01")))
	if v := value(t, k["invoices_processed"]); v != 1 {
		t.Errorf("invoices_processed %v", v)
	}
	if v := value(t, k["invoices_held"]); v != 1 {
		t.Errorf("invoices_held %v (an invoice with no fields needs review)", v)
	}
	// Another month of the same tenant: measured zero, invoices exist.
	other := now.AddDate(0, -2, 0).Format("2006-01")
	if v := value(t, byKey(t, mustMetrics(t, e, other))["invoices_processed"]); v != 0 {
		t.Errorf("invoices in %s: %v; want 0", other, v)
	}
}

// Another tenant's numbers come only from its own data: nothing here.
func TestPGMetricsIsolation(t *testing.T) {
	e := newPGEnv(t)
	e.run(t)
	ms, err := e.store.Metrics(e.ctxB, "2026-10", metricsNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		noData(t, m)
	}
	rows, err := e.store.MatchRows(e.ctxB, day(1), day(31))
	if err != nil || len(rows) != 1 {
		t.Errorf("tenant b export: %d rows (%v); want only the header", len(rows), err)
	}
}

// The matches export: one row per match of the period, with rule and
// version, tray and the decision in effect.
func TestPGMatchRows(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	if _, err := e.decideAs(t, identity.RoleOperator, suggested(r), Approve, "ok"); err != nil {
		t.Fatal(err)
	}
	rows, err := e.store.MatchRows(e.ctx, day(1), day(31))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("%d rows; want header and 3 matches", len(rows))
	}
	head := rows[0]
	col := func(name string) int {
		for i, h := range head {
			if h == name {
				return i
			}
		}
		t.Fatalf("no column %s", name)
		return -1
	}
	decided := 0
	for _, row := range rows[1:] {
		if row[col("rule")] == "" || row[col("rule_version")] != "1" {
			t.Errorf("row without rule or version: %v", row)
		}
		if row[col("decision")] == "approve" {
			decided++
			if row[col("decided_role")] != identity.RoleOperator || row[col("decided_by")] != e.a.UserID.String() ||
				row[col("decision_tray")] != "suggested" || row[col("decision_revision")] != "1" || row[col("decided_at")] == "" {
				t.Errorf("decision columns %v", row)
			}
		}
	}
	if decided != 1 {
		t.Errorf("%d approved rows; want 1", decided)
	}
	if rows, _ := e.store.MatchRows(e.ctx, day(20), day(31)); len(rows) != 1 {
		t.Errorf("rows outside the dates: %d", len(rows)-1)
	}
}

// F3 end to end: the Bancor file again with one description edited goes
// to quarantine through the Postgres store, and the panel still counts the
// month's ten movements. A cosmetic change enters with nothing new.
func TestPGEditedDescriptionCountsOnce(t *testing.T) {
	e := newPGEnv(t)
	var acct string
	if err := e.d.InTenant(e.ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(e.ctx, `select id::text from ledger.accounts`).Scan(&acct)
	}); err != nil {
		t.Fatal(err)
	}
	orig, err := os.ReadFile("../ledger/testdata/bancor_ok.csv")
	if err != nil {
		t.Fatal(err)
	}
	in := &ledger.Ingester{Store: &ledger.PGStore{DB: e.d}}
	upload := func(from, to string) ledger.Result {
		t.Helper()
		r, err := in.Ingest(e.ctx, ledger.Upload{TenantID: e.a.TenantID.String(), AccountID: acct, Bank: ledger.Bancor,
			Data: []byte(strings.Replace(string(orig), from, to, 1))})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := upload("COMISION MANTENIMIENTO CUENTA", "=1+1 COMISION"); r.Status != ledger.Quarantined ||
		r.Reason != ledger.ReasonMovementConflict {
		t.Fatalf("edited description: %s %s %s", r.Status, r.Reason, r.Detail)
	}
	if r := upload("COMISION MANTENIMIENTO CUENTA", "comision  mantenimiento cuenta"); r.Status != ledger.Accepted || r.New != 0 {
		t.Fatalf("cosmetic change: %s new %d (%s)", r.Status, r.New, r.Detail)
	}
	if n := e.count(t, e.ctx, `select count(*) from ledger.movements`); n != 10 {
		t.Fatalf("%d movements stored; want 10", n)
	}
	e.run(t)
	k := byKey(t, mustMetrics(t, e, "2026-10"))
	if v := value(t, k["automation_rate"]); v != 20 || !strings.Contains(k["automation_rate"].Source, "(10)") {
		t.Errorf("automation_rate %v from %q; want 20 of 10 movements", v, k["automation_rate"].Source)
	}
	if v := value(t, k["open_exception_movements"]); v != 7 {
		t.Errorf("open_exception_movements %v; want 7", v)
	}
}

// The tray lists and the results panel read the same definition
// (matching.waiting, and the escalated matches): on the same data their
// counts agree, whatever state the decisions leave the month in.
func TestPGTraysAndPanelAgree(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	const period = "2026-10"
	agree := func(step string) (exceptions, suggestedRows, escalated int) {
		t.Helper()
		// The operator sees every tray and every metric.
		e.setMembership(t, e.a, identity.RoleOperator)
		k := byKey(t, mustMetrics(t, e, period))
		exc, err := e.store.LatestPendingIn(e.ctx, Exception, period, 1000)
		if err != nil {
			t.Fatal(err)
		}
		sug, err := e.store.LatestPendingIn(e.ctx, Suggested, period, 1000)
		if err != nil {
			t.Fatal(err)
		}
		esc, err := e.store.UndecidedIn(e.ctx, Exception, period, 1000)
		if err != nil {
			t.Fatal(err)
		}
		if v := value(t, k["open_exception_movements"]); int(v) != len(exc) {
			t.Errorf("%s: panel open_exception_movements %v, Exceptions list %d", step, v, len(exc))
		}
		sum := 0
		for _, reason := range pendingReasons {
			sum += int(value(t, k["pending_"+string(reason)]))
		}
		if sum != len(sug) {
			t.Errorf("%s: panel pending in Suggested %d, Suggested list %d", step, sum, len(sug))
		}
		if v := value(t, k["open_exception_matches"]); int(v) != len(esc) {
			t.Errorf("%s: panel open_exception_matches %v, escalated list %d", step, v, len(esc))
		}
		return len(exc), len(sug), len(esc)
	}

	agree("after the run")
	fee := e.movementsByDescription(t, "COMISION MANTENIMIENTO CUENTA")[0]
	if _, err := e.decideMovementAs(t, identity.RoleApprover, fee, MovementOther, "gasto bancario"); err != nil {
		t.Fatal(err)
	}
	_, sug, _ := agree("after the client's other")
	if _, err := e.decideAs(t, identity.RoleOperator, suggested(r), Escalate, ""); err != nil {
		t.Fatal(err)
	}
	_, _, esc := agree("after an escalation")
	if _, err := e.decideMovementAs(t, identity.RoleOperator, fee, MovementReturn, "es tuyo"); err != nil {
		t.Fatal(err)
	}
	exc, _, _ := agree("after the operator's return")
	// Each tray had something in it at some step, or the agreement proves
	// nothing.
	if sug == 0 || esc == 0 || exc == 0 {
		t.Errorf("empty trays: suggested %d, escalated %d, exceptions %d", sug, esc, exc)
	}
}

func mustPanel(t *testing.T, e pgEnv, sp span) map[string]Metric {
	t.Helper()
	var ms []Metric
	if err := e.d.InTenant(e.ctx, func(tx pgx.Tx) error {
		var err error
		ms, err = panel(e.ctx, tx, sp, metricsNow)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return byKey(t, ms)
}

// Two spans that cut a month count, between them, what the month counts:
// the panel of a span filters by the movement's date, not by the month of
// the run. A span other than a month names its dates in its lists.
func TestPGPanelOfASpan(t *testing.T) {
	e := newPGEnv(t)
	e.run(t)
	fee := e.movementsByDescription(t, "COMISION MANTENIMIENTO CUENTA")[0]
	if _, err := e.decideMovementAs(t, identity.RoleApprover, fee, MovementOther, "gasto bancario"); err != nil {
		t.Fatal(err)
	}
	e.setMembership(t, e.a, identity.RoleOperator)
	month := byKey(t, mustMetrics(t, e, "2026-10"))
	early := mustPanel(t, e, span{From: day(1), Last: day(5)})
	late := mustPanel(t, e, span{From: day(6), Last: day(31)})

	counts := []string{"open_exception_movements", "open_exception_matches", "invoices_processed", "invoices_held"}
	for _, r := range pendingReasons {
		counts = append(counts, "pending_"+string(r))
	}
	for _, k := range counts {
		if month[k].Value == nil {
			continue
		}
		if a, b, m := value(t, early[k]), value(t, late[k]), value(t, month[k]); a+b != m {
			t.Errorf("%s: %v + %v; the month counts %v", k, a, b, m)
		}
	}
	// Supplier payment and VAT wait in Exceptions from the 1st to the 5th;
	// the client's "other" moved the commission to Suggested.
	if v := value(t, early["open_exception_movements"]); v != 2 {
		t.Errorf("open_exception_movements of the 1st to the 5th %v; want 2", v)
	}
	if v := value(t, early["pending_client_other"]); v != 1 {
		t.Errorf("pending_client_other of the 1st to the 5th %v; want 1", v)
	}
	if s := early["automation_rate"].Source; !strings.Contains(s, "(4)") || !strings.Contains(s, "del período") {
		t.Errorf("source %q", s)
	}
	if l := early["open_exception_movements"].List; l != "/v1/matching/pending?tray=exception&from=2026-10-01&to=2026-10-05" {
		t.Errorf("list %q", l)
	}
	if p := early["automation_rate"].Period; p != "" {
		t.Errorf("a span that is not a month has period %q", p)
	}

	// The second fortnight has no movements: no data, worded for a period.
	second := mustPanel(t, e, span{From: day(16), Last: day(31)})
	noData(t, second["automation_rate"])
	if s := second["automation_rate"].Source; s != "Sin movimientos cargados en el período" {
		t.Errorf("source %q", s)
	}
}
