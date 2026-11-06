package matching

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
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
	if _, ok := e.pendingIDs(t, Exception)[fee]; !ok {
		t.Fatal("the fee should be an exception without candidate")
	}
	if _, err := e.decideMovementAs(t, identity.RoleApprover, fee, MovementOther, ""); !errors.Is(err, ErrActionNotInTray) {
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
			_, _, err := e.store.Record(e.ctx, DecisionInput{Key: uuid.NewString(), Match: m, Action: a})
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
