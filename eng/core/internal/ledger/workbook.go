package ledger

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// parseWorkbook reads every sheet of an .xlsx export and returns one
// statement. The rules, in doubt quarantine:
//
//   - a sheet whose header fingerprint is known is a data sheet;
//   - a sheet with no known header is ignored only when no row of it looks
//     like a movement (a date next to an amount): a cover or a summary.
//     Otherwise it is an unknown layout when no sheet is known, and an
//     unsupported workbook when other sheets are (a movement must never be
//     left out silently);
//   - every data sheet has the same layout, and the same stated account;
//   - data sheets are put in date order and must chain: each one opens at
//     the balance the previous one closed with, and none starts before the
//     previous one ends (a shared boundary day is allowed);
//   - the result is one statement, checked like any other afterwards.
func parseWorkbook(data []byte) parsed {
	sheets, err := readWorkbook(data)
	if err != nil {
		if errors.Is(err, ErrUnsupportedFormat) {
			return parsed{reason: ReasonUnsupportedFormat, detail: err.Error()}
		}
		return parsed{reason: ReasonRowParse, detail: err.Error()}
	}
	type part struct {
		no int
		st *Statement
	}
	var parts []part
	var layout *Layout
	unknown := parsed{} // first sheet with movement-like rows under no known header
	unknownWidth := -1  // widest unknown header, when no sheet looks like data
	var fallback parsed
	for i, s := range sheets {
		no := i + 1
		var l *Layout
		fp := ""
		if s.Header >= 0 {
			fp = Fingerprint(SheetDelimiter, s.Rows[s.Header].Cells)
			l = registry[fp]
		}
		if l == nil {
			if movementLike(s) && unknown.reason == "" {
				if fp == "" {
					unknown = parsed{reason: ReasonUnknownLayout,
						detail: fmt.Sprintf("sheet %d has rows that look like movements and no header", no)}
				} else {
					unknown = unknownLayout(no, fp)
				}
			}
			if s.Header >= 0 && len(s.Rows[s.Header].Cells) > unknownWidth {
				unknownWidth = len(s.Rows[s.Header].Cells)
				fallback = unknownLayout(no, fp)
			}
			continue
		}
		if layout != nil && l != layout {
			return parsed{reason: ReasonUnsupportedFormat,
				detail: fmt.Sprintf("sheets %d and %d have different layouts", parts[0].no, no)}
		}
		layout = l
		p := parseSheet(s, l, fp, no)
		if p.reason != "" {
			return p
		}
		parts = append(parts, part{no, p.st})
	}
	if len(parts) == 0 {
		switch {
		case unknown.reason != "":
			return unknown
		case fallback.reason != "":
			return fallback
		}
		return parsed{reason: ReasonUnknownLayout, detail: "no sheet has a header"}
	}
	if unknown.reason != "" {
		return parsed{reason: ReasonUnsupportedFormat, fingerprint: unknown.fingerprint,
			detail: unknown.detail + "; the other sheets are a known layout, so the file is held whole"}
	}

	// A data sheet with no movements and no stated balances adds nothing.
	kept := parts[:0]
	for _, p := range parts {
		if len(p.st.Movements) > 0 || p.st.OpeningStated || p.st.ClosingStated {
			kept = append(kept, p)
		}
	}
	if len(kept) == 0 {
		kept = parts[:1]
	}
	parts = kept
	slices.SortStableFunc(parts, func(a, b part) int {
		if c := a.st.From.Compare(b.st.From); c != 0 {
			return c
		}
		return a.st.To.Compare(b.st.To)
	})

	out := *parts[0].st
	out.Movements = slices.Clone(parts[0].st.Movements)
	for k, p := range parts[1:] {
		prev := parts[k]
		if p.st.From.Before(out.To) {
			return parsed{reason: ReasonUnsupportedFormat, detail: fmt.Sprintf(
				"sheets %d and %d overlap in dates (%s to %s and %s to %s)", prev.no, p.no,
				day(prev.st.From), day(prev.st.To), day(p.st.From), day(p.st.To))}
		}
		if out.AccountNumber != "" && p.st.AccountNumber != "" && !sameAccount(out.AccountNumber, p.st.AccountNumber) {
			return parsed{reason: ReasonUnsupportedFormat,
				detail: fmt.Sprintf("sheets %d and %d state different accounts", prev.no, p.no)}
		}
		if p.st.Opening != out.Closing {
			return parsed{reason: ReasonBalance, detail: fmt.Sprintf(
				"sheet %d opens on %s at %s, but sheet %d closed on %s at %s (difference %s)",
				p.no, day(p.st.From), p.st.Opening, prev.no, day(prev.st.To), out.Closing,
				p.st.Opening-out.Closing)}
		}
		if out.AccountNumber == "" {
			out.AccountNumber = p.st.AccountNumber
		}
		out.Movements = append(out.Movements, p.st.Movements...)
		out.To = p.st.To
		out.Closing, out.ClosingStated = p.st.Closing, p.st.ClosingStated
		out.Running = out.Running && p.st.Running
	}
	return parsed{st: &out}
}

// movementLike reports whether any row of the sheet has a date cell next to
// an amount cell: what a movement looks like whatever the header says.
func movementLike(s *sheet) bool {
	for _, r := range s.Rows {
		date, amount := false, false
		for i, c := range r.Cells {
			if c == "" {
				continue
			}
			if _, err := parseDate(c); err == nil {
				date = true
				continue
			}
			if i < len(r.Num) && r.Num[i] {
				amount = true
				continue
			}
			if _, err := money.Parse(c, money.DecimalComma); err == nil {
				amount = true
			}
		}
		if date && amount {
			return true
		}
	}
	return false
}

func day(t time.Time) string { return t.Format("02/01/2006") }
