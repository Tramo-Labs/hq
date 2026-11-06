package listrange

import (
	"errors"
	"testing"
	"time"
)

func day(s string) *time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return &t
}

func str(s string) *string { return &s }

func TestParse(t *testing.T) {
	ok := []struct {
		name        string
		period      *string
		from, to    *time.Time
		first, last string // empty: no filter
	}{
		{"nothing", nil, nil, nil, "", ""},
		{"a month", str("2026-02"), nil, nil, "2026-02-01", "2026-02-28"},
		{"a leap February", str("2028-02"), nil, nil, "2028-02-01", "2028-02-29"},
		{"a fortnight", nil, day("2026-11-01"), day("2026-11-15"), "2026-11-01", "2026-11-15"},
		{"one day", nil, day("2026-11-16"), day("2026-11-16"), "2026-11-16", "2026-11-16"},
	}
	for _, c := range ok {
		r, err := Parse(c.period, c.from, c.to)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if c.first == "" {
			if r != nil {
				t.Errorf("%s: %+v; want no filter", c.name, r)
			}
			continue
		}
		if r == nil || r.From.Format("2006-01-02") != c.first || r.Last.Format("2006-01-02") != c.last {
			t.Errorf("%s: %+v; want %s to %s", c.name, r, c.first, c.last)
		}
	}

	bad := []struct {
		name     string
		period   *string
		from, to *time.Time
	}{
		{"from without to", nil, day("2026-11-01"), nil},
		{"to without from", nil, nil, day("2026-11-15")},
		{"period and range", str("2026-11"), day("2026-11-01"), day("2026-11-15")},
		{"period and from", str("2026-11"), day("2026-11-01"), nil},
		{"to before from", nil, day("2026-11-15"), day("2026-11-01")},
		{"not a month", str("noviembre"), nil, nil},
	}
	for _, c := range bad {
		if _, err := Parse(c.period, c.from, c.to); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v; want ErrInvalid", c.name, err)
		}
	}
}

// A time of day or a zone does not move the civil date.
func TestParseKeepsTheCivilDate(t *testing.T) {
	ar := time.FixedZone("ART", -3*3600)
	from := time.Date(2026, 11, 1, 23, 30, 0, 0, ar)
	to := time.Date(2026, 11, 15, 0, 10, 0, 0, ar)
	r, err := Parse(nil, &from, &to)
	if err != nil {
		t.Fatal(err)
	}
	if r.From != time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC) || r.Last != time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC) {
		t.Errorf("%+v", r)
	}
	var none *Range
	if f, l := none.Bounds(); f != nil || l != nil {
		t.Error("no filter has bounds")
	}
}
