package ledger

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// Layout is one known export format of one bank. The header is the exact
// header row the bank produces; its fingerprint is what Detect looks up.
type Layout struct {
	ID        string // bank/format-version, stored with each statement
	Bank      Bank
	Delimiter rune
	Header    []string
	// Verified is false while the layout was built from documentation or
	// synthetic samples and not yet from a real export. It is reported, not
	// enforced: an unverified layout still needs the balance check to pass.
	Verified bool
	parse    func(s *sheet, l *Layout) (*Statement, error)
}

// Fingerprint of the layout.
func (l *Layout) Fingerprint() string { return Fingerprint(l.Delimiter, l.Header) }

var registry = map[string]*Layout{}

func register(l *Layout) {
	fp := l.Fingerprint()
	if prev, ok := registry[fp]; ok {
		panic(fmt.Sprintf("layouts %s and %s share fingerprint %s", prev.ID, l.ID, fp))
	}
	registry[fp] = l
}

// Layouts lists the registered layouts sorted by ID.
func Layouts() []*Layout {
	out := make([]*Layout, 0, len(registry))
	for _, l := range registry {
		out = append(out, l)
	}
	slices.SortFunc(out, func(a, b *Layout) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// ErrUnknownLayout means no registered fingerprint matched the file.
var ErrUnknownLayout = errors.New("unknown layout")

// detection is the outcome of looking a file up in the registry.
type detection struct {
	Layout      *Layout // nil when unknown
	Sheet       *sheet
	Fingerprint string   // of the best header candidate, even when unknown
	Header      []string // that candidate, for the quarantine notice
}

// detect tries each delimiter and returns the first sheet whose header
// fingerprint is registered. When none matches it returns the candidate with
// the widest header so the quarantine shows what the file looked like.
func detect(text string) detection {
	var best detection
	for _, d := range []rune{';', ',', '\t'} {
		s, err := readSheet(text, d)
		if err != nil || s.Header < 0 {
			continue
		}
		h := s.Rows[s.Header].Cells
		fp := Fingerprint(d, h)
		if l, ok := registry[fp]; ok {
			return detection{Layout: l, Sheet: s, Fingerprint: fp, Header: h}
		}
		if len(h) > len(best.Header) {
			best = detection{Sheet: s, Fingerprint: fp, Header: h}
		}
	}
	return best
}

// RowError is a row that does not fit its layout. One bad row stops the file.
type RowError struct {
	Line   int
	Column string
	Err    error
}

func (e *RowError) Error() string {
	return fmt.Sprintf("line %d, column %q: %v", e.Line, e.Column, e.Err)
}

func (e *RowError) Unwrap() error { return e.Err }

// columns describes a row oriented layout where each movement is one row.
type columns struct {
	Date, Desc, Ref     int
	Debit, Credit       int // separate unsigned columns, -1 if the layout uses Signed
	Signed              int // one signed column, -1 if the layout uses Debit and Credit
	Balance             int // running balance, -1 if absent
	Style               money.Style
	NewestFirst         bool
	StopAtLabel         []string // first cell of trailer rows that end the data
}

// parseColumns reads the data rows after the header until a blank row or a
// trailer label. It returns the movements in chronological order and the
// rows that followed the data, for the layout to read its trailer.
func parseColumns(s *sheet, c columns) ([]Movement, []row, error) {
	var out []Movement
	rows := s.Rows[s.Header+1:]
	header := s.Rows[s.Header].Cells
	name := func(i int) string {
		if i >= 0 && i < len(header) {
			return header[i]
		}
		return fmt.Sprint(i)
	}
	i := 0
	for ; i < len(rows); i++ {
		r := rows[i]
		if r.blank() || slices.Contains(c.StopAtLabel, normalizeHeader(r.cell(0))) {
			break
		}
		if len(r.Cells) < len(header) {
			return nil, nil, &RowError{r.Line, "", fmt.Errorf("%d cells, header has %d", len(r.Cells), len(header))}
		}
		d, err := parseDate(r.cell(c.Date))
		if err != nil {
			return nil, nil, &RowError{r.Line, name(c.Date), err}
		}
		m := Movement{Line: r.Line, Date: d, Description: r.cell(c.Desc), Reference: r.cell(c.Ref)}
		if c.Signed >= 0 {
			a, err := money.Parse(r.cell(c.Signed), c.Style)
			if err != nil {
				return nil, nil, &RowError{r.Line, name(c.Signed), err}
			}
			m.Amount = a
		} else {
			deb, errD := optional(r.cell(c.Debit), c.Style)
			if errD != nil {
				return nil, nil, &RowError{r.Line, name(c.Debit), errD}
			}
			cred, errC := optional(r.cell(c.Credit), c.Style)
			if errC != nil {
				return nil, nil, &RowError{r.Line, name(c.Credit), errC}
			}
			if (deb == 0) == (cred == 0) {
				return nil, nil, &RowError{r.Line, name(c.Debit), errors.New("exactly one of debit and credit must be set")}
			}
			if deb < 0 || cred < 0 {
				return nil, nil, &RowError{r.Line, name(c.Debit), errors.New("debit and credit columns must be unsigned")}
			}
			m.Amount = cred - deb
		}
		if c.Balance >= 0 {
			b, err := money.Parse(r.cell(c.Balance), c.Style)
			if err != nil {
				return nil, nil, &RowError{r.Line, name(c.Balance), err}
			}
			m.Balance = &b
		}
		out = append(out, m)
	}
	if c.NewestFirst {
		slices.Reverse(out)
	}
	return out, rows[i:], nil
}

func optional(s string, st money.Style) (money.Amount, error) {
	a, err := money.Parse(s, st)
	if errors.Is(err, money.ErrEmpty) {
		return 0, nil
	}
	return a, err
}

// finish fills the derived fields of a statement parsed from a running
// balance column.
func finish(st *Statement, running bool) {
	st.Running = running && len(st.Movements) > 0
	if len(st.Movements) > 0 {
		st.From = st.Movements[0].Date
		st.To = st.Movements[len(st.Movements)-1].Date
	}
	if st.Running {
		first := st.Movements[0]
		last := st.Movements[len(st.Movements)-1]
		if !st.OpeningStated {
			st.Opening = *first.Balance - first.Amount
		}
		if !st.ClosingStated {
			st.Closing = *last.Balance
		}
	}
	if st.Currency == "" {
		st.Currency = "ARS"
	}
}
