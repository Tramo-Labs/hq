package ledger

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/ledger/store"
	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// Span is one accepted statement of an account: the dates of its first and
// last movement (or its stated period) and its balances.
type Span struct {
	From, To         time.Time
	Opening, Closing money.Amount
}

// Gap is a stretch of a period that the stored statements do not prove
// complete. Missing is true when the balances prove movements are missing
// (one statement closes at a balance the next one does not open at); false
// when nothing covers those days, which may also mean there were no
// movements then. Detail is the sentence the tenant reads.
type Gap struct {
	From, To time.Time
	Missing  bool
	Amount   money.Amount // the difference that is missing, when Missing
	Detail   string
}

// Gaps returns what is not proven in [from, to] for one account, in date
// order. Two statements chain when they overlap, or when the later one
// opens at the balance the earlier one closed with: then nothing is missing
// between them whatever the days in between (days without movements leave
// no rows in an export with a running balance). A break in balances is a
// missing stretch from the last movement before it to the first after it,
// both days included because an export can be cut mid-day.
func Gaps(spans []Span, from, to time.Time) []Gap {
	from, to = civil(from), civil(to)
	if len(spans) == 0 {
		return []Gap{{From: from, To: to, Detail: fmt.Sprintf("no statement covers %s to %s", day(from), day(to))}}
	}
	s := slices.Clone(spans)
	slices.SortStableFunc(s, func(a, b Span) int {
		if c := a.From.Compare(b.From); c != 0 {
			return c
		}
		return a.To.Compare(b.To)
	})
	var out []Gap
	add := func(g Gap) {
		if g.To.Before(from) || g.From.After(to) {
			return
		}
		if g.From.Before(from) {
			g.From = from
		}
		if g.To.After(to) {
			g.To = to
		}
		out = append(out, g)
	}
	if s[0].From.After(from) {
		end := s[0].From.AddDate(0, 0, -1)
		add(Gap{From: from, To: end, Detail: fmt.Sprintf("no statement covers %s to %s", day(from), day(end))})
	}
	cur := s[0]
	for _, n := range s[1:] {
		if !n.From.After(cur.To) || n.Opening == cur.Closing {
			if n.To.After(cur.To) {
				cur.To, cur.Closing = n.To, n.Closing
			}
			continue
		}
		add(Gap{From: cur.To, To: n.From, Missing: true, Amount: n.Opening - cur.Closing,
			Detail: chainGapDetailNext(cur.To, cur.Closing, n.From, n.Opening)})
		cur = n
	}
	if cur.To.Before(to) {
		next := cur.To.AddDate(0, 0, 1)
		add(Gap{From: next, To: to, Detail: fmt.Sprintf(
			"the latest statement reaches %s: no statement covers %s to %s yet",
			day(cur.To), day(next), day(to))})
	}
	return out
}

// AccountCoverage is what a period's statements prove for one account.
type AccountCoverage struct {
	Account Account
	Gaps    []Gap // empty when the period is proven complete
}

// Coverage returns, for every account of the tenant in the context, the
// stretches of [from, to] its accepted statements do not prove. The period
// close reads it: an account with gaps is not ready to close.
func (s *PGStore) Coverage(ctx context.Context, from, to time.Time) ([]AccountCoverage, error) {
	accts, err := s.Accounts(ctx)
	if err != nil {
		return nil, err
	}
	spans := map[uuid.UUID][]Span{}
	err = s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		rows, err := store.New(tx).StatementSpans(ctx)
		for _, r := range rows {
			spans[r.AccountID] = append(spans[r.AccountID], Span{From: civil(r.PeriodFrom), To: civil(r.PeriodTo),
				Opening: money.Amount(r.Opening), Closing: money.Amount(r.Closing)})
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	out := make([]AccountCoverage, 0, len(accts))
	for _, a := range accts {
		out = append(out, AccountCoverage{Account: a, Gaps: Gaps(spans[a.ID], from, to)})
	}
	return out, nil
}

// chainGapDetailNext is chainGapDetail for two stored statements.
func chainGapDetailNext(prevTo time.Time, prevClosing money.Amount, from time.Time, opening money.Amount) string {
	return fmt.Sprintf("movements are missing between %s and %s: the statement up to %s closed at %s and the next one, "+
		"from %s, opens at %s (difference %s)",
		day(prevTo), day(from), day(prevTo), prevClosing, day(from), opening, opening-prevClosing)
}
