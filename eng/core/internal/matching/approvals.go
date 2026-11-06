package matching

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

// Action a person takes on a match.
type Action string

const (
	Approve Action = "approve"
	Reject  Action = "reject"
)

// Decision is a human decision on a match: who, when and on what. Decisions
// are never edited or deleted. Key is the client's idempotency key: the same
// decision sent twice is applied once.
type Decision struct {
	Key     string
	MatchID string
	Match   Match // snapshot of what was decided on
	Action  Action
	By      string // user id
	Role    string // operator for Suggested, approver (client) for Exception
	At      time.Time
	Note    string
}

var (
	ErrAlreadyDecided = errors.New("match already has a decision; decisions are not edited")
	ErrKeyReused      = errors.New("idempotency key already used for a different decision")
	ErrInvalid        = errors.New("invalid decision")
)

// Book is the append-only record of decisions. The Postgres table behind it
// rejects UPDATE and DELETE with a trigger, like the audit log.
type Book struct {
	mu        sync.Mutex
	decisions []Decision
	byKey     map[string]int
	byMatch   map[string]int
}

// NewBook returns an empty book.
func NewBook() *Book { return &Book{byKey: map[string]int{}, byMatch: map[string]int{}} }

// Record appends a decision. It returns the stored decision and whether this
// call applied it (false on an idempotent replay).
func (b *Book) Record(d Decision) (Decision, bool, error) {
	if d.Key == "" || d.By == "" || d.At.IsZero() || d.Match.ID == "" || (d.Action != Approve && d.Action != Reject) {
		return Decision{}, false, ErrInvalid
	}
	if d.MatchID == "" {
		d.MatchID = d.Match.ID
	}
	if d.MatchID != d.Match.ID || d.Match.ID != matchID(d.Match.Rule, d.Match.MovementIDs, d.Match.ItemIDs) {
		return Decision{}, false, fmt.Errorf("%w: match snapshot does not belong to %s", ErrInvalid, d.MatchID)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if i, ok := b.byKey[d.Key]; ok {
		prev := b.decisions[i]
		if prev.MatchID == d.MatchID && prev.Action == d.Action && prev.By == d.By {
			return prev, false, nil
		}
		return Decision{}, false, ErrKeyReused
	}
	if _, ok := b.byMatch[d.MatchID]; ok {
		return Decision{}, false, ErrAlreadyDecided
	}
	d.Match.MovementIDs = slices.Clone(d.Match.MovementIDs)
	d.Match.ItemIDs = slices.Clone(d.Match.ItemIDs)
	b.decisions = append(b.decisions, d)
	b.byKey[d.Key] = len(b.decisions) - 1
	b.byMatch[d.MatchID] = len(b.decisions) - 1
	return d, true, nil
}

// Decisions returns a copy of every decision in the order recorded.
func (b *Book) Decisions() []Decision {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.decisions)
}

// Settled returns the approved matches plus the automatic matches nobody
// rejected, ready for Input.Settled. Rejected returns the matches turned
// down, for Input.Rejected.
func (b *Book) Settled(auto []Match) []Match {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Match
	for _, d := range b.decisions {
		if d.Action == Approve {
			out = append(out, d.Match)
		}
	}
	for _, m := range auto {
		if i, ok := b.byMatch[m.ID]; ok {
			if b.decisions[i].Action == Reject {
				continue
			}
			if b.decisions[i].Action == Approve {
				continue // already in out
			}
		}
		out = append(out, m)
	}
	return out
}

// Rejected returns the matches people turned down.
func (b *Book) Rejected() []Match {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Match
	for _, d := range b.decisions {
		if d.Action == Reject {
			out = append(out, d.Match)
		}
	}
	return out
}
