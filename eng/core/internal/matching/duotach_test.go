package matching

// The six acceptance cases proposed by Duotach in "Automatizar conciliación
// bancaria: qué contratar" (duotach.com/blog/automatizar-conciliacion-bancaria,
// 15/10/2026), section "La prueba de aceptación que conviene pedir". Each test
// quotes the expected result the post asks for. Cases 4 and 6 are about the
// input, so they run through the ledger.

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/ledger"
	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

func day(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }

func inv(id string, d int, a money.Amount, refs ...string) Item {
	return Item{ID: id, Kind: "invoice", Date: day(d), Amount: a, Refs: refs, Confidence: 1}
}

// Case 1, "Mismo importe y fecha, referencias distintas": "No cerrar una
// relación ambigua sin evidencia adicional".
func TestDuotach1SameAmountSameDate(t *testing.T) {
	in := Input{
		Movements: []Movement{
			{ID: "m1", Date: day(10), Amount: 5000000, Description: "TRANSF RECIBIDA", Reference: "77001"},
			{ID: "m2", Date: day(10), Amount: 5000000, Description: "TRANSF RECIBIDA", Reference: "77002"},
		},
		Items: []Item{inv("a", 9, 5000000), inv("b", 9, 5000000)},
	}
	r := Run(DefaultConfig, in)
	if len(r.Matches) != 0 {
		t.Fatalf("closed an ambiguous relation: %+v", r.Matches)
	}
	if len(r.Pending) != 2 {
		t.Fatalf("pending %+v", r.Pending)
	}
	for _, p := range r.Pending {
		if p.Reason != ReasonAmbiguous || p.Tray != Suggested || len(p.Candidates) != 2 {
			t.Errorf("pending %+v: want ambiguous, suggested tray, both candidates", p)
		}
	}
	// With additional verifiable evidence (the invoice number in one
	// transfer) both resolve: one by reference, the other is then unique.
	in.Movements[0].Description = "TRANSF RECIBIDA FC 0001-00000077"
	in.Items[0].Refs = []string{"0001-00000077"}
	r = Run(DefaultConfig, in)
	if len(r.Matches) != 2 || r.Matches[0].Rule != RuleReference && r.Matches[1].Rule != RuleReference {
		t.Fatalf("matches %+v", r.Matches)
	}
}

// Case 2, "Un depósito que agrupa varias partidas": "Mostrar cada vínculo y
// la regla de agrupación, sin reutilizar partidas".
func TestDuotach2GroupedDeposit(t *testing.T) {
	in := Input{
		Movements: []Movement{
			{ID: "dep", Date: day(12), Amount: 6000000, Description: "DEPOSITO CLIENTE NORTE"},
			{ID: "dep2", Date: day(12), Amount: 2500002, Description: "DEPOSITO CLIENTE NORTE"},
		},
		Items: []Item{
			{ID: "f1", Date: day(10), Amount: 1000000, Confidence: 1, Party: "CLIENTE NORTE"},
			{ID: "f2", Date: day(10), Amount: 2000000, Confidence: 1, Party: "CLIENTE NORTE"},
			{ID: "f3", Date: day(11), Amount: 3000000, Confidence: 1, Party: "CLIENTE NORTE"},
			{ID: "f4", Date: day(11), Amount: 1500000, Confidence: 1, Party: "CLIENTE NORTE"},
			{ID: "f5", Date: day(11), Amount: 1000000 + 1, Confidence: 1, Party: "CLIENTE NORTE"},
		},
	}
	r := Run(DefaultConfig, in)
	used := map[string]string{}
	for _, m := range r.Matches {
		if m.Rule != RuleManyItems || m.Tray != Suggested || len(m.ItemIDs) < 2 {
			t.Errorf("match %+v: want grouping rule, suggested, each item listed", m)
		}
		for _, id := range m.ItemIDs {
			if prev, ok := used[id]; ok {
				t.Fatalf("item %s reused by %s and %s", id, prev, m.MovementIDs[0])
			}
			used[id] = m.MovementIDs[0]
		}
	}
	// dep = f1+f2+f3 (60.000) is the only subset; nothing adds up to dep2
	// (25.000,02), so it gets no combination.
	if len(r.Matches) != 1 || !slices.Equal(r.Matches[0].ItemIDs, []string{"f1", "f2", "f3"}) {
		t.Fatalf("matches %+v", r.Matches)
	}
	if r.Matches[0].Reference != "CLIENTE NORTE" {
		t.Errorf("grouping evidence %q", r.Matches[0].Reference)
	}

	// Now dep2 is 25.000 = f1+f4: both deposits claim f1. Neither is
	// matched; both wait with their candidate.
	in.Movements[1].Amount = 2500000
	r = Run(DefaultConfig, in)
	if len(r.Matches) != 0 || len(r.Pending) != 2 {
		t.Fatalf("an item was claimed twice: matches %+v pending %+v", r.Matches, r.Pending)
	}
}

// Two combinations that both close are a choice the engine does not make.
func TestDuotach2TwoCombinationsArePending(t *testing.T) {
	in := Input{
		Movements: []Movement{{ID: "dep", Date: day(12), Amount: 3000000, Description: "DEPOSITO"}},
		Items:     []Item{inv("a", 11, 1000000), inv("b", 11, 2000000), inv("c", 11, 1500000), inv("d", 11, 1500000)},
	}
	r := Run(DefaultConfig, in)
	if len(r.Matches) != 0 || len(r.Pending) != 1 || r.Pending[0].Reason != ReasonComboAmbiguous {
		t.Fatalf("matches %+v pending %+v", r.Matches, r.Pending)
	}
}

// Case 3, "Importe neto con comisión documentada": "Explicar la diferencia
// con el documento o regla aprobada; no inventar un ajuste".
func TestDuotach3NetOfDocumentedFee(t *testing.T) {
	gross := money.Amount(10000000)
	in := Input{
		Movements: []Movement{{ID: "mp", Date: day(5), Amount: 9470000, Description: "payment",
			ExternalRef: "PEDIDO-77", Gross: &gross,
			Fees: []Fee{{"mp_fee", -410000}, {"taxes", -120000}}}},
		Items: []Item{inv("order", 5, 10000000, "PEDIDO-77")},
	}
	r := Run(DefaultConfig, in)
	if len(r.Matches) != 1 {
		t.Fatalf("matches %+v pending %+v", r.Matches, r.Pending)
	}
	m := r.Matches[0]
	if m.Rule != RuleDocumented || m.Tray != Suggested || len(m.Adjustments) != 2 {
		t.Fatalf("match %+v", m)
	}
	var sum money.Amount
	for _, a := range m.Adjustments {
		if a.Source != "documented_by_source" {
			t.Errorf("adjustment %+v not from the document", a)
		}
		sum += a.Amount
	}
	if sum != 9470000-10000000 {
		t.Errorf("adjustments add to %s", sum)
	}

	// The same difference without documentation: no adjustment is invented.
	in.Movements[0].Gross, in.Movements[0].Fees = nil, nil
	r = Run(DefaultConfig, in)
	if len(r.Matches) != 0 {
		t.Fatalf("invented an adjustment: %+v", r.Matches)
	}
	if len(r.Pending) != 1 || r.Pending[0].Reason != ReasonRefAmountDiffers {
		t.Errorf("pending %+v: want the referenced order as a candidate", r.Pending)
	}
}

// Case 4, "Reimportar el mismo archivo": "No crear ni aprobar movimientos
// duplicados". The ledger refuses the file; and a rerun of matching over the
// same movements creates no new match.
func TestDuotach4ReimportSameFile(t *testing.T) {
	data, err := os.ReadFile("../ledger/testdata/bancor_ok.csv")
	if err != nil {
		t.Fatal(err)
	}
	store := ledger.NewMemStore()
	ing := &ledger.Ingester{Store: store}
	up := ledger.Upload{TenantID: "t", AccountID: "acc", Bank: ledger.Bancor, Data: data}
	first, err := ing.Ingest(context.Background(), up)
	if err != nil || first.Status != ledger.Accepted {
		t.Fatalf("%v %+v", err, first)
	}
	again, err := ing.Ingest(context.Background(), up)
	if err != nil || again.Status != ledger.Duplicate || again.New != 0 {
		t.Fatalf("%v %+v", err, again)
	}
	var moves []Movement
	for _, m := range store.Movements("t", "acc") {
		moves = append(moves, Movement{ID: m.Key, Date: m.Date, Amount: m.Amount, Description: m.Description, Reference: m.Reference})
	}
	items := []Item{inv("cheque", 3, 31025075, "CH 00012345"), inv("cli1", 1, 25000000)}
	run1 := Run(DefaultConfig, Input{Movements: moves, Items: items})
	if len(run1.Matches) != 2 {
		t.Fatalf("matches %+v", run1.Matches)
	}
	run2 := Run(DefaultConfig, Input{Movements: moves, Items: items, Settled: run1.Matches})
	if len(run2.Matches) != 0 {
		t.Errorf("rerun created %d matches", len(run2.Matches))
	}
}

// Case 5, "Partida del cierre anterior que aparece después": "Conservar su
// origen y el estado entre períodos".
func TestDuotach5ItemFromPreviousClose(t *testing.T) {
	sept := Item{ID: "chq", Kind: "payment", Date: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC),
		Amount: -8000000, Refs: []string{"CH 44556677"}, Confidence: 1}
	in := Input{
		Movements: []Movement{{ID: "m", Date: day(14), Amount: -8000000, Description: "CHEQUE 44556677 DEBITADO"}},
		Items:     []Item{sept},
	}
	r := Run(DefaultConfig, in)
	if len(r.Matches) != 1 {
		t.Fatalf("matches %+v", r.Matches)
	}
	m := r.Matches[0]
	if !m.CrossPeriod || !slices.Equal(m.ItemPeriods, []string{"2026-09"}) || !slices.Equal(m.MovePeriods, []string{"2026-10"}) {
		t.Errorf("periods not kept: %+v", m)
	}
	if m.DateDiffDays != 20 {
		t.Errorf("date distance %d", m.DateDiffDays)
	}
	// While it is not cashed, the September item stays open in October:
	// its status crosses the period.
	r = Run(DefaultConfig, Input{Items: []Item{sept}})
	if !slices.Equal(r.OpenItems, []string{"chq"}) {
		t.Errorf("open items %v", r.OpenItems)
	}
}

// Case 6, "Archivo incompleto o cambio de formato": "Detener o aislar la
// entrada con aviso y responsable, sin declarar conciliación completa".
// Nothing of such a file reaches matching.
func TestDuotach6IncompleteOrChangedFile(t *testing.T) {
	for _, c := range []struct {
		file   string
		bank   ledger.Bank
		reason string
	}{
		{"santander_truncated.csv", ledger.Santander, ledger.ReasonIncomplete},
		{"galicia_changed_layout.csv", ledger.Galicia, ledger.ReasonUnknownLayout},
	} {
		data, err := os.ReadFile("../ledger/testdata/" + c.file)
		if err != nil {
			t.Fatal(err)
		}
		store := ledger.NewMemStore()
		r, err := (&ledger.Ingester{Store: store}).Ingest(context.Background(),
			ledger.Upload{TenantID: "t", AccountID: "acc", Bank: c.bank, Data: data})
		if err != nil {
			t.Fatal(err)
		}
		if r.Status != ledger.Quarantined || r.Reason != c.reason || r.Detail == "" {
			t.Errorf("%s: %+v", c.file, r)
		}
		if n := len(store.Movements("t", "acc")); n != 0 {
			t.Errorf("%s: %d movements reached the ledger", c.file, n)
		}
	}
}

// Also asked in the post: reject a suggestion and process the corrected
// data; the record says what changed and who decided.
func TestDuotachRejectThenReprocess(t *testing.T) {
	in := Input{
		Movements: []Movement{{ID: "m", Date: day(8), Amount: 1000500, Description: "TRANSF"}},
		Items:     []Item{inv("x", 7, 1000000)},
	}
	r := Run(DefaultConfig, in)
	if len(r.Matches) != 1 || r.Matches[0].Rule != RuleTolerance {
		t.Fatalf("%+v", r.Matches)
	}
	book := NewBook()
	_, applied, err := book.Record(Decision{Key: "k1", Match: r.Matches[0], Action: Reject, By: "operator-1",
		Role: "operator", At: time.Date(2026, 10, 9, 15, 0, 0, 0, time.UTC), Note: "different customer"})
	if err != nil || !applied {
		t.Fatal(err)
	}
	in.Rejected = book.Rejected()
	r = Run(DefaultConfig, in)
	if len(r.Matches) != 0 {
		t.Fatalf("rejected link proposed again: %+v", r.Matches)
	}
	// The corrected item arrives: now the movement matches it exactly.
	in.Items = append(in.Items, inv("x2", 8, 1000500))
	r = Run(DefaultConfig, in)
	if len(r.Matches) != 1 || r.Matches[0].ItemIDs[0] != "x2" || r.Matches[0].Rule != RuleAmountDate {
		t.Fatalf("%+v", r.Matches)
	}
	d := book.Decisions()[0]
	if d.By != "operator-1" || d.At.IsZero() || d.Match.ItemIDs[0] != "x" {
		t.Errorf("decision record %+v", d)
	}
}
