// Package matching links bank movements with the items they settle
// (invoices, payments, open items from the ERP) and says why. It is
// deterministic: the same input gives the same output in any order, every
// match carries the rule and rule version that made it, and when two
// candidates are equally good the engine does not choose: it leaves the
// movement pending with every candidate for a person.
package matching

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// Fee is a deduction the movement's source documents (Mercado Pago fees,
// taxes withheld on a settlement).
type Fee struct {
	Kind   string
	Amount money.Amount // negative for deductions
}

// Movement is a bank or Mercado Pago line, as stored by the ledger.
type Movement struct {
	ID          string
	AccountID   string
	Date        time.Time
	Amount      money.Amount // credits positive
	Description string
	Reference   string
	ExternalRef string
	Gross       *money.Amount // when the source documents fees
	Fees        []Fee
}

// Item is what a movement can settle: an invoice, an expected collection or
// payment, an open item read from the ERP. Amount carries the sign it will
// have in the bank (a customer invoice is positive, a supplier invoice is
// negative).
type Item struct {
	ID     string
	Kind   string // invoice, payment, open_item
	Date   time.Time
	Due    time.Time // optional; when set the date window centers on it
	Amount money.Amount
	Refs   []string // invoice number, cheque number, Mercado Pago id, order id
	// Confidence of the item's data. ERP items are 1. Items read from
	// documents carry documents.Invoice.MinConfidence: below AutoConfidence
	// they can only be suggested.
	Confidence float64
	Party      string // counterparty CUIT or name, informative
}

func (it Item) anchor() time.Time {
	if !it.Due.IsZero() {
		return it.Due
	}
	return it.Date
}

// Tray is where a result waits.
type Tray string

const (
	// Auto: an exact rule without ambiguity. Registered with its label and
	// reviewed afterwards.
	Auto Tray = "auto"
	// Suggested: tolerance, combinations, documented fees, low confidence
	// data, or ambiguity. An operator decides.
	Suggested Tray = "suggested"
	// Exception: no candidate at all. The client decides with the
	// explanation.
	Exception Tray = "exception"
)

// Rule identifies the pass that made a match. The version changes whenever
// the behavior of the pass changes, so old matches stay explainable.
type Rule struct {
	ID      string
	Version int
}

func (r Rule) String() string { return fmt.Sprintf("%s@v%d", r.ID, r.Version) }

// The passes, in the order they run.
var (
	RuleReference  = Rule{"reference_exact", 1}
	RuleAmountDate = Rule{"amount_date_window", 1}
	RuleDocumented = Rule{"net_of_documented_fees", 1}
	RuleTolerance  = Rule{"amount_tolerance", 1}
	RuleManyItems  = Rule{"one_movement_many_items", 1}
	RuleManyMoves  = Rule{"many_movements_one_item", 1}
	RulesInOrder   = []Rule{RuleReference, RuleAmountDate, RuleDocumented, RuleTolerance, RuleManyItems, RuleManyMoves}
)

// Adjustment is a documented difference between the movements and the
// items of a match.
type Adjustment struct {
	Kind   string       // mp_fee, taxes, rounding, ...
	Amount money.Amount // movements minus items, for this kind
	Source string       // documented_by_source or rounding_tolerance
}

// Match links movements with items.
type Match struct {
	ID          string // deterministic: same rule and same links give the same ID
	Rule        Rule
	Tray        Tray
	MovementIDs []string
	ItemIDs     []string
	Adjustments []Adjustment
	// Evidence, kept structured so an explanation can be written from it.
	Reference     string // the reference token, or for a combination the counterparty named, that linked them
	DateDiffDays  int    // largest distance between a movement and an item
	CrossPeriod   bool   // movements and items fall in different months
	MovePeriods   []string
	ItemPeriods   []string
	LowConfidence bool // an item had data below the automatic threshold
	AfterClose    bool // the client linked it after its period closed: not in that close
}

// Candidate is one option of a pending decision.
type Candidate struct {
	Rule    Rule
	ItemIDs []string
}

// PendingReason says why a movement waits for a person.
type PendingReason string

const (
	ReasonAmbiguous PendingReason = "ambiguous" // two or more equally good candidates
	// ReasonSharedCandidate: one candidate, but it is also the only one of
	// another movement (two equal transfers, one invoice). Who takes it is
	// not guessed.
	ReasonSharedCandidate  PendingReason = "shared_candidate"
	ReasonComboAmbiguous   PendingReason = "combination_ambiguous"
	ReasonRefAmountDiffers PendingReason = "reference_amount_differs" // a reference points at an item whose amount does not close
	ReasonNoCandidate      PendingReason = "no_candidate"
)

// Pending is a movement the engine did not match, with why and the options.
type Pending struct {
	MovementID string
	Tray       Tray
	Reason     PendingReason
	Candidates []Candidate
}

// Result of a run.
type Result struct {
	Matches    []Match
	Pending    []Pending // ambiguity and partial evidence: Suggested tray
	Exceptions []Pending // no candidate: Exception tray
	OpenItems  []string  // items nothing settled yet (not an exception)
}

func matchID(r Rule, moves, items []string) string {
	m := slices.Clone(moves)
	it := slices.Clone(items)
	slices.Sort(m)
	slices.Sort(it)
	h := sha256.Sum256([]byte(r.String() + "\x1f" + strings.Join(m, ",") + "\x1f" + strings.Join(it, ",")))
	return hex.EncodeToString(h[:16])
}

func period(t time.Time) string { return t.Format("2006-01") }

func daysBetween(a, b time.Time) int {
	d := int(a.Sub(b).Round(time.Hour).Hours() / 24)
	if d < 0 {
		return -d
	}
	return d
}
