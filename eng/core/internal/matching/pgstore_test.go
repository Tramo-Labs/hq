package matching

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	apigen "github.com/Tramo-Labs/hq/eng/core/gen/api"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/ledger"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db/dbtest"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

func seedTenants(t *testing.T, d *db.DB) (a, b db.Scope) {
	t.Helper()
	ctx := context.Background()
	err := d.System(ctx, func(tx pgx.Tx) error {
		for i, s := range []*db.Scope{&a, &b} {
			s.TenantID, s.UserID = uuid.New(), uuid.New()
			slug := []string{"alfa", "beta"}[i]
			if _, err := tx.Exec(ctx, `insert into tenants (id, slug, name) values ($1, $2, $2)`, s.TenantID, slug); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `insert into users (id, email) values ($1, $2)`, s.UserID, slug+"@example.test"); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `insert into memberships (user_id, tenant_id, role) values ($1, $2, 'operator')`, s.UserID, s.TenantID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return a, b
}

// session is what the API middleware builds from a verified access token:
// the principal (user, tenant, role) and the database scope.
func session(sc db.Scope, role string) context.Context {
	return identity.WithPrincipal(context.Background(), identity.Principal{UserID: sc.UserID, TenantID: sc.TenantID, Role: role})
}

type pgEnv struct {
	d         *db.DB
	store     *PGStore
	a, b      db.Scope
	ctx, ctxB context.Context // operators of tenant a and tenant b
	from, to  time.Time
}

// newPGEnv loads the synthetic Bancor October statement for tenant a and
// three items: an echeq by number, a customer transfer by amount and date,
// and an invoice a few pesos off a deposit.
func newPGEnv(t *testing.T) pgEnv {
	d := dbtest.New(t)
	a, b := seedTenants(t, d)
	e := pgEnv{d: d, store: &PGStore{DB: d}, a: a, b: b,
		ctx: session(a, identity.RoleOperator), ctxB: session(b, identity.RoleOperator), from: day(1), to: day(31)}
	ls := &ledger.PGStore{DB: d}
	acct, err := ls.CreateAccount(e.ctx, ledger.Bancor, "", "Cuenta")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../ledger/testdata/bancor_ok.csv")
	if err != nil {
		t.Fatal(err)
	}
	r, err := (&ledger.Ingester{Store: ls}).Ingest(e.ctx, ledger.Upload{TenantID: a.TenantID.String(),
		AccountID: acct.String(), Bank: ledger.Bancor, Data: data})
	if err != nil || r.Status != ledger.Accepted {
		t.Fatalf("%v %+v", err, r)
	}
	if _, err := e.store.UpsertItems(e.ctx, "erp", []ItemIn{
		{SourceID: "CH-12345", Kind: "payment", Date: day(1), Amount: 31025075, Refs: []string{"CH 00012345"}, Confidence: 1},
		{SourceID: "FC-A-1", Kind: "invoice", Date: day(1), Amount: 25000000, Confidence: 1},
		{SourceID: "FC-A-2", Kind: "invoice", Date: day(6), Amount: 4499500, Confidence: 1},
	}); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e pgEnv) run(t *testing.T) (Result, int) {
	t.Helper()
	in, err := e.store.LoadInput(e.ctx, e.from, e.to, DefaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	r := Run(DefaultConfig, in)
	_, n, err := e.store.SaveRun(e.ctx, "2026-10", DefaultConfig, r)
	if err != nil {
		t.Fatal(err)
	}
	return r, n
}

func (e pgEnv) count(t *testing.T, ctx context.Context, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.d.InTenant(ctx, func(tx pgx.Tx) error { return tx.QueryRow(ctx, q, args...).Scan(&n) }); err != nil {
		t.Fatal(err)
	}
	return n
}

// Spec: running the same period twice creates no new matches.
func TestPGRunTwiceCreatesNothingNew(t *testing.T) {
	e := newPGEnv(t)
	r, n := e.run(t)
	if len(r.Matches) != 3 || n != 3 {
		t.Fatalf("first run: %d matches, %d new", len(r.Matches), n)
	}
	rules := map[Rule]Tray{}
	for _, m := range r.Matches {
		rules[m.Rule] = m.Tray
	}
	if rules[RuleReference] != Auto || rules[RuleAmountDate] != Auto || rules[RuleTolerance] != Suggested {
		t.Errorf("rules %v", rules)
	}
	// Second run: automatic matches are settled; the suggested one is found
	// again with the same ID and is not stored twice.
	r2, n2 := e.run(t)
	if n2 != 0 {
		t.Errorf("second run stored %d new matches", n2)
	}
	if len(r2.Matches) != 1 || r2.Matches[0].Rule != RuleTolerance {
		t.Errorf("second run proposed %+v", r2.Matches)
	}
	if n := e.count(t, e.ctx, `select count(*) from matching.matches`); n != 3 {
		t.Errorf("%d matches stored", n)
	}
}

func suggested(r Result) Match {
	for _, m := range r.Matches {
		if m.Tray == Suggested {
			return m
		}
	}
	return Match{}
}

func auto(r Result) Match {
	for _, m := range r.Matches {
		if m.Tray == Auto {
			return m
		}
	}
	return Match{}
}

// Spec: a decision is not edited or deleted, and the same decision sent
// twice is applied once.
func TestPGDecisions(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	m := suggested(r)
	key := uuid.NewString()
	d, applied, err := e.store.Record(e.ctx, DecisionInput{Key: key, Match: m, Action: Approve, Note: "ok"})
	if err != nil || !applied || d.At.IsZero() {
		t.Fatalf("%v %v %+v", err, applied, d)
	}
	again, applied, err := e.store.Record(e.ctx, DecisionInput{Key: key, Match: m, Action: Approve})
	if err != nil || applied || !again.At.Equal(d.At) {
		t.Errorf("replay: %v %v", err, applied)
	}
	if _, _, err := e.store.Record(e.ctx, DecisionInput{Key: uuid.NewString(), Match: m, Action: Reject, Note: "x"}); !errors.Is(err, ErrAlreadyDecided) {
		t.Errorf("second decision: %v", err)
	}
	if _, _, err := e.store.Record(e.ctx, DecisionInput{Key: key, Match: auto(r), Action: Approve}); !errors.Is(err, ErrKeyReused) {
		t.Errorf("key reused for another match: %v", err)
	}
	// Approved: the next run treats it as settled and proposes nothing.
	r2, n2 := e.run(t)
	if len(r2.Matches) != 0 || n2 != 0 {
		t.Errorf("after approval: %+v", r2.Matches)
	}
	if n := e.count(t, e.ctx, `select count(*) from audit_log where action = 'matching.approve'`); n != 1 {
		t.Errorf("%d audit entries for the approval", n)
	}
}

// The append only trigger holds by itself: through the owner connection,
// which has every privilege and is a superuser here (so RLS does not stop
// it either), UPDATE and DELETE still fail, and they fail on the trigger.
func TestPGDecisionsTriggerStopsTheOwner(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	if _, _, err := e.store.Record(e.ctx, DecisionInput{Key: uuid.NewString(), Match: suggested(r), Action: Approve}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, q := range []string{`update matching.decisions set note = 'edited'`, `delete from matching.decisions`} {
		_, err := e.d.Pool.Exec(ctx, q)
		if err == nil || !strings.Contains(err.Error(), "append only") {
			t.Errorf("%q through the owner: %v; want the trigger to reject it", q, err)
		}
	}
	var n int
	if err := e.d.Pool.QueryRow(ctx, `select count(*) from matching.decisions`).Scan(&n); err != nil || n != 1 {
		t.Errorf("decisions after the attempts: %d (%v)", n, err)
	}
	// And for tramo_app, UPDATE and DELETE are not even granted.
	for _, q := range []string{`update matching.decisions set note = 'x'`, `delete from matching.decisions`} {
		err := e.d.InTenant(e.ctx, func(tx pgx.Tx) error { _, err := tx.Exec(e.ctx, q); return err })
		if err == nil {
			t.Errorf("%q succeeded for tramo_app", q)
		}
	}
}

// The decider and the role stored are the session's, whatever the caller
// tries; there is no input for them.
func TestPGDeciderIsTheSession(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	got, _, err := e.store.Record(e.ctx, DecisionInput{Key: uuid.NewString(), Match: suggested(r), Action: Approve})
	if err != nil {
		t.Fatal(err)
	}
	var user uuid.UUID
	var role string
	if err := e.d.InTenant(e.ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(e.ctx, `select user_id, role from matching.decisions`).Scan(&user, &role)
	}); err != nil {
		t.Fatal(err)
	}
	if user != e.a.UserID || role != identity.RoleOperator || got.By != e.a.UserID.String() {
		t.Errorf("stored user %s role %s; want the session's %s operator", user, role, e.a.UserID)
	}
	// A session whose user is not a member of the tenant cannot decide, even
	// with an operator claim.
	stranger := session(db.Scope{TenantID: e.a.TenantID, UserID: uuid.New()}, identity.RoleOperator)
	if _, _, err := e.store.Record(stranger, DecisionInput{Key: uuid.NewString(), Match: auto(r), Action: Approve}); !errors.Is(err, ErrForbidden) {
		t.Errorf("non member decided: %v", err)
	}
}

// setMembership changes the stored role of a user, as an owner would from
// the members screen.
func (e pgEnv) setMembership(t *testing.T, sc db.Scope, role string) {
	t.Helper()
	ctx := context.Background()
	if err := e.d.System(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `update memberships set role = $1 where user_id = $2 and tenant_id = $3`, role, sc.UserID, sc.TenantID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// decideAs sets the membership of tenant a's user to role and records a
// decision with the operator token of the env: the role that counts is
// always the membership's.
func (e pgEnv) decideAs(t *testing.T, role string, m Match, a Action, note string) (Decision, error) {
	t.Helper()
	e.setMembership(t, e.a, role)
	d, _, err := e.store.Record(e.ctx, DecisionInput{Key: uuid.NewString(), Match: m, Action: a, Note: note})
	return d, err
}

// escalated returns the suggested match after an operator sent it to the
// Exceptions tray.
func (e pgEnv) escalated(t *testing.T, r Result) Match {
	t.Helper()
	m := suggested(r)
	if d, err := e.decideAs(t, identity.RoleOperator, m, Escalate, "client has to confirm"); err != nil || d.Tray != Suggested {
		t.Fatalf("escalate: %v %+v", err, d)
	}
	return m
}

func TestCanDecideTable(t *testing.T) {
	want := map[Tray][]string{
		Suggested: {identity.RoleOperator},
		Exception: {identity.RoleOwner, identity.RoleApprover},
		Auto:      nil,
	}
	all := []string{identity.RoleOwner, identity.RoleApprover, identity.RoleViewer, identity.RoleAccountant, identity.RoleOperator}
	for tray, allowed := range want {
		for _, role := range all {
			ok := false
			for _, a := range allowed {
				ok = ok || a == role
			}
			if CanDecide(role, tray) != ok {
				t.Errorf("CanDecide(%s, %s) = %v; want %v", role, tray, !ok, ok)
			}
		}
	}
}

// Spec sections 1 and 5, Suggested: an operator decides; an approver cannot.
func TestPGSuggestedIsTheOperators(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	for _, role := range []string{identity.RoleOwner, identity.RoleApprover, identity.RoleViewer, identity.RoleAccountant} {
		if _, err := e.decideAs(t, role, suggested(r), Approve, ""); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s decided a suggested match: %v", role, err)
		}
	}
	if _, err := e.decideAs(t, identity.RoleOperator, suggested(r), Other, "x"); !errors.Is(err, ErrActionNotInTray) {
		t.Errorf("other in Suggested: %v", err)
	}
	if d, err := e.decideAs(t, identity.RoleOperator, suggested(r), Approve, ""); err != nil || d.Tray != Suggested || d.Role != identity.RoleOperator {
		t.Errorf("operator: %v %+v", err, d)
	}
}

// Spec sections 1 and 5, Exceptions: the client (owner or approver) decides
// what an operator escalated; the operator cannot close it.
func TestPGExceptionsAreTheClients(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	m := e.escalated(t, r)
	for _, role := range []string{identity.RoleOperator, identity.RoleViewer, identity.RoleAccountant} {
		if _, err := e.decideAs(t, role, m, Approve, ""); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s decided an exception: %v", role, err)
		}
	}
	if _, err := e.decideAs(t, identity.RoleApprover, m, Escalate, ""); !errors.Is(err, ErrActionNotInTray) {
		t.Errorf("escalate in Exceptions: %v", err)
	}
	if _, err := e.decideAs(t, identity.RoleApprover, m, Other, ""); !errors.Is(err, ErrNoteRequired) {
		t.Errorf("other without a note: %v", err)
	}
	d, err := e.decideAs(t, identity.RoleApprover, m, Approve, "")
	if err != nil || d.Tray != Exception || d.Role != identity.RoleApprover {
		t.Fatalf("approver: %v %+v", err, d)
	}
	if _, err := e.decideAs(t, identity.RoleOwner, m, Reject, "x"); !errors.Is(err, ErrAlreadyDecided) {
		t.Errorf("second client decision: %v", err)
	}
	// Approved by the client: settled for the next run.
	if r2, _ := e.run(t); len(r2.Matches) != 0 {
		t.Errorf("approved exception proposed again: %+v", r2.Matches)
	}
}

// The caller's copy of the match says nothing about its tray: Record
// decides by the stored tray (plus any escalation) whatever Tray it is sent.
func TestPGTrayIsTheStoredOne(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	forged := func(m Match, tray Tray) Match { m.Tray = tray; return m }

	// Automatic sent as Suggested: still nobody's.
	if _, err := e.decideAs(t, identity.RoleOperator, forged(auto(r), Suggested), Approve, ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("automatic match sent as suggested: %v", err)
	}
	// Suggested sent as Exception: the approver still cannot, the operator can.
	if _, err := e.decideAs(t, identity.RoleApprover, forged(suggested(r), Exception), Approve, ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("suggested match sent as exception, approver: %v", err)
	}
	if _, err := e.decideAs(t, identity.RoleOperator, forged(suggested(r), Exception), Escalate, "check"); err != nil {
		t.Fatalf("operator escalates the stored suggested match: %v", err)
	}
	// Escalated, sent as Suggested: it is the client's now.
	if _, err := e.decideAs(t, identity.RoleOperator, forged(suggested(r), Suggested), Approve, ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("escalated match sent as suggested, operator: %v", err)
	}
	if d, err := e.decideAs(t, identity.RoleApprover, forged(suggested(r), Suggested), Approve, ""); err != nil || d.Tray != Exception {
		t.Errorf("escalated match sent as suggested, approver: %v %+v", err, d)
	}
}

// Nobody decides on an automatic match.
func TestPGAutomaticIsNotDecided(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	for _, role := range []string{identity.RoleOwner, identity.RoleApprover, identity.RoleOperator} {
		if _, err := e.decideAs(t, role, auto(r), Reject, ""); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s decided an automatic match: %v", role, err)
		}
	}
}

// Escalated and not yet decided by the client: neither settled nor turned
// down, it waits in Exceptions. "Other" from the client turns it down.
func TestPGEscalatedWaitsAndOtherTurnsDown(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	m := e.escalated(t, r)
	in, err := e.store.LoadInput(e.ctx, e.from, e.to, DefaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range append(in.Settled, in.Rejected...) {
		if s.ID == m.ID && s.Tray != Auto {
			t.Fatalf("escalated match counted as settled or rejected")
		}
	}
	if list, err := e.store.Undecided(e.ctx, Exception, 10); err != nil || len(list) != 1 || list[0].ID != m.ID {
		t.Errorf("exception tray: %v %+v", err, list)
	}
	if list, _ := e.store.Undecided(e.ctx, Suggested, 10); len(list) != 0 {
		t.Errorf("still in suggested: %+v", list)
	}
	if _, err := e.decideAs(t, identity.RoleApprover, m, Other, "es un adelanto de otro cliente"); err != nil {
		t.Fatal(err)
	}
	if r2, _ := e.run(t); len(r2.Matches) != 0 {
		t.Errorf("turned down match proposed again: %+v", r2.Matches)
	}
}

// A role lowered while the access token is still valid takes effect at
// once, in each tray: the membership says viewer and the decision is
// refused; restoring the membership lets the same token decide.
func TestPGDowngradedRoleCannotDecide(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	if sc, _ := db.ScopeFrom(e.ctx); sc.Role != identity.RoleOperator {
		t.Fatalf("the token should say operator, says %q", sc.Role)
	}
	// Suggested: was operator, now viewer.
	if _, err := e.decideAs(t, identity.RoleViewer, suggested(r), Escalate, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("downgraded operator decided a suggested match: %v", err)
	}
	m := e.escalated(t, r)
	// Exceptions: was approver, now viewer.
	e.setMembership(t, e.a, identity.RoleApprover)
	if _, err := e.decideAs(t, identity.RoleViewer, m, Approve, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("downgraded approver decided an exception: %v", err)
	}
	if _, err := e.decideAs(t, identity.RoleApprover, m, Approve, ""); err != nil {
		t.Errorf("restored approver: %v", err)
	}
}

func TestPGRejectedIsNotProposedAgain(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	if _, _, err := e.store.Record(e.ctx, DecisionInput{Key: uuid.NewString(), Match: suggested(r), Action: Reject,
		Note: "not this invoice"}); err != nil {
		t.Fatal(err)
	}
	r2, _ := e.run(t)
	if len(r2.Matches) != 0 {
		t.Errorf("rejected match proposed again: %+v", r2.Matches)
	}
}

func TestPGDecisionNeedsAPerson(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	job := db.WithScope(context.Background(), db.Scope{TenantID: e.a.TenantID})
	if _, _, err := e.store.Record(job, DecisionInput{Key: uuid.NewString(), Match: suggested(r), Action: Approve}); !errors.Is(err, ErrNoUser) {
		t.Errorf("decision without a user: %v", err)
	}
}

// Every write of the store leaves an audit entry, item loads included.
func TestPGItemLoadsAreAudited(t *testing.T) {
	e := newPGEnv(t)
	if n := e.count(t, e.ctx, `select count(*) from audit_log where action = 'matching.items_loaded'`); n != 1 {
		t.Errorf("%d audit entries for the item load", n)
	}
}

// Spec: a tenant does not see another tenant's matches.
func TestPGTenantIsolation(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	// Decisions of A on a match and on a movement, so the decision tables
	// and the state views have rows that could leak.
	if _, err := e.decideAs(t, identity.RoleOperator, suggested(r), Approve, ""); err != nil {
		t.Fatal(err)
	}
	fee := e.movementsByDescription(t, "COMISION MANTENIMIENTO CUENTA")[0]
	if _, err := e.decideMovementAs(t, identity.RoleApprover, fee, MovementOther, "gasto"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"matching.items", "matching.runs", "matching.matches", "matching.pending",
		"matching.decisions", "matching.movement_decisions", "matching.decision_state",
		"matching.movement_decision_state", "matching.matches_in_effect", "matching.waiting", "ledger.movements"} {
		if n := e.count(t, e.ctx, "select count(*) from "+table); n == 0 {
			t.Errorf("tenant a sees no rows of %s: nothing to leak, the check proves nothing", table)
		}
		if n := e.count(t, e.ctxB, "select count(*) from "+table); n != 0 {
			t.Errorf("tenant b sees %d rows of %s", n, table)
		}
	}
	in, err := e.store.LoadInput(e.ctxB, e.from, e.to, DefaultConfig)
	if err != nil || len(in.Movements)+len(in.Items)+len(in.Settled) != 0 {
		t.Errorf("tenant b input %+v %v", in, err)
	}
	// B cannot decide on A's match: it does not exist for B.
	if _, _, err := e.store.Record(e.ctxB, DecisionInput{Key: uuid.NewString(), Match: suggested(r), Action: Approve}); !errors.Is(err, ErrInvalid) {
		t.Errorf("tenant b decided on a's match: %v", err)
	}
	// Nor write rows for A.
	err = e.d.InTenant(e.ctxB, func(tx pgx.Tx) error {
		_, err := tx.Exec(e.ctxB, `insert into matching.items (tenant_id, source, source_id, kind, date, amount, confidence)
			values ($1, 'erp', 'x', 'invoice', '2026-10-01', 1, 1)`, e.a.TenantID)
		return err
	})
	if err == nil {
		t.Error("tenant b wrote an item for tenant a")
	}
}

// An escalated match is stored as suggested, but the client's Exceptions
// listing must say where it waits now: tray "exception".
// Rejecting says why, in both trays (product spec section 4): the operator
// in Suggested, the client in Exceptions. A blank note is no note. The
// reason is stored with the decision.
func TestPGRejectRequiresANote(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	sug := suggested(r)
	for _, note := range []string{"", "   ", "\u200b", "\u200b\u202e"} { // F28: invisible is no reason
		if _, err := e.decideAs(t, identity.RoleOperator, sug, Reject, note); !errors.Is(err, ErrNoteRequired) {
			t.Errorf("operator reject with note %q: %v", note, err)
		}
	}
	if d, err := e.decideAs(t, identity.RoleOperator, sug, Reject, "the invoice is of another client"); err != nil ||
		d.Note != "the invoice is of another client" || d.Tray != Suggested {
		t.Fatalf("operator reject: %v %+v", err, d)
	}

	e2 := newPGEnv(t)
	r2, _ := e2.run(t)
	m := e2.escalated(t, r2)
	if _, err := e2.decideAs(t, identity.RoleApprover, m, Reject, ""); !errors.Is(err, ErrNoteRequired) {
		t.Errorf("client reject without a note: %v", err)
	}
	if d, err := e2.decideAs(t, identity.RoleApprover, m, Reject, "no es de este proveedor"); err != nil || d.Tray != Exception {
		t.Fatalf("client reject: %v %+v", err, d)
	}
	var stored string
	if err := e2.d.InTenant(e2.ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(e2.ctx, `select note from matching.decisions where match_id = $1 and action = 'reject'`, m.ID).Scan(&stored)
	}); err != nil || stored != "no es de este proveedor" {
		t.Errorf("stored reason %q (%v)", stored, err)
	}
}

// Every decision problem has a fixed detail: whatever text the error
// carries (ids, input, internals) never reaches the client.
func TestDecisionProblemDetailsAreFixed(t *testing.T) {
	const leak = "SECRET 7c8911c0 select * from"
	for _, base := range []error{ErrForbidden, ErrNoUser, ErrAlreadyDecided, ErrNothingToRevert, ErrClosed,
		ErrNotInExceptions, ErrOperatorAnswered, ErrRevertOnMatch, ErrNotWithOperator, ErrItemTaken, ErrNoteRequired,
		ErrActionNotInTray, ErrLinkDoesNotAdd, ErrUnknownItem, ErrUnknownMovement, ErrInvalid, ErrKeyReused} {
		got := decisionProblem(fmt.Errorf("%w: %s", base, leak))
		var pe *httpx.Error
		if !errors.As(got, &pe) {
			t.Errorf("%v: not a problem: %v", base, got)
			continue
		}
		if pe.Detail == "" || strings.Contains(pe.Detail, "SECRET") || strings.Contains(pe.Detail, "matching:") {
			t.Errorf("%v: detail %q", base, pe.Detail)
		}
	}
}

func TestHTTPEscalatedListsAsException(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	m := e.escalated(t, r)
	e.setMembership(t, e.a, identity.RoleApprover)
	res, err := NewHandlers(e.store).ListMatches(e.ctx, apigen.ListMatchesRequestObject{
		Params: apigen.ListMatchesParams{Tray: "exception"}})
	if err != nil {
		t.Fatal(err)
	}
	items := res.(apigen.ListMatches200JSONResponse).Items
	if len(items) != 1 || items[0].Id != m.ID || items[0].Tray != "exception" {
		t.Fatalf("escalated match as the approver lists it: %+v", items)
	}
}
