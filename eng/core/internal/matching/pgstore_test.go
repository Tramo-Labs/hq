package matching

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/ledger"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db/dbtest"
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

type pgEnv struct {
	d         *db.DB
	store     *PGStore
	ctx, ctxB context.Context
	from, to  time.Time
}

// newPGEnv loads the synthetic Bancor October statement for tenant a and
// three items: an echeq by number, a customer transfer by amount and date,
// and an invoice a few pesos off a deposit.
func newPGEnv(t *testing.T) pgEnv {
	d := dbtest.New(t)
	a, b := seedTenants(t, d)
	e := pgEnv{d: d, store: &PGStore{DB: d}, ctx: db.WithScope(context.Background(), a), ctxB: db.WithScope(context.Background(), b),
		from: day(1), to: day(31)}
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
	var stored int
	e.d.InTenant(e.ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(e.ctx, `select count(*) from matching.matches`).Scan(&stored)
	})
	if stored != 3 {
		t.Errorf("%d matches stored", stored)
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

// Spec: a decision is not edited or deleted, and the same decision sent
// twice is applied once.
func TestPGDecisions(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	m := suggested(r)
	key := uuid.NewString()
	d, applied, err := e.store.Record(e.ctx, Decision{Key: key, Match: m, Action: Approve, Role: "operator", Note: "ok"})
	if err != nil || !applied || d.At.IsZero() || d.By == "" {
		t.Fatalf("%v %v %+v", err, applied, d)
	}
	again, applied, err := e.store.Record(e.ctx, Decision{Key: key, Match: m, Action: Approve, Role: "operator"})
	if err != nil || applied || !again.At.Equal(d.At) {
		t.Errorf("replay: %v %v", err, applied)
	}
	if _, _, err := e.store.Record(e.ctx, Decision{Key: uuid.NewString(), Match: m, Action: Reject, Role: "operator"}); !errors.Is(err, ErrAlreadyDecided) {
		t.Errorf("second decision: %v", err)
	}
	var auto Match
	for _, x := range r.Matches {
		if x.Tray == Auto {
			auto = x
		}
	}
	if _, _, err := e.store.Record(e.ctx, Decision{Key: key, Match: auto, Action: Approve}); !errors.Is(err, ErrKeyReused) {
		t.Errorf("key reused for another match: %v", err)
	}
	for _, q := range []string{`update matching.decisions set action = 'reject'`, `delete from matching.decisions`} {
		err := e.d.InTenant(e.ctx, func(tx pgx.Tx) error { _, err := tx.Exec(e.ctx, q); return err })
		if err == nil {
			t.Errorf("%q succeeded", q)
		}
	}
	// The owner cannot edit it either: the trigger stops every role.
	err = e.d.System(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `update matching.decisions set note = 'x'`)
		return err
	})
	if err == nil {
		t.Error("owner edited a decision")
	}
	// Approved: the next run treats it as settled and proposes nothing.
	r2, n2 := e.run(t)
	if len(r2.Matches) != 0 || n2 != 0 {
		t.Errorf("after approval: %+v", r2.Matches)
	}
	var audited int
	e.d.InTenant(e.ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(e.ctx, `select count(*) from audit_log where action = 'matching.approve'`).Scan(&audited)
	})
	if audited != 1 {
		t.Errorf("%d audit entries for the approval", audited)
	}
}

func TestPGRejectedIsNotProposedAgain(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	if _, _, err := e.store.Record(e.ctx, Decision{Key: uuid.NewString(), Match: suggested(r), Action: Reject, Role: "operator"}); err != nil {
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
	sc, _ := db.ScopeFrom(e.ctx)
	job := db.WithScope(context.Background(), db.Scope{TenantID: sc.TenantID})
	if _, _, err := e.store.Record(job, Decision{Key: uuid.NewString(), Match: suggested(r), Action: Approve}); !errors.Is(err, ErrNoUser) {
		t.Errorf("decision without a user: %v", err)
	}
}

// Spec: a tenant does not see another tenant's matches.
func TestPGTenantIsolation(t *testing.T) {
	e := newPGEnv(t)
	r, _ := e.run(t)
	for _, table := range []string{"matching.items", "matching.runs", "matching.matches", "matching.pending", "ledger.movements"} {
		var n int
		if err := e.d.InTenant(e.ctxB, func(tx pgx.Tx) error {
			return tx.QueryRow(e.ctxB, "select count(*) from "+table).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("tenant b sees %d rows of %s", n, table)
		}
	}
	in, err := e.store.LoadInput(e.ctxB, e.from, e.to, DefaultConfig)
	if err != nil || len(in.Movements)+len(in.Items)+len(in.Settled) != 0 {
		t.Errorf("tenant b input %+v %v", in, err)
	}
	// B cannot decide on A's match: it does not exist for B.
	if _, _, err := e.store.Record(e.ctxB, Decision{Key: uuid.NewString(), Match: suggested(r), Action: Approve}); !errors.Is(err, ErrInvalid) {
		t.Errorf("tenant b decided on a's match: %v", err)
	}
}
