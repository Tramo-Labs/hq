package closes

import (
	"testing"
	"time"
)

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

// Calendar fortnights (lead-2, D-6): the 1st to the 15th and the 16th to
// the month's last day, whatever its length.
func TestFortnightPeriods(t *testing.T) {
	cases := []struct{ date, start, end string }{
		{"2026-11-01", "2026-11-01", "2026-11-16"},
		{"2026-11-15", "2026-11-01", "2026-11-16"},
		{"2026-11-16", "2026-11-16", "2026-12-01"},
		{"2026-11-30", "2026-11-16", "2026-12-01"},
		{"2026-12-31", "2026-12-16", "2027-01-01"},
		{"2027-02-28", "2027-02-16", "2027-03-01"},
		{"2028-02-29", "2028-02-16", "2028-03-01"},
	}
	for _, c := range cases {
		p := PeriodOf(day(c.date), Fortnight)
		if !p.Start.Equal(day(c.start)) || !p.End.Equal(day(c.end)) {
			t.Errorf("%s: %s to %s, want %s to %s", c.date, p.Start.Format(time.DateOnly), p.End.Format(time.DateOnly), c.start, c.end)
		}
	}
	if got := PeriodOf(day("2026-11-01"), Fortnight).LastDay(); !got.Equal(day("2026-11-15")) {
		t.Errorf("last day: %s", got.Format(time.DateOnly))
	}
}

// Only the day a period starts on names it.
func TestPeriodStarting(t *testing.T) {
	for _, s := range []string{"2026-11-01", "2026-11-16", "2027-02-16"} {
		if _, err := PeriodStarting(day(s), Fortnight); err != nil {
			t.Errorf("%s refused: %v", s, err)
		}
	}
	for _, s := range []string{"2026-11-02", "2026-11-15", "2026-11-17", "2026-11-30"} {
		if _, err := PeriodStarting(day(s), Fortnight); err == nil {
			t.Errorf("%s accepted as a period start", s)
		}
	}
	if _, err := PeriodStarting(day("2026-11-01"), Cadence("weekly")); err == nil {
		t.Error("an unknown cadence was accepted")
	}
}

// A period is over once its last day has passed, in Argentina's time.
func TestPeriodOver(t *testing.T) {
	p := PeriodOf(day("2026-11-01"), Fortnight)
	art := time.FixedZone("ART", -3*3600)
	if p.OverAt(time.Date(2026, 11, 15, 23, 59, 0, 0, art)) {
		t.Error("over before its last day ended")
	}
	if !p.OverAt(time.Date(2026, 11, 16, 0, 0, 0, 0, art)) {
		t.Error("not over the day after")
	}
	// 01:00 UTC on the 16th is still the 15th in Argentina.
	if p.OverAt(time.Date(2026, 11, 16, 1, 0, 0, 0, time.UTC)) {
		t.Error("over by UTC while still the 15th in Argentina")
	}
}
