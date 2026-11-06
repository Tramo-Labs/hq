// Package closes is the period close of a tenant: an operator freezes a
// period once its Suggested tray is empty, and the close keeps the summary
// the client sees (spec §6, guide §2.4, D-6).
package closes

import (
	"errors"
	"time"
)

// Cadence is how a tenant's time is cut into periods. A parameter (D-6
// says fortnightly; Martín may change it per tenant).
type Cadence string

// Fortnight is the calendar fortnight: the 1st to the 15th and the 16th to
// the month's last day. The default (lead-2): aligned with the month, so a
// month is always two whole periods.
const Fortnight Cadence = "fortnight"

// DefaultCadence is the cadence of a tenant that chose none.
const DefaultCadence = Fortnight

// ErrInvalidPeriod: a date where no period of the cadence starts, or an
// unknown cadence.
var ErrInvalidPeriod = errors.New("not the start of a period")

// Period is a half open range of civil dates [Start, End), at midnight UTC
// (dates, not instants: a movement's date is a bank date).
type Period struct {
	Start, End time.Time
	Cadence    Cadence
}

// LastDay is the period's last date, included.
func (p Period) LastDay() time.Time { return p.End.AddDate(0, 0, -1) }

// Contains reports whether the civil date d falls in the period.
func (p Period) Contains(d time.Time) bool {
	d = civil(d)
	return !d.Before(p.Start) && d.Before(p.End)
}

// argentina is the clock periods end by: the clients are in Argentina, and
// a fortnight is over when its last day is over there, not in UTC.
var argentina = time.FixedZone("ART", -3*3600)

// OverAt reports whether the period's last day has ended at t, in
// Argentina's time.
func (p Period) OverAt(t time.Time) bool {
	return !civil(t.In(argentina)).Before(p.End)
}

// PeriodOf returns the period of cadence c the date d falls in. An unknown
// cadence is taken as the default.
func PeriodOf(d time.Time, c Cadence) Period {
	d = civil(d)
	y, m, day := d.Date()
	switch c {
	default: // Fortnight
		if day <= 15 {
			return Period{Start: date(y, m, 1), End: date(y, m, 16), Cadence: Fortnight}
		}
		return Period{Start: date(y, m, 16), End: date(y, m+1, 1), Cadence: Fortnight}
	}
}

// PeriodStarting returns the period of cadence c that starts on d, or
// ErrInvalidPeriod when none does or the cadence is unknown.
func PeriodStarting(d time.Time, c Cadence) (Period, error) {
	if c != Fortnight {
		return Period{}, ErrInvalidPeriod
	}
	p := PeriodOf(d, c)
	if !p.Start.Equal(civil(d)) {
		return Period{}, ErrInvalidPeriod
	}
	return p, nil
}

// civil keeps the calendar date of t, at midnight UTC.
func civil(t time.Time) time.Time {
	y, m, d := t.Date()
	return date(y, m, d)
}

func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
