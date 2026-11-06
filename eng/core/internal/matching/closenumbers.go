package matching

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/closes"
	"github.com/Tramo-Labs/hq/eng/core/internal/matching/store"
)

// CloseNumbers gives the period close its two numbers (closes.Numbers):
// what blocks it and what it freezes. Both run in the close's transaction,
// under the tenant scope, without opening another. Ranges are civil dates
// [from, to), to excluded, as the close passes them.
type CloseNumbers struct{}

var _ closes.Numbers = CloseNumbers{}

// PendingSuggested counts the Suggested tray of the range with no decision
// in effect: undecided suggested matches and the movements waiting in
// Suggested. An escalated match has a decision (it waits in Exceptions)
// and does not count.
func (CloseNumbers) PendingSuggested(ctx context.Context, tx pgx.Tx, from, to time.Time) (int, error) {
	last, err := lastDay(from, to)
	if err != nil {
		return 0, err
	}
	n, err := store.New(tx).SuggestedPendingInRange(ctx, store.SuggestedPendingInRangeParams{DateFrom: from, DateTo: last})
	return int(n), err
}

// Summary is the results panel over the range as every role of the tenant
// sees it: the frozen summary is read by the client too, and the Suggested
// tray's numbers are the operator's only (spec section 1).
func (CloseNumbers) Summary(ctx context.Context, tx pgx.Tx, from, to, now time.Time) ([]closes.Metric, error) {
	last, err := lastDay(from, to)
	if err != nil {
		return nil, err
	}
	ms, err := panel(ctx, tx, span{From: from, Last: last}, now, false)
	if err != nil {
		return nil, err
	}
	out := make([]closes.Metric, 0, len(ms))
	for _, m := range ms {
		out = append(out, closes.Metric{Key: m.Key, Label: m.Label, Unit: m.Unit, Value: m.Value, Source: m.Source, List: m.List})
	}
	return out, nil
}

// lastDay turns [from, to) into the last day included.
func lastDay(from, to time.Time) (time.Time, error) {
	last := to.AddDate(0, 0, -1)
	if last.Before(from) {
		return time.Time{}, fmt.Errorf("%w: empty range", ErrInvalid)
	}
	return last, nil
}
