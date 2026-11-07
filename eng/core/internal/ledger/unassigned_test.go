package ledger

import (
	"context"
	"strings"
	"testing"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
)

type fakeRouter map[Bank][]AccountRef

func (f fakeRouter) AccountsOfBank(_ context.Context, _ string, b Bank) ([]AccountRef, error) {
	return f[b], nil
}

const routeTenant = "t1"

func mailed(t *testing.T, name string) Unassigned {
	return Unassigned{TenantID: routeTenant, Filename: name, Data: fixture(t, name)}
}

// The routing rule, case by case: what each file and set of accounts gives.
func TestIngestUnassignedRoutes(t *testing.T) {
	ctx := context.Background()
	santander := []AccountRef{{ID: "s1", Number: "000-000001/0", Currency: "ARS"}}
	cases := []struct {
		name    string
		file    string
		router  fakeRouter
		status  Status
		reason  string
		account string
		routed  string
	}{
		{"number stated, one account has it", "santander_ok.csv", fakeRouter{Santander: santander},
			Accepted, "", "s1", RoutedByNumber},
		{"number stated, a second account of the bank does not matter", "santander_ok.csv",
			fakeRouter{Santander: append(append([]AccountRef{}, santander...), AccountRef{ID: "s2", Number: "000-000009/9"})},
			Accepted, "", "s1", RoutedByNumber},
		{"number stated, no account has it", "santander_ok.csv",
			fakeRouter{Santander: {{ID: "s2", Number: "000-000009/9", HasStatement: true}}},
			Quarantined, ReasonUnrouted, "", ""},
		{"number stated, two accounts have it", "santander_ok.csv",
			fakeRouter{Santander: {{ID: "s1", Number: "000-000001/0"}, {ID: "s3", Number: "0000000010"}}},
			Quarantined, ReasonUnrouted, "", ""},
		{"no number, no account of the bank", "bancor_ok.csv", fakeRouter{},
			Quarantined, ReasonUnrouted, "", ""},
		{"no number, the only account has no statement yet", "bancor_ok.csv",
			fakeRouter{Bancor: {{ID: "b1"}}},
			Quarantined, ReasonUnrouted, "", ""},
		{"no number, two accounts of the bank", "bancor_ok.csv",
			fakeRouter{Bancor: {{ID: "b1", HasStatement: true}, {ID: "b2", HasStatement: true}}},
			Quarantined, ReasonUnrouted, "", ""},
		{"no number, the only account has a statement", "bancor_ok.csv",
			fakeRouter{Bancor: {{ID: "b1", HasStatement: true}}},
			Accepted, "", "b1", RoutedSingleAccount},
		{"not readable keeps its reading reason", "galicia_changed_layout.csv",
			fakeRouter{Galicia: {{ID: "g1", HasStatement: true}}},
			Quarantined, ReasonUnknownLayout, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := NewMemStore()
			r, err := (&Ingester{Store: s}).IngestUnassigned(ctx, c.router, mailed(t, c.file))
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != c.status || r.Reason != c.reason || r.AccountID != c.account || r.Routed != c.routed {
				t.Fatalf("%s %s (%s) account %q routed %q; want %s %s account %q routed %q",
					r.Status, r.Reason, r.Detail, r.AccountID, r.Routed, c.status, c.reason, c.account, c.routed)
			}
			if c.reason == ReasonUnrouted && (strings.Contains(r.Detail, "000") || r.Detail == "") {
				t.Errorf("detail %q: it says why, never with a number", r.Detail)
			}
		})
	}
}

// After routing, every check of an upload runs: a mailed file that only
// repeats an accepted one is a duplicate, and one that does not chain
// with the account's last statement is quarantined on that account.
func TestIngestUnassignedRunsTheChecks(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	in := &Ingester{Store: s}
	// The first statement is uploaded by hand.
	if r, err := in.Ingest(ctx, Upload{TenantID: routeTenant, AccountID: "b1", Bank: Bancor, Filename: "oct.csv",
		Data: fixture(t, "bancor_ok.csv")}); err != nil || r.Status != Accepted {
		t.Fatalf("%v %+v", err, r)
	}
	router := fakeRouter{Bancor: {{ID: "b1", HasStatement: true}}}
	if r, err := in.IngestUnassigned(ctx, router, mailed(t, "bancor_ok.csv")); err != nil || r.Status != Duplicate {
		t.Errorf("the same file by mail: %v %+v", err, r)
	}
	if r, err := in.IngestUnassigned(ctx, router, mailed(t, "bancor_nov_gap.csv")); err != nil ||
		r.Status != Quarantined || r.Reason != ReasonChainGap || r.AccountID != "b1" {
		t.Errorf("a statement that does not chain, by mail: %v %+v", err, r)
	}
	r, err := in.IngestUnassigned(ctx, router, mailed(t, "bancor_nov.csv"))
	if err != nil || r.Status != Accepted || r.AccountID != "b1" || r.Routed != RoutedSingleAccount {
		t.Fatalf("the next statement by mail: %v %+v", err, r)
	}
}

// Against Postgres, as the inbox runs it: the tenant's scope and no user.
// A file nobody could route is stored with no account and listed; once the
// account has its first statement (uploaded by hand), the next one by mail
// is routed to it. The list says how each came in and how it was routed.
func TestPGIngestUnassigned(t *testing.T) {
	e := newPGEnv(t)
	tenant := e.scope.TenantID.String()
	job := db.WithScope(context.Background(), db.Scope{TenantID: e.scope.TenantID})
	mail := func(name string) Result {
		t.Helper()
		r, err := e.in.IngestUnassigned(job, e.store, Unassigned{TenantID: tenant, Filename: name, Data: fixture(t, name)})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := mail("bancor_ok.csv"); r.Status != Quarantined || r.Reason != ReasonUnrouted || r.AccountID != "" {
		t.Fatalf("first statement by mail: %+v", r)
	}
	if r := e.upload(t, e.ctx, e.scope, e.acct, "bancor_ok.csv"); r.Status != Accepted {
		t.Fatalf("first statement by hand: %+v", r)
	}
	if r := mail("bancor_nov.csv"); r.Status != Accepted || r.AccountID != e.acct.String() || r.Routed != RoutedSingleAccount {
		t.Fatalf("next statement by mail: %+v", r)
	}

	rows, _, err := e.store.Files(e.ctx, FileFilter{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]FileRow{}
	for _, f := range rows {
		by[f.Filename+"|"+string(f.Status)] = f
	}
	unrouted, hand, routed := by["bancor_ok.csv|quarantined"], by["bancor_ok.csv|accepted"], by["bancor_nov.csv|accepted"]
	if unrouted.AccountID != nil || unrouted.AccountName != "" || unrouted.Bank != Bancor || unrouted.Source != SourceMail ||
		unrouted.Routed != "" || unrouted.Reason != ReasonUnrouted || unrouted.UploadedBy != nil {
		t.Errorf("unrouted row: %+v", unrouted)
	}
	if hand.AccountID == nil || *hand.AccountID != e.acct || hand.Source != SourceUpload || hand.Routed != "" {
		t.Errorf("row uploaded by hand: %+v", hand)
	}
	if routed.AccountID == nil || *routed.AccountID != e.acct || routed.Source != SourceMail ||
		routed.Routed != RoutedSingleAccount || routed.Movements == 0 {
		t.Errorf("routed row: %+v", routed)
	}
}

// With the currency stated by the layout, one account per bank and currency
// is not ambiguous; with it not stated, both count.
func TestRouteByCurrency(t *testing.T) {
	accounts := []AccountRef{{ID: "pesos", Currency: "ARS", HasStatement: true}, {ID: "dolares", Currency: "USD", HasStatement: true}}
	st := &Statement{Bank: Macro, Currency: "USD", CurrencyStated: true}
	if a, how, _ := route(st, accounts); a == nil || a.ID != "dolares" || how != RoutedSingleAccount {
		t.Errorf("stated USD: %+v %s", a, how)
	}
	st.CurrencyStated = false
	if a, _, why := route(st, accounts); a != nil || why == "" {
		t.Errorf("currency not stated: %+v, want unrouted", a)
	}
}
