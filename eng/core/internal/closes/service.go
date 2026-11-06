package closes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/closes/store"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/audit"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
)

// Metric is one number of a period's summary, as the results panel shows
// it: what it counts (Source) and where its rows are (List). A nil Value
// means there was nothing to measure, never zero.
type Metric struct {
	Key    string   `json:"key"`
	Label  string   `json:"label"`
	Unit   string   `json:"unit"`
	Value  *float64 `json:"value"`
	Source string   `json:"source"`
	List   string   `json:"list"`
}

// Numbers is what matching provides for a range of civil dates [from, to)
// (datos). Both run in the close's transaction, under the tenant scope, so
// the count, the frozen numbers and the close row are one atomic write.
type Numbers interface {
	// PendingSuggested counts the Suggested items of the range with no
	// decision in effect (an escalated one is in Exceptions, not counted).
	PendingSuggested(ctx context.Context, tx pgx.Tx, from, to time.Time) (int, error)
	// Summary computes the results panel's numbers for the range.
	Summary(ctx context.Context, tx pgx.Tx, from, to, now time.Time) ([]Metric, error)
}

// Close is a closed period with its frozen summary.
type Close struct {
	ID       uuid.UUID
	Period   Period
	ClosedAt time.Time
	ClosedBy uuid.UUID
	Summary  []Metric
}

// Due is a period the fortnightly job marked to close and nobody closed yet.
type Due struct {
	Period   Period
	MarkedAt time.Time
}

var (
	// ErrForbidden: only an operator closes.
	ErrForbidden = errors.New("closes: only an operator closes a period")
	// ErrNotOver: the period's last day is not over in Argentina yet.
	ErrNotOver = errors.New("closes: the period has not ended")
	// ErrAlreadyClosed: that period is closed.
	ErrAlreadyClosed = errors.New("closes: the period is already closed")
	// ErrNoNumbers: matching does not provide the period's numbers yet.
	ErrNoNumbers = errors.New("closes: period numbers are not available yet")
	// ErrNotFound: no close with that id in the tenant.
	ErrNotFound = errors.New("closes: not found")
)

// PendingError refuses a close while the period has Suggested items.
type PendingError struct{ N int }

func (e *PendingError) Error() string {
	return fmt.Sprintf("closes: %d suggested items pending in the period", e.N)
}

// Service closes periods and answers whether a date is closed.
type Service struct {
	DB      *db.DB
	Numbers Numbers // nil until matching provides it: closing answers ErrNoNumbers
	Cadence Cadence // zero means DefaultCadence
	Now     func() time.Time
}

func (s *Service) cadence() Cadence {
	if s.Cadence == "" {
		return DefaultCadence
	}
	return s.Cadence
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// ClosePeriod closes the period of the tenant in scope that starts on
// start. The caller's role is read from memberships in the transaction,
// not from the token. Refused, with nothing written: ErrForbidden,
// ErrInvalidPeriod, ErrNotOver, ErrAlreadyClosed, *PendingError,
// ErrNoNumbers.
func (s *Service) ClosePeriod(ctx context.Context, start time.Time) (Close, error) {
	var out Close
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		role, err := q.MemberRole(ctx)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && role != "operator") {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		p, err := PeriodStarting(start, s.cadence())
		if err != nil {
			return err
		}
		now := s.now()
		if !p.OverAt(now) {
			return ErrNotOver
		}
		// From here on no write that asked ClosedOn can slip in: they hold
		// the shared side of this lock until they commit.
		if err := q.LockExclusive(ctx); err != nil {
			return err
		}
		closed, err := q.IsClosed(ctx, p.Start)
		if err != nil {
			return err
		}
		if closed {
			return ErrAlreadyClosed
		}
		if s.Numbers == nil {
			return ErrNoNumbers
		}
		n, err := s.Numbers.PendingSuggested(ctx, tx, p.Start, p.End)
		if err != nil {
			return err
		}
		if n > 0 {
			return &PendingError{N: n}
		}
		summary, err := s.Numbers.Summary(ctx, tx, p.Start, p.End, now)
		if err != nil {
			return err
		}
		if summary == nil {
			summary = []Metric{}
		}
		raw, err := json.Marshal(summary)
		if err != nil {
			return err
		}
		row, err := q.InsertClose(ctx, store.InsertCloseParams{Cadence: string(p.Cadence),
			PeriodStart: p.Start, PeriodEnd: p.End, Summary: raw})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAlreadyClosed
		}
		if err != nil {
			return err
		}
		out, err = toClose(row.ID, row.Cadence, row.PeriodStart, row.PeriodEnd, row.ClosedAt, row.ClosedBy, row.Summary)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "closes.close", Subject: "closes.closes:" + out.ID.String(),
			Detail: map[string]any{"cadence": string(p.Cadence), "period_start": p.Start.Format(time.DateOnly),
				"period_end": p.LastDay().Format(time.DateOnly), "metrics": len(summary)}})
	})
	return out, err
}

// ListPageSize is how many closes a page of the list carries.
const ListPageSize = 50

// List returns the tenant's closes, newest first, starting before the
// period that starts on before (nil: from the newest). more says there may
// be another page.
func (s *Service) List(ctx context.Context, before *time.Time) (cs []Close, more bool, err error) {
	err = s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		rows, err := store.New(tx).ListCloses(ctx, store.ListClosesParams{Before: before, Lim: ListPageSize + 1})
		if err != nil {
			return err
		}
		if len(rows) > ListPageSize {
			rows, more = rows[:ListPageSize], true
		}
		cs = make([]Close, 0, len(rows))
		for _, r := range rows {
			c, err := toClose(r.ID, r.Cadence, r.PeriodStart, r.PeriodEnd, r.ClosedAt, r.ClosedBy, r.Summary)
			if err != nil {
				return err
			}
			cs = append(cs, c)
		}
		return nil
	})
	return cs, more, err
}

// Get returns one close of the tenant, or ErrNotFound.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Close, error) {
	var c Close
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		r, err := store.New(tx).GetClose(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		c, err = toClose(r.ID, r.Cadence, r.PeriodStart, r.PeriodEnd, r.ClosedAt, r.ClosedBy, r.Summary)
		return err
	})
	return c, err
}

// ClosedOn reports whether any of the civil dates falls in a closed period
// of the tenant in scope. It runs in the caller's transaction and takes the
// shared side of the close lock until that transaction ends: a write that
// saw "open" commits before any close of the tenant counts what is pending.
// The signature fits matching's ClosedFunc as is.
func ClosedOn(ctx context.Context, tx pgx.Tx, dates []time.Time) (bool, error) {
	_, ok, err := ClosedPeriodOf(ctx, tx, dates)
	return ok, err
}

// ClosedPeriodOf returns the earliest closed period any of the civil dates
// falls in, so a quarantine can name the range. Same transaction and lock
// as ClosedOn.
func ClosedPeriodOf(ctx context.Context, tx pgx.Tx, dates []time.Time) (Period, bool, error) {
	if len(dates) == 0 {
		return Period{}, false, nil
	}
	q := store.New(tx)
	if err := q.LockShared(ctx); err != nil {
		return Period{}, false, err
	}
	civ := make([]time.Time, len(dates))
	for i, d := range dates {
		civ[i] = civil(d)
	}
	r, err := q.ClosedPeriodOf(ctx, civ)
	if errors.Is(err, pgx.ErrNoRows) {
		return Period{}, false, nil
	}
	if err != nil {
		return Period{}, false, err
	}
	return Period{Start: civil(r.PeriodStart), End: civil(r.PeriodEnd), Cadence: Cadence(r.Cadence)}, true, nil
}

// LastOver is the latest period of the cadence that is over at now, in
// Argentina's time: the one the job marks to close.
func LastOver(now time.Time, c Cadence) Period {
	today := civil(now.In(argentina))
	cur := PeriodOf(today, c)
	return PeriodOf(cur.Start.AddDate(0, 0, -1), c)
}

// MarkDue marks period p of the tenant in scope as one to close ("para
// cerrar"). It never closes, and refuses a period that is not over. It
// reports whether the mark is new.
func (s *Service) MarkDue(ctx context.Context, p Period) (bool, error) {
	if !p.OverAt(s.now()) {
		return false, ErrNotOver
	}
	var n int64
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		var err error
		n, err = store.New(tx).MarkDue(ctx, store.MarkDueParams{Cadence: string(p.Cadence), PeriodStart: p.Start, PeriodEnd: p.End})
		return err
	})
	return n == 1, err
}

// PendingDue lists the periods of the tenant in scope marked to close and
// not closed yet, oldest first.
func (s *Service) PendingDue(ctx context.Context) ([]Due, error) {
	var out []Due
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		rows, err := store.New(tx).PendingDue(ctx)
		if err != nil {
			return err
		}
		out = make([]Due, 0, len(rows))
		for _, r := range rows {
			out = append(out, Due{Period: Period{Start: civil(r.PeriodStart), End: civil(r.PeriodEnd), Cadence: Cadence(r.Cadence)},
				MarkedAt: r.MarkedAt})
		}
		return nil
	})
	return out, err
}

func toClose(id uuid.UUID, cadence string, start, end, closedAt time.Time, by uuid.UUID, summary []byte) (Close, error) {
	c := Close{ID: id, Period: Period{Start: civil(start), End: civil(end), Cadence: Cadence(cadence)},
		ClosedAt: closedAt, ClosedBy: by}
	if err := json.Unmarshal(summary, &c.Summary); err != nil {
		return Close{}, fmt.Errorf("closes: summary of %s: %w", id, err)
	}
	if c.Summary == nil {
		c.Summary = []Metric{}
	}
	return c, nil
}
