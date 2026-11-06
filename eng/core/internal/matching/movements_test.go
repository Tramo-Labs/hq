package matching

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	apigen "github.com/Tramo-Labs/hq/eng/core/gen/api"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
)

// movementsByDescription returns the ids of tenant a's movements with that
// description, in line order.
func (e pgEnv) movementsByDescription(t *testing.T, desc string) []string {
	t.Helper()
	var out []string
	if err := e.d.InTenant(e.ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(e.ctx, `select id from ledger.movements where description = $1 order by line`, desc)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			out = append(out, id.String())
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func (e pgEnv) decideMovementAs(t *testing.T, role, movement string, a MovementAction, note string, items ...string) (MovementDecision, error) {
	t.Helper()
	e.setMembership(t, e.a, role)
	d, _, err := e.store.DecideMovement(e.ctx, MovementDecisionInput{Key: uuid.NewString(), MovementID: movement,
		Action: a, Note: note, ItemIDs: items})
	return d, err
}

func (e pgEnv) pendingIDs(t *testing.T, tray Tray) map[string]PendingRow {
	t.Helper()
	rows, err := e.store.LatestPending(e.ctx, tray, 100)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]PendingRow{}
	for _, r := range rows {
		out[r.MovementID] = r
	}
	return out
}

// "Es otra cosa": the client's note sends the movement back to the operator;
// it leaves the client's tray. Reverting puts it back.
func TestPGMovementOtherGoesBackToTheOperator(t *testing.T) {
	e := newPGEnv(t)
	e.run(t)
	fee := e.movementsByDescription(t, "COMISION MANTENIMIENTO CUENTA")[0]
	row, ok := e.pendingIDs(t, Exception)[fee]
	if !ok {
		t.Fatal("the fee should be an exception without candidate")
	}
	// Each pending row carries its movement, so the client can tell them apart.
	if row.Description != "COMISION MANTENIMIENTO CUENTA" || row.Amount != -850000 || !row.Date.Equal(day(2)) ||
		row.Bank != "bancor" || row.AccountName != "Cuenta" {
		t.Errorf("pending row without its movement: %+v", row)
	}
	if _, err := e.decideMovementAs(t, identity.RoleApprover, fee, MovementOther, "\u200b\u200b"); !errors.Is(err, ErrNoteRequired) {
		t.Errorf("other without a note: %v", err)
	}
	for _, role := range []string{identity.RoleOperator, identity.RoleViewer, identity.RoleAccountant} {
		if _, err := e.decideMovementAs(t, role, fee, MovementOther, "x"); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s decided a client's exception: %v", role, err)
		}
	}
	d, err := e.decideMovementAs(t, identity.RoleApprover, fee, MovementOther, "comisión del banco, va a gastos")
	if err != nil || d.Revision != 1 || d.Role != identity.RoleApprover {
		t.Fatalf("approver other: %v %+v", err, d)
	}
	if _, ok := e.pendingIDs(t, Exception)[fee]; ok {
		t.Error("a decided movement is still in the client's tray")
	}
	e.setMembership(t, e.a, identity.RoleOperator) // Suggested is the operator's to see
	if r, ok := e.pendingIDs(t, Suggested)[fee]; !ok || r.Reason != ReasonClientOther || r.Note == "" {
		t.Errorf("not back with the operator: %+v", r)
	}
	if _, err := e.decideMovementAs(t, identity.RoleOwner, fee, MovementOther, "otra"); !errors.Is(err, ErrAlreadyDecided) {
		t.Errorf("second decision: %v", err)
	}
	if d, err := e.decideMovementAs(t, identity.RoleOwner, fee, MovementRevert, ""); err != nil || d.Revision != 2 {
		t.Fatalf("revert: %v %+v", err, d)
	}
	if _, ok := e.pendingIDs(t, Exception)[fee]; !ok {
		t.Error("a reverted decision should put the movement back in Exceptions")
	}
	if _, err := e.decideMovementAs(t, identity.RoleOwner, fee, MovementRevert, ""); !errors.Is(err, ErrNothingToRevert) {
		t.Errorf("revert with nothing in effect: %v", err)
	}
}

// Linking by hand: exact sum only; the link is settled for the next runs and
// its items cannot be linked again; reverting frees both.
func TestPGMovementLinkByHand(t *testing.T) {
	e := newPGEnv(t)
	e.run(t)
	twins := e.movementsByDescription(t, "TRANSFERENCIA RECIBIDA CLIENTE DOS")
	ids, err := e.store.UpsertItems(e.ctx, "erp", []ItemIn{
		{SourceID: "FC-B-1", Kind: "invoice", Date: day(5), Amount: 6000000, Confidence: 1},
		{SourceID: "FC-B-2", Kind: "invoice", Date: day(5), Amount: 4000000, Confidence: 1},
		{SourceID: "FC-B-3", Kind: "invoice", Date: day(5), Amount: 3999900, Confidence: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	b1, b2, b3 := ids["FC-B-1"].String(), ids["FC-B-2"].String(), ids["FC-B-3"].String()

	if _, err := e.decideMovementAs(t, identity.RoleApprover, twins[0], MovementLink, "", b1, b3); !errors.Is(err, ErrLinkDoesNotAdd) {
		t.Errorf("link $1 short: %v", err)
	}
	d, err := e.decideMovementAs(t, identity.RoleApprover, twins[0], MovementLink, "", b1, b2)
	if err != nil || d.MatchID == "" {
		t.Fatalf("link: %v %+v", err, d)
	}
	m, err := e.store.GetMatch(e.ctx, d.MatchID)
	if err != nil || m.Rule != RuleManual || m.Tray != Manual || len(m.ItemIDs) != 2 {
		t.Fatalf("manual match: %v %+v", err, m)
	}
	if _, err := e.decideMovementAs(t, identity.RoleApprover, twins[1], MovementLink, "", b1, b2); !errors.Is(err, ErrItemTaken) {
		t.Errorf("same items for the twin: %v", err)
	}
	// Next run: the linked movement is settled; the twin is still an exception.
	e.run(t)
	pend := e.pendingIDs(t, Exception)
	if _, ok := pend[twins[0]]; ok {
		t.Error("linked movement still pending")
	}
	if _, ok := pend[twins[1]]; !ok {
		t.Error("the twin should still be an exception")
	}
	// Revert the link: the movement is back and the items are free.
	if _, err := e.decideMovementAs(t, identity.RoleOwner, twins[0], MovementRevert, ""); err != nil {
		t.Fatal(err)
	}
	if d, err := e.decideMovementAs(t, identity.RoleApprover, twins[1], MovementLink, "", b1, b2); err != nil || d.MatchID == "" {
		t.Errorf("items after the revert: %v", err)
	}
}

func TestPGMovementMustBeAnException(t *testing.T) {
	e := newPGEnv(t)
	e.run(t)
	cheque := e.movementsByDescription(t, "ECHEQ 00012345 COBRADO")[0] // matched by reference
	if _, err := e.decideMovementAs(t, identity.RoleApprover, cheque, MovementOther, "x"); !errors.Is(err, ErrNotInExceptions) {
		t.Errorf("decided a matched movement: %v", err)
	}
	if _, err := e.decideMovementAs(t, identity.RoleApprover, uuid.NewString(), MovementOther, "x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown movement: %v", err)
	}
}

// A role lowered while the token is still valid stops movement decisions too.
// A viewer that sends the key of someone else's decision gets 403, not a
// replay nor a 422: the role is checked before the key, for matches and
// for movements. The same viewer with a fresh key is 403 too.
func TestPGRoleBeforeIdempotencyKey(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	m := e.escalated(t, r)
	fee := e.movementsByDescription(t, "COMISION MANTENIMIENTO CUENTA")[0]

	e.setMembership(t, e.a, identity.RoleApprover)
	matchKey, moveKey := uuid.NewString(), uuid.NewString()
	if _, _, err := e.store.Record(e.ctx, DecisionInput{Key: matchKey, Match: m, Action: Approve}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.store.DecideMovement(e.ctx, MovementDecisionInput{Key: moveKey, MovementID: fee,
		Action: MovementOther, Note: "gasto bancario"}); err != nil {
		t.Fatal(err)
	}
	// Another person of the tenant, a viewer, sends the approver's keys (F1).
	other := uuid.New()
	bg := context.Background()
	if err := e.d.System(bg, func(tx pgx.Tx) error {
		if _, err := tx.Exec(bg, `insert into users (id, email) values ($1, 'viewer@alfa.test')`, other); err != nil {
			return err
		}
		_, err := tx.Exec(bg, `insert into memberships (user_id, tenant_id, role) values ($1, $2, 'viewer')`, other, e.a.TenantID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	vctx := session(db.Scope{TenantID: e.a.TenantID, UserID: other}, identity.RoleViewer)
	if _, _, err := e.store.Record(vctx, DecisionInput{Key: matchKey, Match: m, Action: Approve}); !errors.Is(err, ErrForbidden) {
		t.Errorf("another viewer with the approver's match key: %v; want forbidden", err)
	}
	if _, _, err := e.store.DecideMovement(vctx, MovementDecisionInput{Key: moveKey, MovementID: fee,
		Action: MovementOther, Note: "gasto bancario"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("another viewer with the approver's movement key: %v; want forbidden", err)
	}

	// The approver itself, lowered: no replay of its own decision either.
	for _, role := range []string{identity.RoleViewer, identity.RoleAccountant} {
		e.setMembership(t, e.a, role)
		if _, _, err := e.store.Record(e.ctx, DecisionInput{Key: matchKey, Match: m, Action: Approve}); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s with the approver's match key: %v; want forbidden", role, err)
		}
		if _, _, err := e.store.DecideMovement(e.ctx, MovementDecisionInput{Key: moveKey, MovementID: fee,
			Action: MovementOther, Note: "gasto bancario"}); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s with the approver's movement key: %v; want forbidden", role, err)
		}
	}
}

func TestPGMovementDowngradedRole(t *testing.T) {
	e := newPGEnv(t)
	e.run(t)
	fee := e.movementsByDescription(t, "COMISION MANTENIMIENTO CUENTA")[0]
	if _, err := e.decideMovementAs(t, identity.RoleViewer, fee, MovementOther, "x"); !errors.Is(err, ErrForbidden) {
		t.Errorf("downgraded approver decided: %v", err)
	}
}

func TestPGMovementDecisionsAppendOnly(t *testing.T) {
	e := newPGEnv(t)
	e.run(t)
	fee := e.movementsByDescription(t, "COMISION MANTENIMIENTO CUENTA")[0]
	if _, err := e.decideMovementAs(t, identity.RoleApprover, fee, MovementOther, "x"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, q := range []string{`update matching.movement_decisions set note = 'edited'`, `delete from matching.movement_decisions`} {
		if _, err := e.d.Pool.Exec(ctx, q); err == nil || !strings.Contains(err.Error(), "append only") {
			t.Errorf("%q through the owner: %v", q, err)
		}
	}
}

func TestPGMovementIsolation(t *testing.T) {
	e := newPGEnv(t)
	e.run(t)
	fee := e.movementsByDescription(t, "COMISION MANTENIMIENTO CUENTA")[0]
	_, _, err := e.store.DecideMovement(e.ctxB, MovementDecisionInput{Key: uuid.NewString(), MovementID: fee,
		Action: MovementOther, Note: "x"})
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("tenant b decided on a's movement: %v", err)
	}
	if n := e.count(t, e.ctxB, `select count(*) from matching.movement_decisions`); n != 0 {
		t.Errorf("tenant b sees %d movement decisions", n)
	}
}

// Reversion of match decisions: a new row, by whoever decides that tray;
// the item is back where it was and can be decided again.
func TestPGRevertAndDecideAgain(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	m := suggested(r)
	if _, err := e.decideAs(t, identity.RoleOperator, m, Revert, ""); !errors.Is(err, ErrNothingToRevert) {
		t.Errorf("revert with nothing in effect: %v", err)
	}
	if _, err := e.decideAs(t, identity.RoleOperator, m, Approve, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.decideAs(t, identity.RoleApprover, m, Revert, ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("approver reverted an operator decision: %v", err)
	}
	if d, err := e.decideAs(t, identity.RoleOperator, m, Revert, "aprobado por error"); err != nil || d.Revision != 2 || d.Tray != Suggested {
		t.Fatalf("operator revert: %v %+v", err, d)
	}
	if list, _ := e.store.Undecided(e.ctx, Suggested, 10); len(list) != 1 || list[0].ID != m.ID {
		t.Errorf("reverted match not back in Suggested: %+v", list)
	}
	if d, err := e.decideAs(t, identity.RoleOperator, m, Escalate, ""); err != nil || d.Revision != 3 {
		t.Fatalf("decide again: %v %+v", err, d)
	}
	// Escalated and approved by the client: only the client reverts that.
	if _, err := e.decideAs(t, identity.RoleApprover, m, Approve, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.decideAs(t, identity.RoleOperator, m, Revert, ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("operator reverted the client's decision: %v", err)
	}
	if d, err := e.decideAs(t, identity.RoleApprover, m, Revert, ""); err != nil || d.Tray != Exception {
		t.Fatalf("client revert: %v %+v", err, d)
	}
	if list, _ := e.store.Undecided(e.ctx, Exception, 10); len(list) != 1 {
		t.Errorf("reverted client decision not back in Exceptions: %+v", list)
	}
	// Now the escalation is the latest decision in effect: the operator's.
	if _, err := e.decideAs(t, identity.RoleOperator, m, Revert, ""); err != nil {
		t.Fatalf("operator reverts the escalation: %v", err)
	}
	if list, _ := e.store.Undecided(e.ctx, Suggested, 10); len(list) != 1 {
		t.Errorf("de-escalated match not back in Suggested: %+v", list)
	}
	// Nothing was edited: six rows (approve, revert, escalate, approve,
	// revert, revert), three of them reversions.
	if n := e.count(t, e.ctx, `select count(*) from matching.decisions where match_id = $1`, m.ID); n != 6 {
		t.Errorf("%d decision rows; want 6", n)
	}
	if n := e.count(t, e.ctx, `select count(*) from matching.decisions where match_id = $1 and action = 'revert'`, m.ID); n != 3 {
		t.Errorf("%d revert rows; want 3", n)
	}
}

// barrier makes n concurrent decisions wait for each other after all their
// checks and before their insert, so they really collide on the insert
// instead of being serialized by luck.
func (e pgEnv) barrier(t *testing.T, n int) {
	t.Helper()
	var arrived sync.WaitGroup
	arrived.Add(n)
	e.store.beforeInsert = func() {
		arrived.Done()
		done := make(chan struct{})
		go func() { arrived.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("barrier: the other decision never reached the insert")
		}
	}
	t.Cleanup(func() { e.store.beforeInsert = nil })
}

// race runs the decisions at once and returns their errors.
func race(fns ...func() error) []error {
	errs := make([]error, len(fns))
	var wg sync.WaitGroup
	for i, fn := range fns {
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = fn() }()
	}
	wg.Wait()
	return errs
}

// exactlyOneApplied checks one nil error and every other ErrAlreadyDecided
// (a 409 for the API, never a 500).
func exactlyOneApplied(t *testing.T, errs []error) {
	t.Helper()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrAlreadyDecided):
		default:
			t.Errorf("concurrent decision failed with %v; want ErrAlreadyDecided", err)
		}
	}
	if ok != 1 {
		t.Errorf("%d concurrent decisions applied; want exactly 1 (%v)", ok, errs)
	}
}

// Two people decide the same match at the same time with different keys:
// one decision is applied, the other is a conflict.
func TestPGConcurrentMatchDecisions(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	m := suggested(r)
	e.barrier(t, 2)
	decide := func(a Action) func() error {
		return func() error {
			_, _, err := e.store.Record(e.ctx, DecisionInput{Key: uuid.NewString(), Match: m, Action: a, Note: "why"})
			return err
		}
	}
	exactlyOneApplied(t, race(decide(Approve), decide(Reject)))
	if n := e.count(t, e.ctx, `select count(*) from matching.decisions where match_id = $1`, m.ID); n != 1 {
		t.Errorf("%d decisions stored; want 1", n)
	}
	if fail := t.Failed(); fail {
		return
	}
	// Same for two reversions of the same decision (a fresh barrier).
	e.barrier(t, 2)
	exactlyOneApplied(t, race(decide(Revert), decide(Revert)))
}

// The same on a movement: two client decisions at once, one applies.
func TestPGConcurrentMovementDecisions(t *testing.T) {
	e := newPGEnv(t)
	e.run(t)
	fee := e.movementsByDescription(t, "COMISION MANTENIMIENTO CUENTA")[0]
	e.setMembership(t, e.a, identity.RoleApprover)
	e.barrier(t, 2)
	decide := func(note string) func() error {
		return func() error {
			_, _, err := e.store.DecideMovement(e.ctx, MovementDecisionInput{Key: uuid.NewString(), MovementID: fee,
				Action: MovementOther, Note: note})
			return err
		}
	}
	exactlyOneApplied(t, race(decide("gasto bancario"), decide("comisión")))
	if n := e.count(t, e.ctx, `select count(*) from matching.movement_decisions where movement_id = $1`, fee); n != 1 {
		t.Errorf("%d movement decisions stored; want 1", n)
	}
}

// What already closed is not reverted. The close does not exist yet: the
// check lives in one place (PGStore.closed) and is exercised with a stub.
func TestPGNoRevertAfterClose(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	m := suggested(r)
	if _, err := e.decideAs(t, identity.RoleOperator, m, Approve, ""); err != nil {
		t.Fatal(err)
	}
	fee := e.movementsByDescription(t, "COMISION MANTENIMIENTO CUENTA")[0]
	if _, err := e.decideMovementAs(t, identity.RoleApprover, fee, MovementOther, "x"); err != nil {
		t.Fatal(err)
	}
	var asked []string
	e.store.Closed = func(_ context.Context, _ pgx.Tx, periods []string) (bool, error) {
		asked = append(asked, periods...)
		return true, nil
	}
	if _, err := e.decideAs(t, identity.RoleOperator, m, Revert, ""); !errors.Is(err, ErrClosed) {
		t.Errorf("match reverted in a closed period: %v", err)
	}
	if _, err := e.decideMovementAs(t, identity.RoleApprover, fee, MovementRevert, ""); !errors.Is(err, ErrClosed) {
		t.Errorf("movement reverted in a closed period: %v", err)
	}
	if len(asked) != 2 || asked[0] != "2026-10" {
		t.Errorf("periods checked: %v", asked)
	}
	// New decisions are not stopped by the close check (only reversions).
	e.store.Closed = nil
	if _, err := e.decideAs(t, identity.RoleOperator, m, Revert, ""); err != nil {
		t.Errorf("with no close, the revert goes through: %v", err)
	}
}

// F20, spec section 1: the client never sees the Suggested tray. Only an
// operator lists Suggested matches and pending movements, gets undecided or
// rejected suggestions in the matches export, and the metrics about
// Suggested. Everyone sees Automatic as a result, and Exceptions.
func TestPGSuggestedIsTheOperatorsOnly(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	sug := suggested(r)
	exportHas := func(id string) bool {
		rows, err := e.store.MatchRows(e.ctx, day(1), day(31))
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows[1:] {
			if row[0] == id {
				return true
			}
		}
		return false
	}
	metricKeys := func() map[string]bool {
		out := map[string]bool{}
		for _, m := range mustMetrics(t, e, "2026-10") {
			out[m.Key] = true
		}
		return out
	}
	for _, role := range []string{identity.RoleOwner, identity.RoleApprover, identity.RoleViewer, identity.RoleAccountant} {
		e.setMembership(t, e.a, role)
		if _, err := e.store.UndecidedIn(e.ctx, Suggested, "", 10); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s lists suggested matches: %v", role, err)
		}
		if _, err := e.store.LatestPendingIn(e.ctx, Suggested, "", 10); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s lists suggested pending movements: %v", role, err)
		}
		if _, err := e.store.UndecidedIn(e.ctx, Auto, "", 10); err != nil {
			t.Errorf("%s cannot list automatic results: %v", role, err)
		}
		if _, err := e.store.LatestPendingIn(e.ctx, Exception, "", 10); err != nil {
			t.Errorf("%s cannot list exceptions: %v", role, err)
		}
		if exportHas(sug.ID) {
			t.Errorf("%s gets an undecided suggestion in the export", role)
		}
		if k := metricKeys(); k["suggested_acceptance_rate"] || k["pending_ambiguous"] || !k["automation_rate"] {
			t.Errorf("%s metrics: %v", role, k)
		}
	}
	e.setMembership(t, e.a, identity.RoleOperator)
	if l, err := e.store.UndecidedIn(e.ctx, Suggested, "", 10); err != nil || len(l) != 1 {
		t.Errorf("operator: %d suggested, %v", len(l), err)
	}
	if !exportHas(sug.ID) || !metricKeys()["suggested_acceptance_rate"] {
		t.Error("operator does not see the Suggested tray")
	}
	// Escalated, it is the client's Exceptions: now the owner sees it.
	if _, err := e.decideAs(t, identity.RoleOperator, sug, Escalate, "confirmar con el cliente"); err != nil {
		t.Fatal(err)
	}
	e.setMembership(t, e.a, identity.RoleOwner)
	if !exportHas(sug.ID) {
		t.Error("owner does not see the match sent to Exceptions")
	}
}

// F4: after the client's "other" the operator answers: resolve (note),
// return to the client (note, back in Exceptions with it) or link. One
// test per rule, so a failure names the rule that broke.

// clientOther runs the month and has the approver mark the bank fee as
// "other". It returns the env, the fee and the open exceptions before.
func clientOther(t *testing.T) (pgEnv, string, float64) {
	t.Helper()
	e := newPGEnv(t)
	e.run(t)
	fee := e.movementsByDescription(t, "COMISION MANTENIMIENTO CUENTA")[0]
	before := e.openExceptions(t)
	if _, err := e.decideMovementAs(t, identity.RoleApprover, fee, MovementOther, "retiro de socio"); err != nil {
		t.Fatal(err)
	}
	return e, fee, before
}

func (e pgEnv) openExceptions(t *testing.T) float64 {
	t.Helper()
	return value(t, byKey(t, mustMetrics(t, e, "2026-10"))["open_exception_movements"])
}

// Only the operator answers, only after the client's "other", and resolve
// and return need a note.
func TestPGOperatorTurnFollowsClientOther(t *testing.T) {
	e := newPGEnv(t)
	e.run(t)
	fee := e.movementsByDescription(t, "COMISION MANTENIMIENTO CUENTA")[0]
	if _, err := e.decideMovementAs(t, identity.RoleOperator, fee, MovementResolve, "x"); !errors.Is(err, ErrNotWithOperator) {
		t.Errorf("operator answered before the client: %v", err)
	}
	if _, err := e.decideMovementAs(t, identity.RoleApprover, fee, MovementOther, "retiro de socio"); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{identity.RoleOwner, identity.RoleApprover, identity.RoleViewer, identity.RoleAccountant} {
		if _, err := e.decideMovementAs(t, role, fee, MovementResolve, "x"); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s resolved: %v", role, err)
		}
	}
	if _, err := e.decideMovementAs(t, identity.RoleOperator, fee, MovementResolve, "\u3164"); !errors.Is(err, ErrNoteRequired) {
		t.Errorf("resolve without a note: %v", err)
	}
	if _, err := e.decideMovementAs(t, identity.RoleOperator, fee, MovementReturn, ""); !errors.Is(err, ErrNoteRequired) {
		t.Errorf("return without a note: %v", err)
	}
}

// Return: back in the client's Exceptions with the operator's note, open
// again in the panel; the client cannot revert the operator's row, and the
// operator no longer has the turn.
func TestPGOperatorReturnsToTheClient(t *testing.T) {
	e, fee, before := clientOther(t)
	if d, err := e.decideMovementAs(t, identity.RoleOperator, fee, MovementReturn, "¿de qué socio? pasame el acta"); err != nil || d.Revision != 2 || d.Role != identity.RoleOperator {
		t.Fatalf("return: %v %+v", err, d)
	}
	if _, ok := e.operatorQueue(t)[fee]; ok {
		t.Error("a returned movement is still with the operator")
	}
	if r, ok := e.pendingIDs(t, Exception)[fee]; !ok || r.Reason != ReasonOperatorReturned || r.Note != "¿de qué socio? pasame el acta" {
		t.Errorf("not back with the client: %+v", r)
	}
	if got := e.openExceptions(t); got != before {
		t.Errorf("a returned movement is not open for the client: %v; want %v", got, before)
	}
	if _, err := e.decideMovementAs(t, identity.RoleOwner, fee, MovementRevert, ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("the client reverted the operator's return: %v", err)
	}
	if _, err := e.decideMovementAs(t, identity.RoleOperator, fee, MovementResolve, "x"); !errors.Is(err, ErrNotWithOperator) {
		t.Errorf("operator resolved a movement it returned: %v", err)
	}
	// The client answers again: back with the operator, with the new note.
	if _, err := e.decideMovementAs(t, identity.RoleOwner, fee, MovementOther, "retiro de Juan, acta 12"); err != nil {
		t.Fatal(err)
	}
	if r := e.operatorQueue(t)[fee]; r.Reason != ReasonClientOther || r.Note != "retiro de Juan, acta 12" {
		t.Errorf("second other: %+v", r)
	}
}

// Resolve: in neither tray, also after the next run, no longer open in the
// panel, and the client cannot decide on it again.
func TestPGOperatorResolves(t *testing.T) {
	e, fee, before := clientOther(t)
	if d, err := e.decideMovementAs(t, identity.RoleOperator, fee, MovementResolve, "asiento manual de retiro"); err != nil || d.Revision != 2 {
		t.Fatalf("resolve: %v %+v", err, d)
	}
	e.run(t) // the state is per movement, not per run
	if _, ok := e.operatorQueue(t)[fee]; ok {
		t.Error("resolved movement still with the operator")
	}
	if _, ok := e.pendingIDs(t, Exception)[fee]; ok {
		t.Error("resolved movement back with the client")
	}
	if got := e.openExceptions(t); got != before-1 {
		t.Errorf("open exceptions after resolve: %v; want %v", got, before-1)
	}
	if _, err := e.decideMovementAs(t, identity.RoleApprover, fee, MovementOther, "y"); !errors.Is(err, ErrAlreadyDecided) {
		t.Errorf("client decided a resolved movement: %v", err)
	}
}

// Reverting the operator's answer puts the client's "other" back in
// effect, note included; the operator cannot revert the client's row.
func TestPGRevertingTheOperatorRestoresClientOther(t *testing.T) {
	e, fee, _ := clientOther(t)
	if _, err := e.decideMovementAs(t, identity.RoleOperator, fee, MovementResolve, "asiento manual de retiro"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.decideMovementAs(t, identity.RoleOperator, fee, MovementRevert, ""); err != nil {
		t.Fatal(err)
	}
	if r := e.operatorQueue(t)[fee]; r.Reason != ReasonClientOther || r.Note != "retiro de socio" {
		t.Errorf("the client's other is not back in effect: %+v", r)
	}
	if _, err := e.decideMovementAs(t, identity.RoleOperator, fee, MovementRevert, ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("the operator reverted the client's other: %v", err)
	}
}

// Link: the operator links it to an open item that adds up exactly, and
// it leaves the operator's tray.
func TestPGOperatorLinksClientOther(t *testing.T) {
	e, fee, _ := clientOther(t)
	e.setMembership(t, e.a, identity.RoleOwner) // items are loaded as the client
	ids, err := e.store.UpsertItems(e.ctx, "erp", []ItemIn{
		{SourceID: "ND-1", Kind: "invoice", Date: day(2), Amount: -850000, Confidence: 1}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := e.decideMovementAs(t, identity.RoleOperator, fee, MovementLink, "", ids["ND-1"].String())
	if err != nil || d.MatchID == "" {
		t.Fatalf("operator link: %v %+v", err, d)
	}
	if _, ok := e.operatorQueue(t)[fee]; ok {
		t.Error("linked movement still with the operator")
	}
}

// operatorQueue is the Suggested tray as the operator sees it (only the
// operator may list it).
func (e pgEnv) operatorQueue(t *testing.T) map[string]PendingRow {
	t.Helper()
	e.setMembership(t, e.a, identity.RoleOperator)
	return e.pendingIDs(t, Suggested)
}

// A client's reject of an escalated match goes back to the operator: each
// movement of the match lists in Suggested as client_rejected, with the
// client's reason and the match. The operator answers it like
// client_other; the client reverts its reject on the match, only while the
// operator has not answered. The panel counts what the lists show.
func TestPGClientRejectGoesBackToTheOperator(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	m := e.escalated(t, r)
	mv := m.MovementIDs[0]
	if len(m.MovementIDs) != 1 {
		t.Fatalf("fixture: the suggested match has %d movements", len(m.MovementIDs))
	}
	if _, err := e.decideAs(t, identity.RoleApprover, m, Reject, ""); !errors.Is(err, ErrNoteRequired) {
		t.Errorf("reject without a reason: %v", err)
	}
	if _, err := e.decideAs(t, identity.RoleApprover, m, Reject, "no es de este proveedor"); err != nil {
		t.Fatal(err)
	}
	row, ok := e.operatorQueue(t)[mv]
	if !ok || row.Reason != ReasonClientRejected || row.Note != "no es de este proveedor" || row.MatchID != m.ID {
		t.Fatalf("not back with the operator: %+v", row)
	}
	if _, ok := e.pendingIDs(t, Exception)[mv]; ok {
		t.Error("a rejected movement is in the client's tray")
	}
	if v := value(t, byKey(t, mustMetrics(t, e, "2026-10"))["pending_client_rejected"]); v != 1 {
		t.Errorf("pending_client_rejected %v; want 1", v)
	}
	// The client reverts on the match, not on the movement.
	if _, err := e.decideMovementAs(t, identity.RoleApprover, mv, MovementRevert, ""); !errors.Is(err, ErrRevertOnMatch) {
		t.Errorf("client revert on the movement: %v", err)
	}
	if _, err := e.decideMovementAs(t, identity.RoleOperator, mv, MovementRevert, ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("operator reverted the client's reject: %v", err)
	}

	// The operator returns it; the client cannot take its reject back now.
	if _, err := e.decideMovementAs(t, identity.RoleOperator, mv, MovementReturn, "¿de qué proveedor es?"); err != nil {
		t.Fatal(err)
	}
	e.setMembership(t, e.a, identity.RoleApprover)
	if row := e.pendingIDs(t, Exception)[mv]; row.Reason != ReasonOperatorReturned || row.Note != "¿de qué proveedor es?" {
		t.Errorf("not back with the client: %+v", row)
	}
	if _, err := e.decideAs(t, identity.RoleApprover, m, Revert, ""); !errors.Is(err, ErrOperatorAnswered) {
		t.Errorf("client reverted a reject the operator answered: %v", err)
	}
	// The operator takes its return back: the client's reject is in effect.
	if _, err := e.decideMovementAs(t, identity.RoleOperator, mv, MovementRevert, ""); err != nil {
		t.Fatal(err)
	}
	if row := e.operatorQueue(t)[mv]; row.Reason != ReasonClientRejected {
		t.Errorf("the client's reject is not back in effect: %+v", row)
	}
	// Now the client may revert its reject: the match waits for it again.
	if _, err := e.decideAs(t, identity.RoleApprover, m, Revert, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.operatorQueue(t)[mv]; ok {
		t.Error("a reverted reject is still with the operator")
	}
	e.setMembership(t, e.a, identity.RoleApprover)
	waiting, err := e.store.UndecidedIn(e.ctx, Exception, "", 10)
	if err != nil || len(waiting) != 1 || waiting[0].ID != m.ID {
		t.Errorf("the match does not wait for the client again: %v %+v", err, waiting)
	}

	// Rejected again and resolved by the operator: in neither tray, also
	// after the next run, which does not propose the rejected match again.
	if _, err := e.decideAs(t, identity.RoleApprover, m, Reject, "tampoco"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.decideMovementAs(t, identity.RoleOperator, mv, MovementResolve, "pago a cuenta, asiento manual"); err != nil {
		t.Fatal(err)
	}
	r2, _ := e.run(t)
	for _, x := range r2.Matches {
		if x.ID == m.ID {
			t.Error("the rejected match was proposed again")
		}
	}
	if _, ok := e.operatorQueue(t)[mv]; ok {
		t.Error("resolved movement still with the operator")
	}
	if _, ok := e.pendingIDs(t, Exception)[mv]; ok {
		t.Error("resolved movement with the client")
	}
	if v := value(t, byKey(t, mustMetrics(t, e, "2026-10"))["pending_client_rejected"]); v != 0 {
		t.Errorf("pending_client_rejected %v after resolve; want 0", v)
	}
}

// A movement with the operator stops waiting when a later run settles it
// with a match in effect: the list never shows something already settled.
func TestPGSettledMovementLeavesTheOperator(t *testing.T) {
	e := newPGEnv(t)
	e.run(t)
	fee := e.movementsByDescription(t, "COMISION MANTENIMIENTO CUENTA")[0]
	if _, err := e.decideMovementAs(t, identity.RoleApprover, fee, MovementOther, "comisión, falta la nota de débito"); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.operatorQueue(t)[fee]; !ok {
		t.Fatal("not with the operator")
	}
	if _, err := e.store.UpsertItems(e.ctx, "erp", []ItemIn{
		{SourceID: "ND-1", Kind: "payable", Date: day(2), Amount: -850000, Confidence: 1}}); err != nil {
		t.Fatal(err)
	}
	e.run(t) // the debit note settles the fee automatically
	if _, ok := e.operatorQueue(t)[fee]; ok {
		t.Error("a settled movement is still with the operator")
	}
}

// openSources lists the source ids of the open items for the filters, in
// the order the list gives them (date, then source id).
func (e pgEnv) openSources(t *testing.T, ctx context.Context, period, direction string) []string {
	t.Helper()
	items, _, err := e.store.OpenItems(ctx, period, direction, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, it := range items {
		out = append(out, it.SourceID)
	}
	return out
}

// What "link by hand" offers: the items no match in effect settles. An
// automatic match settles; a suggested one not yet approved does not; a
// manual link settles while it is in effect.
func TestPGOpenItems(t *testing.T) {
	e := newPGEnv(t)
	fee := e.movementsByDescription(t, "COMISION MANTENIMIENTO CUENTA")[0]
	if got := e.openSources(t, e.ctx, "", ""); !slices.Equal(got, []string{"CH-12345", "FC-A-1", "FC-A-2"}) {
		t.Errorf("before any run: %v", got)
	}
	e.run(t) // the cheque and FC-A-1 match automatically; FC-A-2 is only suggested
	// A debit note loaded after the run: open until something settles it.
	if _, err := e.store.UpsertItems(e.ctx, "erp", []ItemIn{
		{SourceID: "ND-1", Kind: "payable", Date: day(2), Amount: -850000, Confidence: 1}}); err != nil {
		t.Fatal(err)
	}
	if got := e.openSources(t, e.ctx, "", ""); !slices.Equal(got, []string{"ND-1", "FC-A-2"}) {
		t.Errorf("after the run: %v", got)
	}
	if got := e.openSources(t, e.ctx, "", "in"); !slices.Equal(got, []string{"FC-A-2"}) {
		t.Errorf("money in: %v", got)
	}
	if got := e.openSources(t, e.ctx, "2026-10", "out"); !slices.Equal(got, []string{"ND-1"}) {
		t.Errorf("money out in October: %v", got)
	}
	if got := e.openSources(t, e.ctx, "2026-09", ""); len(got) != 0 {
		t.Errorf("September: %v", got)
	}
	if got := e.openSources(t, e.ctxB, "", ""); len(got) != 0 {
		t.Errorf("tenant b sees a's items: %v", got)
	}

	// A manual link settles the item while it is in effect.
	items, _, _ := e.store.OpenItems(e.ctx, "", "out", 10)
	if _, err := e.decideMovementAs(t, identity.RoleApprover, fee, MovementLink, "", items[0].ID.String()); err != nil {
		t.Fatal(err)
	}
	if got := e.openSources(t, e.ctx, "", ""); !slices.Equal(got, []string{"FC-A-2"}) {
		t.Errorf("after the link: %v", got)
	}
	if _, err := e.decideMovementAs(t, identity.RoleApprover, fee, MovementRevert, ""); err != nil {
		t.Fatal(err)
	}
	if got := e.openSources(t, e.ctx, "", ""); !slices.Equal(got, []string{"ND-1", "FC-A-2"}) {
		t.Errorf("after reverting the link: %v", got)
	}

	// Through the API, as a viewer: the fields the portal needs.
	e.setMembership(t, e.a, identity.RoleViewer)
	res, err := NewHandlers(e.store).ListOpenItems(e.ctx, apigen.ListOpenItemsRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	got := res.(apigen.ListOpenItems200JSONResponse)
	if len(got.Items) != 2 || got.Truncated || got.Items[0].SourceId != "ND-1" || got.Items[0].Amount != -850000 ||
		got.Items[0].Confidence != 1 || got.Items[0].Refs == nil || got.Items[0].Date.Format("2006-01-02") != "2026-10-02" {
		t.Errorf("api: %+v", got)
	}

	// Same date: the tie breaks by source id, never by the random UUID.
	// Fixed ids in the opposite order make an id tie break fail every run.
	e.setMembership(t, e.a, identity.RoleOwner)
	if err := e.d.InTenant(e.ctx, func(tx pgx.Tx) error {
		for _, it := range []struct{ id, source string }{
			{"ffffffff-ffff-4fff-bfff-ffffffffffff", "AA-1"},
			{"00000000-0000-4000-8000-000000000001", "ZZ-1"},
		} {
			if _, err := tx.Exec(e.ctx,
				`insert into matching.items (tenant_id, id, source, source_id, kind, date, amount, confidence)
				 values (app_current_tenant(), $1, 'erp', $2, 'invoice', $3, 123400, 1)`, it.id, it.source, day(20)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := e.openSources(t, e.ctx, "", "in"); !slices.Equal(got, []string{"FC-A-2", "AA-1", "ZZ-1"}) {
		t.Errorf("same date ties: %v; want by source id", got)
	}
}
