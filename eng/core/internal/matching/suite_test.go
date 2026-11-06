package matching

import (
	"encoding/json"
	"math/rand"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

type suiteFile struct {
	Movements []struct {
		ID          string        `json:"id"`
		Date        string        `json:"date"`
		Amount      money.Amount  `json:"amount"`
		Description string        `json:"description"`
		Reference   string        `json:"reference"`
		ExternalRef string        `json:"external_ref"`
		Gross       *money.Amount `json:"gross"`
		Fees        []struct {
			Kind   string       `json:"kind"`
			Amount money.Amount `json:"amount"`
		} `json:"fees"`
	} `json:"movements"`
	Items []struct {
		ID         string       `json:"id"`
		Kind       string       `json:"kind"`
		Date       string       `json:"date"`
		Amount     money.Amount `json:"amount"`
		Refs       []string     `json:"refs"`
		Confidence float64      `json:"confidence"`
		Party      string       `json:"party"`
	} `json:"items"`
	Labels map[string]struct {
		Outcome string   `json:"outcome"` // match, pending, exception
		Tray    string   `json:"tray"`
		Rule    string   `json:"rule"`
		Items   []string `json:"items"`
	} `json:"labels"`
}

func loadSuite(t *testing.T) (suiteFile, Input) {
	t.Helper()
	b, err := os.ReadFile("testdata/suite_sintetica.json")
	if err != nil {
		t.Fatal(err)
	}
	var s suiteFile
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	d := func(v string) time.Time {
		x, err := time.Parse("2006-01-02", v)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	var in Input
	for _, m := range s.Movements {
		mv := Movement{ID: m.ID, Date: d(m.Date), Amount: m.Amount, Description: m.Description,
			Reference: m.Reference, ExternalRef: m.ExternalRef, Gross: m.Gross}
		for _, f := range m.Fees {
			mv.Fees = append(mv.Fees, Fee{Kind: f.Kind, Amount: f.Amount})
		}
		in.Movements = append(in.Movements, mv)
	}
	for _, it := range s.Items {
		in.Items = append(in.Items, Item{ID: it.ID, Kind: it.Kind, Date: d(it.Date), Amount: it.Amount,
			Refs: it.Refs, Confidence: it.Confidence, Party: it.Party})
	}
	return s, in
}

// Score of a run against the labels.
type Score struct {
	Total, Auto, Suggested, Pending, Exception                       int
	Correct, FalseMatch, WrongTray, WrongRule, MissedMatch, Spurious int
}

func score(s suiteFile, r Result) (Score, []string) {
	var sc Score
	var notes []string
	got := map[string]Match{}
	for _, m := range r.Matches {
		for _, id := range m.MovementIDs {
			got[id] = m
		}
	}
	pend := map[string]bool{}
	for _, p := range r.Pending {
		pend[p.MovementID] = true
	}
	for id, l := range s.Labels {
		sc.Total++
		m, matched := got[id]
		switch {
		case matched && m.Tray == Auto:
			sc.Auto++
		case matched:
			sc.Suggested++
		case pend[id]:
			sc.Pending++
		default:
			sc.Exception++
		}
		switch l.Outcome {
		case "match":
			switch {
			case !matched:
				sc.MissedMatch++
				notes = append(notes, id+": expected a match")
			case !slices.Equal(m.ItemIDs, l.Items):
				sc.FalseMatch++
				notes = append(notes, id+": matched the wrong items")
			case string(m.Tray) != l.Tray:
				sc.WrongTray++
				notes = append(notes, id+": wrong tray "+string(m.Tray))
			case m.Rule.ID != l.Rule:
				sc.WrongRule++
				notes = append(notes, id+": rule "+m.Rule.ID+", labeled "+l.Rule)
			default:
				sc.Correct++
			}
		case "pending":
			if matched {
				sc.Spurious++
				notes = append(notes, id+": matched but should be pending ("+m.Rule.ID+")")
			} else if pend[id] {
				sc.Correct++
			} else {
				notes = append(notes, id+": exception but should be pending")
			}
		case "exception":
			if matched {
				sc.Spurious++
				notes = append(notes, id+": matched but should be an exception ("+m.Rule.ID+")")
			} else if !pend[id] {
				sc.Correct++
			} else {
				notes = append(notes, id+": pending but should be an exception")
			}
		}
	}
	slices.Sort(notes)
	return sc, notes
}

// The synthetic suite: 200 labeled movements. The counts are logged; the
// test fails on any false or spurious match and on any label disagreement.
func TestSyntheticSuite(t *testing.T) {
	s, in := loadSuite(t)
	r := Run(DefaultConfig, in)
	sc, notes := score(s, r)
	t.Logf("synthetic suite: %d movements: auto %d, suggested %d, pending %d, exception %d; correct %d, false matches %d, spurious %d",
		sc.Total, sc.Auto, sc.Suggested, sc.Pending, sc.Exception, sc.Correct, sc.FalseMatch, sc.Spurious)
	for _, n := range notes {
		t.Log(n)
	}
	if sc.FalseMatch != 0 || sc.Spurious != 0 {
		t.Errorf("false matches %d, spurious %d", sc.FalseMatch, sc.Spurious)
	}
	if sc.Correct != sc.Total {
		t.Errorf("%d of %d movements disagree with their label", sc.Total-sc.Correct, sc.Total)
	}
	for _, m := range r.Matches {
		if m.Rule.ID == "" || m.Rule.Version == 0 || m.ID == "" {
			t.Fatalf("match without rule or version: %+v", m)
		}
	}
}

// Same input in any order gives the same result.
func TestDeterministicUnderShuffle(t *testing.T) {
	_, in := loadSuite(t)
	want := Run(DefaultConfig, in)
	rnd := rand.New(rand.NewSource(7))
	for i := 0; i < 5; i++ {
		rnd.Shuffle(len(in.Movements), func(a, b int) { in.Movements[a], in.Movements[b] = in.Movements[b], in.Movements[a] })
		rnd.Shuffle(len(in.Items), func(a, b int) { in.Items[a], in.Items[b] = in.Items[b], in.Items[a] })
		if got := Run(DefaultConfig, in); !reflect.DeepEqual(got, want) {
			t.Fatalf("shuffle %d changed the result", i)
		}
	}
}

// Running the same period twice creates no new matches: the second run sees
// the first run's matches as settled; and the same input yields the same
// match IDs, so a store with a unique ID never gets a second row.
func TestRerunCreatesNothingNew(t *testing.T) {
	_, in := loadSuite(t)
	first := Run(DefaultConfig, in)
	again := Run(DefaultConfig, in)
	for i := range first.Matches {
		if first.Matches[i].ID != again.Matches[i].ID {
			t.Fatal("match IDs differ between identical runs")
		}
	}
	in.Settled = first.Matches
	second := Run(DefaultConfig, in)
	if len(second.Matches) != 0 {
		t.Errorf("second run created %d matches", len(second.Matches))
	}
	if len(second.Pending) != len(first.Pending) || len(second.Exceptions) != len(first.Exceptions) {
		t.Errorf("pending %d→%d, exceptions %d→%d", len(first.Pending), len(second.Pending),
			len(first.Exceptions), len(second.Exceptions))
	}
}
