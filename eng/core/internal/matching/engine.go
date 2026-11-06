package matching

import (
	"slices"
	"strings"
	"unicode"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// Config holds the limits of the passes. They are per tenant settings; the
// defaults follow product/PRODUCTO.md.
type Config struct {
	WindowDays     int          // date window for amount based passes
	RoundingCap    money.Amount // largest difference the tolerance pass accepts
	ComboMax       int          // most items (or movements) in one combination
	ComboPool      int          // most candidates searched for a combination
	AutoConfidence float64      // items below this can only be suggested
}

// DefaultConfig: five days, ten pesos, up to six items over at most 18
// candidates, 0.85 minimum confidence.
var DefaultConfig = Config{WindowDays: 5, RoundingCap: 1000, ComboMax: 6, ComboPool: 18, AutoConfidence: 0.85}

// Input of a run. Settled are matches already registered (automatic or
// approved): their movements and items are not matched again, which makes a
// rerun over the same period create nothing new. Rejected are matches a
// person turned down: the same link is never proposed again.
type Input struct {
	Movements []Movement
	Items     []Item
	Settled   []Match
	Rejected  []Match
}

type engine struct {
	cfg      Config
	moves    []Movement
	items    []Item
	openM    map[string]bool
	openI    map[string]bool
	frozenM  map[string]bool // ambiguous in a stronger pass: later passes skip it
	frozenI  map[string]bool
	rejected map[string]bool
	pending  map[string]Pending
	refHits  map[string][]string // movement -> open items whose reference it carries
	itemByID map[string]Item
	moveByID map[string]Movement
	itemToks map[string][]string
	moveToks map[string]map[string]bool
	result   Result
}

// Run executes the passes in order over the open movements and items.
func Run(cfg Config, in Input) Result {
	e := &engine{
		cfg: cfg, openM: map[string]bool{}, openI: map[string]bool{},
		frozenM: map[string]bool{}, frozenI: map[string]bool{}, rejected: map[string]bool{},
		pending: map[string]Pending{}, refHits: map[string][]string{},
		itemByID: map[string]Item{}, moveByID: map[string]Movement{},
		itemToks: map[string][]string{}, moveToks: map[string]map[string]bool{},
	}
	settledM, settledI := map[string]bool{}, map[string]bool{}
	for _, m := range in.Settled {
		for _, id := range m.MovementIDs {
			settledM[id] = true
		}
		for _, id := range m.ItemIDs {
			settledI[id] = true
		}
	}
	for _, m := range in.Rejected {
		e.rejected[linkKey(m.MovementIDs, m.ItemIDs)] = true
	}
	for _, m := range in.Movements {
		if _, dup := e.moveByID[m.ID]; dup || settledM[m.ID] {
			continue
		}
		e.moveByID[m.ID] = m
		e.moves = append(e.moves, m)
		e.openM[m.ID] = true
		e.moveToks[m.ID] = movementTokens(m)
	}
	for _, it := range in.Items {
		if _, dup := e.itemByID[it.ID]; dup || settledI[it.ID] {
			continue
		}
		e.itemByID[it.ID] = it
		e.items = append(e.items, it)
		e.openI[it.ID] = true
		e.itemToks[it.ID] = itemTokens(it)
	}
	slices.SortFunc(e.moves, func(a, b Movement) int {
		if c := a.Date.Compare(b.Date); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	slices.SortFunc(e.items, func(a, b Item) int {
		if c := a.anchor().Compare(b.anchor()); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})

	e.pairPass(RuleReference, e.byReference)
	e.pairPass(RuleAmountDate, e.byAmountDate)
	e.pairPass(RuleDocumented, e.byDocumentedFees)
	e.pairPass(RuleTolerance, e.byTolerance)
	e.comboPass(RuleManyItems)
	e.comboPass(RuleManyMoves)
	e.finish()
	return e.result
}

// ---- one to one passes ----

type edge struct {
	m, i string
	ref  string
}

// pairPass collects the edges a rule allows between open movements and open
// items and accepts only those where both ends have exactly one edge. Ends
// with more than one are frozen and recorded as ambiguous.
func (e *engine) pairPass(r Rule, ok func(Movement, Item) (bool, string)) {
	var edges []edge
	degM, degI := map[string]int{}, map[string]int{}
	for _, m := range e.moves {
		if !e.openM[m.ID] || e.frozenM[m.ID] {
			continue
		}
		for _, it := range e.items {
			if !e.openI[it.ID] || e.frozenI[it.ID] {
				continue
			}
			hit, ref := ok(m, it)
			if !hit || e.rejected[linkKey([]string{m.ID}, []string{it.ID})] {
				continue
			}
			edges = append(edges, edge{m.ID, it.ID, ref})
			degM[m.ID]++
			degI[it.ID]++
		}
	}
	var ambiguousM []string
	for _, ed := range edges {
		if degM[ed.m] == 1 && degI[ed.i] == 1 {
			e.accept(r, []string{ed.m}, []string{ed.i}, ed.ref)
			continue
		}
		e.frozenI[ed.i] = true
		if !slices.Contains(ambiguousM, ed.m) {
			ambiguousM = append(ambiguousM, ed.m)
		}
	}
	// Every movement touching an ambiguous component gets all its options.
	for _, ed := range edges {
		if degI[ed.i] > 1 && !slices.Contains(ambiguousM, ed.m) {
			ambiguousM = append(ambiguousM, ed.m)
		}
	}
	for _, id := range ambiguousM {
		e.frozenM[id] = true
		var cands []Candidate
		for _, ed := range edges {
			if ed.m == id {
				cands = append(cands, Candidate{Rule: r, ItemIDs: []string{ed.i}})
			}
		}
		e.recordPending(id, ReasonAmbiguous, cands)
	}
}

func (e *engine) byReference(m Movement, it Item) (bool, string) {
	tok := e.sharedRef(m, it)
	if tok == "" {
		return false, ""
	}
	if !slices.Contains(e.refHits[m.ID], it.ID) {
		e.refHits[m.ID] = append(e.refHits[m.ID], it.ID)
	}
	return m.Amount == it.Amount, tok
}

func (e *engine) byAmountDate(m Movement, it Item) (bool, string) {
	return m.Amount == it.Amount && daysBetween(m.Date, it.anchor()) <= e.cfg.WindowDays, ""
}

// byDocumentedFees links a net movement with its gross item only when the
// movement's own source documents the fees that explain the difference.
// Without that documentation no adjustment is invented.
func (e *engine) byDocumentedFees(m Movement, it Item) (bool, string) {
	if m.Gross == nil || len(m.Fees) == 0 || *m.Gross != it.Amount {
		return false, ""
	}
	var fees money.Amount
	for _, f := range m.Fees {
		fees += f.Amount
	}
	if *m.Gross+fees != m.Amount {
		return false, ""
	}
	if tok := e.sharedRef(m, it); tok != "" {
		return true, tok
	}
	return daysBetween(m.Date, it.anchor()) <= e.cfg.WindowDays, ""
}

func (e *engine) byTolerance(m Movement, it Item) (bool, string) {
	d := (m.Amount - it.Amount).Abs()
	if d == 0 || d > e.cfg.RoundingCap || (m.Amount > 0) != (it.Amount > 0) {
		return false, ""
	}
	if tok := e.sharedRef(m, it); tok != "" {
		return true, tok
	}
	return daysBetween(m.Date, it.anchor()) <= e.cfg.WindowDays, ""
}

// ---- combinations ----

// comboPass looks for exactly one subset of items whose sum is a movement
// (RuleManyItems), or of movements whose sum is an item (RuleManyMoves).
// More than one subset, or an element claimed by two subsets, is ambiguity.
func (e *engine) comboPass(r Rule) {
	type sol struct {
		owner string
		parts []string
		party string
	}
	var found []sol
	use := map[string]int{}
	manyItems := r == RuleManyItems

	owners := e.moveIDs()
	if !manyItems {
		owners = e.itemIDs()
	}
	for _, owner := range owners {
		var target money.Amount
		var ids []string
		var amts []money.Amount
		party := ""
		if manyItems {
			m := e.moveByID[owner]
			target = m.Amount
			words := textWords(m.Description, m.Reference, m.ExternalRef)
			var pIDs []string
			var pAmts []money.Amount
			for _, it := range e.items {
				if e.openI[it.ID] && !e.frozenI[it.ID] && sameSign(it.Amount, target) && it.Amount.Abs() < target.Abs() &&
					daysBetween(m.Date, it.anchor()) <= e.cfg.WindowDays {
					ids, amts = append(ids, it.ID), append(amts, it.Amount)
					if namesParty(words, it.Party) {
						pIDs, pAmts = append(pIDs, it.ID), append(pAmts, it.Amount)
						party = it.Party
					}
				}
			}
			// When the movement names a counterparty, the search runs over
			// that counterparty's items only: stronger evidence and a small
			// search space. A unique solution there is still only suggested.
			if len(pIDs) >= 2 {
				ids, amts = pIDs, pAmts
			} else {
				party = ""
			}
		} else {
			it := e.itemByID[owner]
			target = it.Amount
			for _, m := range e.moves {
				if e.openM[m.ID] && !e.frozenM[m.ID] && sameSign(m.Amount, target) && m.Amount.Abs() < target.Abs() &&
					daysBetween(m.Date, it.anchor()) <= e.cfg.WindowDays {
					ids, amts = append(ids, m.ID), append(amts, m.Amount)
				}
			}
		}
		if len(ids) < 2 {
			continue
		}
		if len(ids) > e.cfg.ComboPool {
			// Too many candidates to prove a combination is the only one:
			// no proposal. The movement keeps whatever other outcome it has.
			continue
		}
		subsets := subsetSums(ids, amts, target, e.cfg.ComboMax, 3)
		var valid [][]string
		for _, s := range subsets {
			mv, it := []string{owner}, s
			if !manyItems {
				mv, it = s, []string{owner}
			}
			if !e.rejected[linkKey(mv, it)] {
				valid = append(valid, s)
			}
		}
		switch len(valid) {
		case 0:
		case 1:
			found = append(found, sol{owner, valid[0], party})
			for _, p := range valid[0] {
				use[p]++
			}
		default:
			var cands []Candidate
			for _, s := range valid {
				cands = append(cands, Candidate{Rule: r, ItemIDs: s})
			}
			if manyItems {
				e.frozenM[owner] = true
				e.recordPending(owner, ReasonComboAmbiguous, cands)
			} else {
				e.frozenI[owner] = true
				for _, s := range valid {
					for _, p := range s {
						e.frozenM[p] = true
						e.recordPending(p, ReasonComboAmbiguous, []Candidate{{Rule: r, ItemIDs: []string{owner}}})
					}
				}
			}
		}
	}
	for _, s := range found {
		clash := false
		for _, p := range s.parts {
			if use[p] > 1 {
				clash = true
			}
		}
		if manyItems {
			if clash {
				e.frozenM[s.owner] = true
				e.recordPending(s.owner, ReasonComboAmbiguous, []Candidate{{Rule: r, ItemIDs: s.parts}})
				continue
			}
			e.accept(r, []string{s.owner}, s.parts, s.party)
		} else {
			if clash {
				for _, p := range s.parts {
					e.recordPending(p, ReasonComboAmbiguous, []Candidate{{Rule: r, ItemIDs: []string{s.owner}}})
				}
				continue
			}
			e.accept(r, s.parts, []string{s.owner}, "")
		}
	}
}

// subsetSums returns up to limit subsets of 2..max elements whose amounts
// add up to target, in a deterministic order.
func subsetSums(ids []string, amts []money.Amount, target money.Amount, max, limit int) [][]string {
	idx := make([]int, len(ids))
	for i := range idx {
		idx[i] = i
	}
	slices.SortFunc(idx, func(a, b int) int {
		if amts[a].Abs() != amts[b].Abs() {
			if amts[a].Abs() > amts[b].Abs() {
				return -1
			}
			return 1
		}
		return strings.Compare(ids[a], ids[b])
	})
	var out [][]string
	var cur []int
	var walk func(start int, sum money.Amount)
	walk = func(start int, sum money.Amount) {
		if len(out) >= limit {
			return
		}
		if sum == target && len(cur) >= 2 {
			s := make([]string, len(cur))
			for i, c := range cur {
				s[i] = ids[c]
			}
			slices.Sort(s)
			out = append(out, s)
			return
		}
		if len(cur) == max {
			return
		}
		for k := start; k < len(idx); k++ {
			next := sum + amts[idx[k]]
			if next.Abs() > target.Abs() {
				continue
			}
			cur = append(cur, idx[k])
			walk(k+1, next)
			cur = cur[:len(cur)-1]
		}
	}
	walk(0, 0)
	return out
}

func sameSign(a, b money.Amount) bool { return (a > 0) == (b > 0) && a != 0 && b != 0 }

// ---- bookkeeping ----

func (e *engine) accept(r Rule, moves, items []string, ref string) {
	m := Match{ID: matchID(r, moves, items), Rule: r, MovementIDs: slices.Clone(moves), ItemIDs: slices.Clone(items), Reference: ref}
	slices.Sort(m.MovementIDs)
	slices.Sort(m.ItemIDs)
	var mvSum, itSum money.Amount
	low := false
	for _, id := range m.MovementIDs {
		mv := e.moveByID[id]
		mvSum += mv.Amount
		if p := period(mv.Date); !slices.Contains(m.MovePeriods, p) {
			m.MovePeriods = append(m.MovePeriods, p)
		}
		e.openM[id] = false
	}
	for _, id := range m.ItemIDs {
		it := e.itemByID[id]
		itSum += it.Amount
		if it.Confidence < e.cfg.AutoConfidence {
			low = true
		}
		if p := period(it.anchor()); !slices.Contains(m.ItemPeriods, p) {
			m.ItemPeriods = append(m.ItemPeriods, p)
		}
		for _, mid := range m.MovementIDs {
			m.DateDiffDays = max(m.DateDiffDays, daysBetween(e.moveByID[mid].Date, it.anchor()))
		}
		e.openI[id] = false
	}
	slices.Sort(m.MovePeriods)
	slices.Sort(m.ItemPeriods)
	m.CrossPeriod = !slices.Equal(m.MovePeriods, m.ItemPeriods)
	m.LowConfidence = low

	switch r {
	case RuleDocumented:
		mv := e.moveByID[m.MovementIDs[0]]
		for _, f := range mv.Fees {
			m.Adjustments = append(m.Adjustments, Adjustment{Kind: f.Kind, Amount: f.Amount, Source: "documented_by_source"})
		}
	case RuleTolerance:
		m.Adjustments = []Adjustment{{Kind: "rounding", Amount: mvSum - itSum, Source: "rounding_tolerance"}}
	}

	m.Tray = Suggested
	if (r == RuleReference || r == RuleAmountDate) && !low {
		m.Tray = Auto
	}
	e.result.Matches = append(e.result.Matches, m)
	for _, id := range m.MovementIDs {
		delete(e.pending, id)
	}
}

func (e *engine) recordPending(moveID string, reason PendingReason, cands []Candidate) {
	if _, ok := e.pending[moveID]; ok {
		return // the strongest pass that saw ambiguity keeps its record
	}
	e.pending[moveID] = Pending{MovementID: moveID, Tray: Suggested, Reason: reason, Candidates: cands}
}

func (e *engine) finish() {
	for _, m := range e.moves {
		if !e.openM[m.ID] {
			continue
		}
		if p, ok := e.pending[m.ID]; ok {
			e.result.Pending = append(e.result.Pending, p)
			continue
		}
		var cands []Candidate
		for _, id := range e.refHits[m.ID] {
			if e.openI[id] {
				cands = append(cands, Candidate{Rule: RuleReference, ItemIDs: []string{id}})
			}
		}
		if len(cands) > 0 {
			e.result.Pending = append(e.result.Pending, Pending{MovementID: m.ID, Tray: Suggested, Reason: ReasonRefAmountDiffers, Candidates: cands})
			continue
		}
		e.result.Exceptions = append(e.result.Exceptions, Pending{MovementID: m.ID, Tray: Exception, Reason: ReasonNoCandidate})
	}
	for _, it := range e.items {
		if e.openI[it.ID] {
			e.result.OpenItems = append(e.result.OpenItems, it.ID)
		}
	}
	slices.SortFunc(e.result.Matches, func(a, b Match) int { return strings.Compare(a.MovementIDs[0]+a.ID, b.MovementIDs[0]+b.ID) })
}

func (e *engine) moveIDs() []string {
	var out []string
	for _, m := range e.moves {
		if e.openM[m.ID] && !e.frozenM[m.ID] {
			out = append(out, m.ID)
		}
	}
	return out
}

func (e *engine) itemIDs() []string {
	var out []string
	for _, it := range e.items {
		if e.openI[it.ID] && !e.frozenI[it.ID] {
			out = append(out, it.ID)
		}
	}
	return out
}

// ---- references ----

// sharedRef returns the first reference of the item that appears as a token
// of the movement, or "".
func (e *engine) sharedRef(m Movement, it Item) string {
	toks := e.moveToks[m.ID]
	for _, t := range e.itemToks[it.ID] {
		if toks[t] {
			return t
		}
	}
	return ""
}

// normRef squashes a reference to uppercase letters and digits and drops
// leading zeros, so "0002-00001234", "2-1234" padded or "000200001234"
// compare equal. Tokens shorter than four characters, or without a digit,
// are too weak to link.
func normRef(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	t := strings.TrimLeft(b.String(), "0")
	if len(t) < 4 || !strings.ContainsAny(t, "0123456789") {
		return ""
	}
	return t
}

// itemTokens normalizes each word of each reference ("CH 00012345" gives
// "12345"; "0002-00001234" gives "200001234"). Hyphen parts are not split on
// the item side: a piece of an invoice number alone is too weak.
func itemTokens(it Item) []string {
	var out []string
	for _, r := range it.Refs {
		words := strings.FieldsFunc(r, func(c rune) bool {
			return !(unicode.IsLetter(c) || unicode.IsDigit(c) || c == '-')
		})
		for _, w := range words {
			if t := normRef(w); t != "" && !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	return out
}

// textWords returns the uppercase words of the movement texts.
func textWords(texts ...string) map[string]bool {
	out := map[string]bool{}
	for _, s := range texts {
		for _, w := range strings.FieldsFunc(strings.ToUpper(s), func(c rune) bool {
			return !(unicode.IsLetter(c) || unicode.IsDigit(c))
		}) {
			out[w] = true
		}
	}
	return out
}

// namesParty reports whether every word of the party name appears in the
// movement texts.
func namesParty(words map[string]bool, party string) bool {
	pw := strings.FieldsFunc(strings.ToUpper(party), func(c rune) bool {
		return !(unicode.IsLetter(c) || unicode.IsDigit(c))
	})
	if len(pw) == 0 {
		return false
	}
	for _, w := range pw {
		if !words[w] {
			return false
		}
	}
	return true
}

// movementTokens splits the movement texts on anything that is not a
// letter, digit or hyphen, and keeps each piece and its hyphen-joined form.
func movementTokens(m Movement) map[string]bool {
	out := map[string]bool{}
	for _, s := range []string{m.Reference, m.ExternalRef, m.Description} {
		for _, f := range strings.FieldsFunc(s, func(r rune) bool {
			return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-')
		}) {
			if t := normRef(f); t != "" {
				out[t] = true
			}
			for _, p := range strings.Split(f, "-") {
				if t := normRef(p); t != "" {
					out[t] = true
				}
			}
		}
	}
	return out
}

// linkKey identifies a set of links regardless of the rule: a rejected link
// is not proposed again by any pass.
func linkKey(moves, items []string) string { return matchID(Rule{}, moves, items) }
