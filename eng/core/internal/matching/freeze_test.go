package matching

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/closes"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
)

// The close of the fortnight, against the real closes service: Suggested
// blocks it, the summary it freezes is the client's view of the panel, and
// after it nothing of the fortnight moves: no reversion, no new operator
// decision, no run that changes it. The client still answers, and a match
// the client makes is marked as after the close.
func TestPGCloseFreezesTheFortnight(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	svc := &closes.Service{DB: e.d, Numbers: CloseNumbers{}, Now: func() time.Time { return metricsNow }}
	closeOct1 := func() (closes.Close, error) {
		t.Helper()
		e.setMembership(t, e.a, identity.RoleOperator)
		return svc.ClosePeriod(e.ctx, day(1))
	}
	pending := func(err error) int {
		t.Helper()
		var pe *closes.PendingError
		if !errors.As(err, &pe) {
			t.Fatalf("close: %v; want pending Suggested", err)
		}
		return pe.N
	}

	// The undecided suggestion blocks the close.
	if _, err := closeOct1(); pending(err) != 1 {
		t.Errorf("pending %d; want 1 (the suggestion)", pending(err))
	}
	if _, err := e.decideAs(t, identity.RoleOperator, suggested(r), Approve, ""); err != nil {
		t.Fatal(err)
	}
	// The client's "other" puts the commission in Suggested: it blocks too.
	fee := e.movementsByDescription(t, "COMISION MANTENIMIENTO CUENTA")[0]
	if _, err := e.decideMovementAs(t, identity.RoleApprover, fee, MovementOther, "gasto bancario"); err != nil {
		t.Fatal(err)
	}
	if _, err := closeOct1(); pending(err) != 1 {
		t.Errorf("pending %d; want 1 (the client's other)", pending(err))
	}
	if _, err := e.decideMovementAs(t, identity.RoleOperator, fee, MovementReturn, "es tuyo"); err != nil {
		t.Fatal(err)
	}

	before := e.trays(t)
	c, err := closeOct1()
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	keys := map[string]closes.Metric{}
	for _, m := range c.Summary {
		keys[m.Key] = m
		if m.Key == "suggested_acceptance_rate" || strings.HasPrefix(m.Key, "pending_") {
			t.Errorf("the frozen summary, read by the client, has %s", m.Key)
		}
	}
	if m := keys["automation_rate"]; m.Value == nil || !strings.Contains(m.List, "from=2026-10-01&to=2026-10-15") {
		t.Errorf("automation_rate %+v", m)
	}

	// Nothing reverts, the operator decides nothing new.
	if _, err := e.decideAs(t, identity.RoleOperator, suggested(r), Revert, ""); !errors.Is(err, ErrClosed) {
		t.Errorf("revert after the close: %v", err)
	}
	// The client still answers: the commission is "other" again.
	if _, err := e.decideMovementAs(t, identity.RoleApprover, fee, MovementOther, "gasto bancario, seguro"); err != nil {
		t.Errorf("the client's answer after the close: %v", err)
	}
	if _, err := e.decideMovementAs(t, identity.RoleOperator, fee, MovementReturn, "otra vez"); !errors.Is(err, ErrClosed) {
		t.Errorf("the operator's answer after the close: %v", err)
	}

	// An item that pairs with the twin transfers of Oct 6 arrives, and the
	// month runs again: the fortnight keeps what it had.
	ids, err := e.store.UpsertItems(e.ctx, "erp", []ItemIn{
		{SourceID: "FC-Z-1", Kind: "invoice", Date: day(6), Amount: 10000000, Confidence: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	matchesBefore := e.count(t, e.ctx, `select count(*) from matching.matches`)
	sum, err := e.store.RunMonth(e.ctx, "2026-10", DefaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Matches != 0 || sum.Pending != 0 || sum.Exceptions != 0 {
		t.Errorf("a run over a closed fortnight produced %+v", sum)
	}
	// A result computed before the close is saved after it (the close
	// slipped in between the engine and the save): the save drops what
	// touches the closed days on its own.
	in, err := e.store.LoadInput(e.ctx, e.from, e.to, DefaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	if late := Run(DefaultConfig, in); len(late.Matches)+len(late.Pending)+len(late.Exceptions) == 0 {
		t.Fatal("the unfiltered result is empty; it proves nothing")
	} else if _, _, err := e.store.SaveRun(e.ctx, "2026-10", DefaultConfig, late); err != nil {
		t.Fatal(err)
	}
	if n := e.count(t, e.ctx, `select count(*) from matching.matches`); n != matchesBefore {
		t.Errorf("matches %d after the runs; %d before", n, matchesBefore)
	}
	after := e.trays(t)
	// The client's second "other" is the only change since the close.
	before.suggested = append(before.suggested, fee)
	if !sameSet(after.exception, without(before.exception, fee)) || !sameSet(after.suggested, before.suggested) {
		t.Errorf("trays moved with the run:\nbefore %+v\nafter  %+v", before, after)
	}

	// The client links a twin by hand: the match is marked after the close,
	// and the export says so.
	twin := e.movementsByDescription(t, "TRANSFERENCIA RECIBIDA CLIENTE DOS")[0]
	d, err := e.decideMovementAs(t, identity.RoleApprover, twin, MovementLink, "", ids["FC-Z-1"].String())
	if err != nil {
		t.Fatalf("the client's link after the close: %v", err)
	}
	e.setMembership(t, e.a, identity.RoleOperator)
	rows, err := e.store.MatchRows(e.ctx, day(1), day(31))
	if err != nil {
		t.Fatal(err)
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[h] = i
	}
	seen := 0
	for _, row := range rows[1:] {
		want := "false"
		if row[col["match_id"]] == d.MatchID {
			want, seen = "true", seen+1
		}
		if row[col["after_close"]] != want {
			t.Errorf("match %s after_close %q; want %s", row[col["match_id"]], row[col["after_close"]], want)
		}
	}
	if seen != 1 {
		t.Errorf("the linked match is not in the export")
	}
}

// With no close, a match the client links by hand is not marked after the
// close: the mark is the close's, never the link's.
func TestPGLinkOnAnOpenDayIsNotAfterClose(t *testing.T) {
	e := newPGEnv(t)
	e.run(t)
	ids, err := e.store.UpsertItems(e.ctx, "erp", []ItemIn{
		{SourceID: "FC-Z-1", Kind: "invoice", Date: day(6), Amount: 10000000, Confidence: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	twin := e.movementsByDescription(t, "TRANSFERENCIA RECIBIDA CLIENTE DOS")[0]
	d, err := e.decideMovementAs(t, identity.RoleApprover, twin, MovementLink, "", ids["FC-Z-1"].String())
	if err != nil {
		t.Fatal(err)
	}
	e.setMembership(t, e.a, identity.RoleOperator)
	rows, err := e.store.MatchRows(e.ctx, day(1), day(31))
	if err != nil {
		t.Fatal(err)
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[h] = i
	}
	seen := false
	for _, row := range rows[1:] {
		if row[col["match_id"]] == d.MatchID {
			seen = true
			if v := row[col["after_close"]]; v != "false" {
				t.Errorf("a link on an open day has after_close %q", v)
			}
		}
	}
	if !seen {
		t.Error("the linked match is not in the export")
	}
}

type trayIDs struct{ exception, suggested []string }

func (e pgEnv) trays(t *testing.T) trayIDs {
	t.Helper()
	e.setMembership(t, e.a, identity.RoleOperator)
	var out trayIDs
	for tray, dst := range map[Tray]*[]string{Exception: &out.exception, Suggested: &out.suggested} {
		rows, err := e.store.LatestPendingIn(e.ctx, tray, "2026-10", 1000)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			*dst = append(*dst, r.MovementID)
		}
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func without(ss []string, x string) []string {
	var out []string
	for _, s := range ss {
		if s != x {
			out = append(out, s)
		}
	}
	return out
}
