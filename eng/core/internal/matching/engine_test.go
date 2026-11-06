package matching

import (
	"errors"
	"testing"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// Spec, Documents: a field with low confidence does not feed an automatic
// match. The same exact match goes to Suggested.
func TestLowConfidenceItemIsOnlySuggested(t *testing.T) {
	it := inv("doc", 5, 1234500, "0002-00000099")
	it.Confidence = 0.6
	r := Run(DefaultConfig, Input{
		Movements: []Movement{{ID: "m", Date: day(6), Amount: 1234500, Description: "PAGO FC 0002-00000099"}},
		Items:     []Item{it},
	})
	if len(r.Matches) != 1 || r.Matches[0].Tray != Suggested || !r.Matches[0].LowConfidence {
		t.Fatalf("%+v", r.Matches)
	}
	it.Confidence = 0 // unknown confidence is not trusted either
	r = Run(DefaultConfig, Input{Movements: []Movement{{ID: "m", Date: day(6), Amount: 1234500}}, Items: []Item{it}})
	if r.Matches[0].Tray != Suggested {
		t.Errorf("unknown confidence went to %s", r.Matches[0].Tray)
	}
}

// One invoice number in two transfers of the same amount is a possible
// duplicate payment: pending, not a pick.
func TestSameReferenceTwice(t *testing.T) {
	r := Run(DefaultConfig, Input{
		Movements: []Movement{
			{ID: "m1", Date: day(3), Amount: 500000, Description: "PAGO FC 0001-00000555"},
			{ID: "m2", Date: day(4), Amount: 500000, Description: "PAGO FC 0001-00000555"},
		},
		Items: []Item{inv("f", 1, 500000, "0001-00000555")},
	})
	if len(r.Matches) != 0 || len(r.Pending) != 2 {
		t.Fatalf("matches %+v pending %+v", r.Matches, r.Pending)
	}
}

// An ambiguity found by a strong pass is not resolved by a weaker one.
func TestAmbiguityIsNotResolvedByTolerance(t *testing.T) {
	r := Run(DefaultConfig, Input{
		Movements: []Movement{{ID: "m", Date: day(5), Amount: 1000000}},
		Items:     []Item{inv("a", 5, 1000000), inv("b", 5, 1000000), inv("c", 5, 999900)},
	})
	if len(r.Matches) != 0 || len(r.Pending) != 1 || r.Pending[0].Reason != ReasonAmbiguous {
		t.Fatalf("matches %+v pending %+v", r.Matches, r.Pending)
	}
}

// Same rule, no tolerance involved: two exact candidates, and two other
// items that add up to the movement. The combination pass must not settle
// what the exact pass found ambiguous.
func TestAmbiguityIsNotResolvedByCombination(t *testing.T) {
	r := Run(DefaultConfig, Input{
		Movements: []Movement{{ID: "m", Date: day(5), Amount: 1000000}},
		Items:     []Item{inv("a", 5, 1000000), inv("b", 4, 1000000), inv("c", 5, 600000), inv("d", 5, 400000)},
	})
	if len(r.Matches) != 0 {
		t.Fatalf("an ambiguous movement got matched: %+v", r.Matches)
	}
	if len(r.Pending) != 1 || r.Pending[0].Reason != ReasonAmbiguous || len(r.Pending[0].Candidates) != 2 {
		t.Fatalf("pending %+v", r.Pending)
	}
}

// Ambiguity on the item side: one invoice, two exact movements. Neither
// movement may take it through a weaker pass, nor another item through a
// combination.
func TestItemSideAmbiguityFreezesBothMovements(t *testing.T) {
	r := Run(DefaultConfig, Input{
		Movements: []Movement{
			{ID: "m1", Date: day(5), Amount: 1000000},
			{ID: "m2", Date: day(6), Amount: 1000000},
		},
		Items: []Item{inv("a", 5, 1000000), inv("c", 5, 600000), inv("d", 5, 400000)},
	})
	if len(r.Matches) != 0 || len(r.Pending) != 2 {
		t.Fatalf("matches %+v pending %+v", r.Matches, r.Pending)
	}
}

// The ambiguous item itself is frozen: a third movement a few cents off must
// not take it through the tolerance pass, nor two small movements through a
// combination, while two exact movements are still waiting on it.
func TestAmbiguousItemNotTakenByWeakerPass(t *testing.T) {
	for name, extra := range map[string][]Movement{
		"tolerance":   {{ID: "m3", Date: day(5), Amount: 1000005}},
		"combination": {{ID: "m3", Date: day(5), Amount: 600000}, {ID: "m4", Date: day(5), Amount: 400000}},
	} {
		r := Run(DefaultConfig, Input{
			Movements: append([]Movement{
				{ID: "m1", Date: day(5), Amount: 1000000},
				{ID: "m2", Date: day(6), Amount: 1000000},
			}, extra...),
			Items: []Item{inv("a", 5, 1000000)},
		})
		if len(r.Matches) != 0 {
			t.Errorf("%s: the ambiguous item was taken: %+v", name, r.Matches)
		}
	}
}

func TestToleranceLimits(t *testing.T) {
	cases := []struct {
		move, item int64
		want       bool
	}{
		{1000999, 1000000, true},   // $9,99 over
		{1001000, 1000000, true},   // exactly the cap
		{1001001, 1000000, false},  // one cent over the cap
		{-1000500, 1000000, false}, // opposite sign
	}
	for _, c := range cases {
		r := Run(DefaultConfig, Input{
			Movements: []Movement{{ID: "m", Date: day(5), Amount: moneyOf(c.move)}},
			Items:     []Item{inv("i", 5, moneyOf(c.item))},
		})
		if got := len(r.Matches) == 1; got != c.want {
			t.Errorf("%d vs %d: matched %v", c.move, c.item, got)
		}
		if c.want && (r.Matches[0].Adjustments[0].Amount != moneyOf(c.move-c.item) || r.Matches[0].Tray != Suggested) {
			t.Errorf("adjustment %+v", r.Matches[0])
		}
	}
}

func TestDateWindow(t *testing.T) {
	for _, c := range []struct {
		itemDay int
		want    bool
	}{{10, true}, {5, true}, {4, false}, {15, true}, {16, false}} {
		r := Run(DefaultConfig, Input{
			Movements: []Movement{{ID: "m", Date: day(10), Amount: 700000}},
			Items:     []Item{inv("i", c.itemDay, 700000)},
		})
		if got := len(r.Matches) == 1; got != c.want {
			t.Errorf("item on day %d: matched %v", c.itemDay, got)
		}
	}
}

func TestManyMovementsOneItem(t *testing.T) {
	r := Run(DefaultConfig, Input{
		Movements: []Movement{
			{ID: "p1", Date: day(10), Amount: 300000},
			{ID: "p2", Date: day(11), Amount: 700000},
		},
		Items: []Item{inv("big", 10, 1000000)},
	})
	if len(r.Matches) != 1 || r.Matches[0].Rule != RuleManyMoves || len(r.Matches[0].MovementIDs) != 2 || r.Matches[0].Tray != Suggested {
		t.Fatalf("%+v", r.Matches)
	}
}

func TestNoCandidateIsException(t *testing.T) {
	r := Run(DefaultConfig, Input{Movements: []Movement{{ID: "m", Date: day(10), Amount: -12300, Description: "COMISION"}}})
	if len(r.Exceptions) != 1 || r.Exceptions[0].Tray != Exception || r.Exceptions[0].Reason != ReasonNoCandidate {
		t.Fatalf("%+v", r)
	}
}

// A rejected link is not proposed again by another pass either.
func TestRejectedLinkBlockedForEveryRule(t *testing.T) {
	in := Input{
		Movements: []Movement{{ID: "m", Date: day(5), Amount: 400000, Description: "FC 0001-00000042"}},
		Items:     []Item{inv("i", 5, 400000, "0001-00000042")},
	}
	first := Run(DefaultConfig, in)
	if first.Matches[0].Rule != RuleReference {
		t.Fatal(first.Matches)
	}
	in.Rejected = first.Matches
	if r := Run(DefaultConfig, in); len(r.Matches) != 0 {
		t.Fatalf("re-proposed by %s", r.Matches[0].Rule)
	}
}

func TestReferenceTokens(t *testing.T) {
	m := Movement{Description: "PAGO FC A 0002-00001234 / CH 00012345", ExternalRef: "PEDIDO-77"}
	toks := movementTokens(m)
	for _, want := range []string{"200001234", "1234", "12345", "PEDIDO77"} {
		if !toks[want] {
			t.Errorf("missing token %s in %v", want, toks)
		}
	}
	if toks["PAGO"] || toks["CH"] {
		t.Error("words without digits must not be tokens")
	}
	it := itemTokens(Item{Refs: []string{"FC A 0002-00001234", "CH 00012345"}})
	if len(it) != 2 || it[0] != "200001234" || it[1] != "12345" {
		t.Errorf("item tokens %v", it)
	}
}

// ---- approvals ----

func sampleMatch() Match {
	m := Match{Rule: RuleTolerance, MovementIDs: []string{"m"}, ItemIDs: []string{"i"}, Tray: Suggested}
	m.ID = matchID(m.Rule, m.MovementIDs, m.ItemIDs)
	return m
}

func TestDecisionAppliedOnce(t *testing.T) {
	b := NewBook()
	d := Decision{Key: "k", Match: sampleMatch(), Action: Approve, By: "u1", At: time.Now()}
	first, applied, err := b.Record(d)
	if err != nil || !applied {
		t.Fatal(err)
	}
	again, applied, err := b.Record(d)
	if err != nil || applied || again.At != first.At {
		t.Fatalf("replay: applied %v err %v", applied, err)
	}
	if len(b.Decisions()) != 1 {
		t.Errorf("%d decisions stored", len(b.Decisions()))
	}
}

func TestDecisionNotEditable(t *testing.T) {
	b := NewBook()
	m := sampleMatch()
	if _, _, err := b.Record(Decision{Key: "k1", Match: m, Action: Approve, By: "u1", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Record(Decision{Key: "k2", Match: m, Action: Reject, By: "u2", At: time.Now()}); !errors.Is(err, ErrAlreadyDecided) {
		t.Errorf("second decision on the same match: %v", err)
	}
	other := Match{Rule: RuleTolerance, MovementIDs: []string{"m2"}, ItemIDs: []string{"i2"}}
	other.ID = matchID(other.Rule, other.MovementIDs, other.ItemIDs)
	if _, _, err := b.Record(Decision{Key: "k1", Match: other, Action: Approve, By: "u1", At: time.Now()}); !errors.Is(err, ErrKeyReused) {
		t.Errorf("key reused for another match: %v", err)
	}
	got := b.Decisions()
	got[0].Action = Reject // a copy: the book does not change
	if b.Decisions()[0].Action != Approve {
		t.Error("Decisions exposes the internal slice")
	}
}

func TestDecisionValidation(t *testing.T) {
	b := NewBook()
	m := sampleMatch()
	bad := []Decision{
		{Match: m, Action: Approve, By: "u", At: time.Now()},                         // no key
		{Key: "k", Match: m, Action: Approve, At: time.Now()},                        // no user
		{Key: "k", Match: m, Action: Approve, By: "u"},                               // no time
		{Key: "k", Match: m, Action: "maybe", By: "u", At: time.Now()},               // bad action
		{Key: "k", Match: m, MatchID: "x", Action: Approve, By: "u", At: time.Now()}, // snapshot of another match
	}
	tampered := m
	tampered.ItemIDs = []string{"other"}
	bad = append(bad, Decision{Key: "k", Match: tampered, Action: Approve, By: "u", At: time.Now()})
	for i, d := range bad {
		if _, _, err := b.Record(d); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d: %v", i, err)
		}
	}
}

func TestSettledFromBook(t *testing.T) {
	b := NewBook()
	auto1 := Match{Rule: RuleAmountDate, MovementIDs: []string{"a"}, ItemIDs: []string{"x"}, Tray: Auto}
	auto1.ID = matchID(auto1.Rule, auto1.MovementIDs, auto1.ItemIDs)
	auto2 := Match{Rule: RuleAmountDate, MovementIDs: []string{"b"}, ItemIDs: []string{"y"}, Tray: Auto}
	auto2.ID = matchID(auto2.Rule, auto2.MovementIDs, auto2.ItemIDs)
	sug := sampleMatch()
	b.Record(Decision{Key: "1", Match: auto2, Action: Reject, By: "u", At: time.Now()})
	b.Record(Decision{Key: "2", Match: sug, Action: Approve, By: "u", At: time.Now()})
	s := b.Settled([]Match{auto1, auto2})
	if len(s) != 2 || s[0].ID != sug.ID || s[1].ID != auto1.ID {
		t.Errorf("settled %+v", s)
	}
	if r := b.Rejected(); len(r) != 1 || r[0].ID != auto2.ID {
		t.Errorf("rejected %+v", r)
	}
}

func moneyOf(n int64) money.Amount { return money.Amount(n) }
