// Package listrange reads the date filter of a list: a month (period,
// YYYY-MM) or a range of civil dates (from and to, both included), as the
// API spec writes them for the pending, matches and invoices lists.
package listrange

import (
	"errors"
	"time"
)

// ErrInvalid is a filter the spec refuses with 400. Its message is safe to
// show: it names the rule, never a value.
var ErrInvalid = errors.New("invalid date filter")

// Range is a span of civil dates at midnight UTC, both ends included. A
// nil *Range is no filter.
type Range struct {
	From, Last time.Time
}

type invalid string

func (e invalid) Error() string   { return string(e) }
func (e invalid) Is(t error) bool { return t == ErrInvalid }

// Parse reads the three optional parameters. from and to go together,
// neither goes with period, and to is not before from. A month becomes the
// range of its days, so every list filters by one kind of range.
func Parse(period *string, from, to *time.Time) (*Range, error) {
	switch {
	case period != nil && (from != nil || to != nil):
		return nil, invalid("period does not go with from and to")
	case (from == nil) != (to == nil):
		return nil, invalid("from and to go together")
	case period != nil:
		first, err := time.Parse("2006-01", *period)
		if err != nil {
			return nil, invalid("period must be YYYY-MM")
		}
		return &Range{From: first, Last: first.AddDate(0, 1, -1)}, nil
	case from == nil:
		return nil, nil
	}
	f, l := civil(*from), civil(*to)
	if l.Before(f) {
		return nil, invalid("to is before from")
	}
	return &Range{From: f, Last: l}, nil
}

// Bounds returns the ends for a query, nil when there is no filter.
func (r *Range) Bounds() (from, last *time.Time) {
	if r == nil {
		return nil, nil
	}
	return &r.From, &r.Last
}

func civil(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
