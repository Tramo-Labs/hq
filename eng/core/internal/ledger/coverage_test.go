package ledger

import (
	"strings"
	"testing"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

func d(m time.Month, day int) time.Time { return time.Date(2026, m, day, 0, 0, 0, 0, time.UTC) }

// The chain check names the days the missing movements are in, both
// balances and the difference, in a sentence the tenant can act on.
func TestChainGapNamesTheRange(t *testing.T) {
	s := NewMemStore()
	in := &Ingester{Store: s}
	ingest(t, in, Bancor, "acc", "bancor_ok.csv")
	r := ingest(t, in, Bancor, "acc", "bancor_nov_gap.csv")
	if r.Reason != ReasonChainGap {
		t.Fatalf("%s %s", r.Status, r.Reason)
	}
	for _, want := range []string{"missing between 10/10/2026 and 02/11/2026", "closed at 1664434.73",
		"opens at 1669434.73", "difference 5000.00"} {
		if !strings.Contains(r.Detail, want) {
			t.Errorf("detail %q does not say %q", r.Detail, want)
		}
	}
}

func TestBalanceErrorNamesDateAndDifference(t *testing.T) {
	r := ingest(t, &Ingester{Store: NewMemStore()}, Bancor, "acc", "bancor_bad_balance.csv")
	for _, want := range []string{"line 6 (06/10/2026)", "a difference of 1.00"} {
		if !strings.Contains(r.Detail, want) {
			t.Errorf("detail %q does not say %q", r.Detail, want)
		}
	}
}

func TestGaps(t *testing.T) {
	const a, b money.Amount = 1000_00, 1500_00
	from, to := d(11, 1), d(11, 15)
	cases := []struct {
		name  string
		spans []Span
		want  []Gap // From, To, Missing, Amount
	}{
		{"nothing uploaded", nil, []Gap{{From: from, To: to}}},
		{"whole fortnight", []Span{{d(10, 20), d(11, 15), a, b}}, nil},
		// Days 6 to 9 have no rows, but the balances chain: nothing missing.
		{"chained with quiet days", []Span{{d(11, 1), d(11, 5), a, b}, {d(11, 10), d(11, 15), b, a}}, nil},
		{"overlapping exports", []Span{{d(11, 1), d(11, 8), a, b}, {d(11, 6), d(11, 15), a, a}}, nil},
		{"balance break", []Span{{d(11, 1), d(11, 5), a, b}, {d(11, 10), d(11, 15), b + 200_00, a}},
			[]Gap{{From: d(11, 5), To: d(11, 10), Missing: true, Amount: 200_00}}},
		{"late start and early end", []Span{{d(11, 4), d(11, 12), a, b}},
			[]Gap{{From: from, To: d(11, 3)}, {From: d(11, 13), To: to}}},
		// A break before the period is not this period's gap.
		{"break before the period", []Span{{d(10, 1), d(10, 10), a, b}, {d(10, 20), d(11, 15), a, b}}, nil},
		// Uploaded in the wrong order: sorted before chaining.
		{"unsorted", []Span{{d(11, 10), d(11, 15), b, a}, {d(11, 1), d(11, 5), a, b}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Gaps(c.spans, from, to)
			if len(got) != len(c.want) {
				t.Fatalf("%d gaps %+v; want %d", len(got), got, len(c.want))
			}
			for i, g := range got {
				w := c.want[i]
				if !g.From.Equal(w.From) || !g.To.Equal(w.To) || g.Missing != w.Missing || g.Amount != w.Amount {
					t.Errorf("gap %d: %s..%s missing %v %s; want %s..%s %v %s", i, day(g.From), day(g.To), g.Missing,
						g.Amount, day(w.From), day(w.To), w.Missing, w.Amount)
				}
				if g.Detail == "" || !strings.Contains(g.Detail, day(g.From)) {
					t.Errorf("gap %d detail %q does not name %s", i, g.Detail, day(g.From))
				}
			}
		})
	}
}
