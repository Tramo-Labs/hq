package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// row is one record of a delimited file with the line where it starts.
type row struct {
	Line  int
	Cells []string
}

// sheet is a file split into rows with a detected header.
type sheet struct {
	Delimiter rune
	Rows      []row
	Header    int // index in Rows of the header row, -1 if none
	// Broken is the first line the CSV reader could not split (a stray or
	// unclosed quote). Rows stops before it: a broken sheet is never read
	// as a whole statement.
	Broken *csv.ParseError
}

// decodeText strips a UTF-8 BOM and converts Latin-1 (what most home
// banking exports in Argentina use) to UTF-8 when the bytes are not UTF-8.
func decodeText(b []byte) string {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if utf8.Valid(b) {
		return string(b)
	}
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

func readSheet(text string, delim rune) (*sheet, error) {
	r := csv.NewReader(strings.NewReader(text))
	r.Comma = delim
	r.FieldsPerRecord = -1
	r.ReuseRecord = false
	s := &sheet{Delimiter: delim, Header: -1}
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		var pe *csv.ParseError
		if errors.As(err, &pe) {
			s.Broken = pe
			break
		}
		if err != nil {
			return nil, err
		}
		line, _ := r.FieldPos(0)
		cells := make([]string, len(rec))
		for i, c := range rec {
			cells[i] = strings.TrimSpace(c)
		}
		s.Rows = append(s.Rows, row{Line: line, Cells: cells})
	}
	s.Header = headerCandidate(s.Rows)
	return s, nil
}

// headerCandidate returns the first row, among the first 30, that looks like
// a header: at least three non-empty cells and none of them a date or an
// amount. Preamble lines ("Saldo inicial;1.000,00") never qualify.
func headerCandidate(rows []row) int {
	for i, r := range rows {
		if i >= 30 {
			break
		}
		n := 0
		ok := true
		for _, c := range r.Cells {
			if c == "" {
				continue
			}
			n++
			if looksLikeValue(c) {
				ok = false
				break
			}
		}
		if ok && n >= 3 {
			return i
		}
	}
	return -1
}

func looksLikeValue(c string) bool {
	if _, err := parseDate(c); err == nil {
		return true
	}
	if _, err := money.Parse(c, money.DecimalComma); err == nil {
		return true
	}
	if _, err := money.Parse(c, money.DecimalDot); err == nil {
		return true
	}
	return false
}

// normalizeHeader lowercases, strips accents and collapses spaces so that a
// cosmetic re-encoding of the same header keeps the same fingerprint.
func normalizeHeader(c string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(strings.TrimSpace(c)) {
		r = foldAccent(r)
		if unicode.IsSpace(r) {
			if !space {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func foldAccent(r rune) rune {
	switch r {
	case 'á', 'à', 'ä', 'â':
		return 'a'
	case 'é', 'è', 'ë', 'ê':
		return 'e'
	case 'í', 'ì', 'ï', 'î':
		return 'i'
	case 'ó', 'ò', 'ö', 'ô':
		return 'o'
	case 'ú', 'ù', 'ü', 'û':
		return 'u'
	case 'ñ':
		return 'n'
	case '°', 'º':
		return 'o'
	}
	return r
}

// Fingerprint identifies a layout by its delimiter and its normalized header.
// Any change in the columns (added, removed, renamed, reordered) changes it.
func Fingerprint(delim rune, header []string) string {
	norm := make([]string, 0, len(header))
	for _, h := range header {
		norm = append(norm, normalizeHeader(h))
	}
	for len(norm) > 0 && norm[len(norm)-1] == "" {
		norm = norm[:len(norm)-1]
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%q|%s", delim, strings.Join(norm, "|"))))
	return hex.EncodeToString(sum[:8])
}

var argentina = time.FixedZone("ART", -3*3600)

// parseDate accepts the civil date formats seen in exports. Timestamps are
// taken to Argentina time before keeping the date.
func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, l := range []string{"02/01/2006", "2006-01-02", "02-01-2006"} {
		if t, err := time.Parse(l, s); err == nil {
			return civil(t), nil
		}
	}
	if len(s) == 8 && s[2] == '/' && s[5] == '/' {
		if t, err := time.Parse("02/01/06", s); err == nil {
			return civil(t), nil
		}
	}
	for _, l := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000-07:00", "2006-01-02T15:04:05-07:00"} {
		if t, err := time.Parse(l, s); err == nil {
			return civil(t.In(argentina)), nil
		}
	}
	return time.Time{}, fmt.Errorf("date %q not recognized", s)
}

// cell returns the cell at i, or "" when the row is shorter.
func (r row) cell(i int) string {
	if i < 0 || i >= len(r.Cells) {
		return ""
	}
	return r.Cells[i]
}

func (r row) blank() bool {
	for _, c := range r.Cells {
		if c != "" {
			return false
		}
	}
	return true
}
