package ledger

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// Excel workbooks (.xlsx) are read with the standard library only: a
// workbook is a zip of XML parts. Each worksheet becomes a sheet with the
// same rows a CSV export would give, so layouts, fingerprints and checks are
// the same. Two things differ from text:
//
//   - a numeric cell is rendered as a plain decimal with a dot ("1234.50")
//     and marked numeric, so the parser reads it in that style whatever the
//     layout's text style is; amounts typed as text ("1.234,50") keep the
//     layout's style;
//   - a numeric cell with a date format is rendered as "dd/mm/yyyy".
//
// A number that is not a whole number of cents (Excel float noise below a
// millionth of a cent is tolerated) is an error on its cell, never rounded.

// SheetDelimiter is the delimiter of a layout read from a workbook sheet.
// It only takes part in the fingerprint, so an Excel layout never collides
// with a CSV layout of the same columns.
const SheetDelimiter rune = 0

const (
	// maxPartSize bounds each decompressed XML part: a 20 MiB upload can
	// expand much more, and a zip bomb stops here.
	maxPartSize = 64 << 20
	maxSheets   = 50
	maxColumn   = 16383 // XFD, the last column Excel has
)

var (
	zipMagic = []byte("PK\x03\x04")
	oleMagic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}
)

// ErrUnsupportedFormat means the bytes are a container the ledger does not
// read (Excel 97-2003, a zip that is not a workbook) or a workbook whose
// sheets cannot be told apart safely.
var ErrUnsupportedFormat = errors.New("unsupported format")

func isWorkbook(b []byte) bool  { return bytes.HasPrefix(b, zipMagic) }
func isLegacyXLS(b []byte) bool { return bytes.HasPrefix(b, oleMagic) }

// readWorkbook returns one sheet per worksheet, in workbook order.
func readWorkbook(data []byte) ([]*sheet, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: not a readable zip", ErrUnsupportedFormat)
	}
	parts := map[string]*zip.File{}
	for _, f := range zr.File {
		parts[f.Name] = f
	}
	if parts["xl/workbook.xml"] == nil {
		return nil, fmt.Errorf("%w: a zip without a workbook", ErrUnsupportedFormat)
	}
	wb, err := readWorkbookXML(parts)
	if err != nil {
		return nil, err
	}
	if len(wb.sheets) > maxSheets {
		return nil, fmt.Errorf("%w: %d sheets", ErrUnsupportedFormat, len(wb.sheets))
	}
	shared, err := readSharedStrings(parts)
	if err != nil {
		return nil, err
	}
	st, err := readStyles(parts)
	if err != nil {
		return nil, err
	}
	var out []*sheet
	for _, target := range wb.sheets {
		f := parts[target]
		if f == nil {
			return nil, fmt.Errorf("%w: a sheet part is missing", ErrUnsupportedFormat)
		}
		s, err := readWorksheet(f, shared, st, wb.date1904)
		if err != nil {
			return nil, sheetError(len(out)+1, err)
		}
		out = append(out, s)
	}
	return out, nil
}

func openPart(f *zip.File) ([]byte, error) {
	if f.UncompressedSize64 > maxPartSize {
		return nil, fmt.Errorf("%w: a part is too large", ErrUnsupportedFormat)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedFormat, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, maxPartSize+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedFormat, err)
	}
	if len(b) > maxPartSize {
		return nil, fmt.Errorf("%w: a part is too large", ErrUnsupportedFormat)
	}
	return b, nil
}

type workbookInfo struct {
	sheets   []string // part names of the worksheets, in workbook order
	date1904 bool
}

func readWorkbookXML(parts map[string]*zip.File) (workbookInfo, error) {
	var info workbookInfo
	b, err := openPart(parts["xl/workbook.xml"])
	if err != nil {
		return info, err
	}
	var wb struct {
		Pr struct {
			Date1904 string `xml:"date1904,attr"`
		} `xml:"workbookPr"`
		Sheets []struct {
			RID string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(b, &wb); err != nil {
		return info, fmt.Errorf("%w: workbook: %v", ErrUnsupportedFormat, err)
	}
	info.date1904 = wb.Pr.Date1904 == "1" || wb.Pr.Date1904 == "true"
	rels := map[string]string{}
	if f := parts["xl/_rels/workbook.xml.rels"]; f != nil {
		b, err := openPart(f)
		if err != nil {
			return info, err
		}
		var r struct {
			Rel []struct {
				ID     string `xml:"Id,attr"`
				Target string `xml:"Target,attr"`
			} `xml:"Relationship"`
		}
		if err := xml.Unmarshal(b, &r); err != nil {
			return info, fmt.Errorf("%w: workbook relations: %v", ErrUnsupportedFormat, err)
		}
		for _, x := range r.Rel {
			t := x.Target
			if strings.HasPrefix(t, "/") {
				t = strings.TrimPrefix(t, "/")
			} else {
				t = path.Join("xl", t)
			}
			rels[x.ID] = t
		}
	}
	for _, s := range wb.Sheets {
		t, ok := rels[s.RID]
		if !ok {
			return info, fmt.Errorf("%w: a sheet without its part", ErrUnsupportedFormat)
		}
		info.sheets = append(info.sheets, t)
	}
	return info, nil
}

func readSharedStrings(parts map[string]*zip.File) ([]string, error) {
	f := parts["xl/sharedStrings.xml"]
	if f == nil {
		return nil, nil
	}
	b, err := openPart(f)
	if err != nil {
		return nil, err
	}
	var sst struct {
		SI []richText `xml:"si"`
	}
	if err := xml.Unmarshal(b, &sst); err != nil {
		return nil, fmt.Errorf("%w: shared strings: %v", ErrUnsupportedFormat, err)
	}
	out := make([]string, len(sst.SI))
	for i, si := range sst.SI {
		out[i] = si.String()
	}
	return out, nil
}

// richText is a string item: a plain <t> or runs <r><t>. Phonetic runs
// (rPh) are not part of the text.
type richText struct {
	T string `xml:"t"`
	R []struct {
		T string `xml:"t"`
	} `xml:"r"`
}

func (r richText) String() string {
	if len(r.R) == 0 {
		return r.T
	}
	var b strings.Builder
	b.WriteString(r.T)
	for _, x := range r.R {
		b.WriteString(x.T)
	}
	return b.String()
}

// styles says, for each cell style index, whether its number format is a date.
type styles []bool

func readStyles(parts map[string]*zip.File) (styles, error) {
	f := parts["xl/styles.xml"]
	if f == nil {
		return nil, nil
	}
	b, err := openPart(f)
	if err != nil {
		return nil, err
	}
	var ss struct {
		NumFmts []struct {
			ID   int    `xml:"numFmtId,attr"`
			Code string `xml:"formatCode,attr"`
		} `xml:"numFmts>numFmt"`
		Xfs []struct {
			NumFmtID int `xml:"numFmtId,attr"`
		} `xml:"cellXfs>xf"`
	}
	if err := xml.Unmarshal(b, &ss); err != nil {
		return nil, fmt.Errorf("%w: styles: %v", ErrUnsupportedFormat, err)
	}
	custom := map[int]string{}
	for _, n := range ss.NumFmts {
		custom[n.ID] = n.Code
	}
	out := make(styles, len(ss.Xfs))
	for i, x := range ss.Xfs {
		if code, ok := custom[x.NumFmtID]; ok {
			out[i] = isDateFormat(code)
		} else {
			out[i] = builtinDate(x.NumFmtID)
		}
	}
	return out, nil
}

func (s styles) isDate(i int) bool { return i >= 0 && i < len(s) && s[i] }

// builtinDate: the built-in number formats that show a date (ECMA-376,
// 18.8.30). 45 to 47 are times only and are not dates.
func builtinDate(id int) bool { return id >= 14 && id <= 22 }

// isDateFormat reports whether a custom format code shows a date: a day,
// month or year token outside quoted text, escapes and brackets.
func isDateFormat(code string) bool {
	inQuote, inBracket := false, false
	for i := 0; i < len(code); i++ {
		c := code[i]
		switch {
		case inQuote:
			inQuote = c != '"'
		case inBracket:
			inBracket = c != ']'
		case c == '"':
			inQuote = true
		case c == '[':
			inBracket = true
		case c == '\\' || c == '_' || c == '*':
			i++
		case c == 'd' || c == 'D' || c == 'y' || c == 'Y' || c == 'm' || c == 'M':
			return true
		}
	}
	return false
}

type xmlCell struct {
	Ref string   `xml:"r,attr"`
	T   string   `xml:"t,attr"`
	S   int      `xml:"s,attr"`
	V   string   `xml:"v"`
	IS  richText `xml:"is"`
}

func readWorksheet(f *zip.File, shared []string, st styles, date1904 bool) (*sheet, error) {
	b, err := openPart(f)
	if err != nil {
		return nil, err
	}
	var ws struct {
		Rows []struct {
			R     int       `xml:"r,attr"`
			Cells []xmlCell `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err := xml.Unmarshal(b, &ws); err != nil {
		return nil, fmt.Errorf("%w: worksheet: %v", ErrUnsupportedFormat, err)
	}
	s := &sheet{Delimiter: SheetDelimiter, Header: -1}
	next := 1
	for _, xr := range ws.Rows {
		line := xr.R
		if line == 0 {
			line = next
		}
		if line < next {
			return nil, &RowError{Line: line, Column: "", Err: errors.New("rows out of order")}
		}
		next = line + 1
		r := row{Line: line}
		for _, c := range xr.Cells {
			col := len(r.Cells)
			if c.Ref != "" {
				if col, err = columnIndex(c.Ref); err != nil {
					return nil, &RowError{Line: line, Column: "", Err: err}
				}
			}
			if col < len(r.Cells) {
				return nil, &RowError{Line: line, Column: c.Ref, Err: errors.New("cells out of order")}
			}
			if col > maxColumn {
				return nil, &RowError{Line: line, Column: c.Ref, Err: errors.New("column beyond the sheet")}
			}
			for len(r.Cells) < col {
				r.Cells = append(r.Cells, "")
				r.Num = append(r.Num, false)
			}
			text, num, err := cellText(c, shared, st, date1904)
			if err != nil {
				return nil, &RowError{Line: line, Column: c.Ref, Err: err}
			}
			r.Cells = append(r.Cells, strings.TrimSpace(text))
			r.Num = append(r.Num, num)
		}
		// A row Excel keeps only for its formatting has no cells: it counts
		// as missing, like the empty lines a CSV reader skips.
		if len(r.Cells) == 0 {
			continue
		}
		s.Rows = append(s.Rows, r)
	}
	s.Header = headerCandidate(s.Rows)
	return s, nil
}

// columnIndex turns the letters of "C12" into 2.
func columnIndex(ref string) (int, error) {
	n, i := 0, 0
	for ; i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z'; i++ {
		n = n*26 + int(ref[i]-'A'+1)
		if n > maxColumn+1 {
			return 0, fmt.Errorf("column beyond the sheet")
		}
	}
	if i == 0 {
		return 0, fmt.Errorf("cell reference not valid")
	}
	return n - 1, nil
}

// cellText renders one cell. num is true for a numeric value rendered with
// a decimal dot.
func cellText(c xmlCell, shared []string, st styles, date1904 bool) (string, bool, error) {
	v := strings.TrimSpace(c.V)
	switch c.T {
	case "s":
		i, err := strconv.Atoi(v)
		if err != nil || i < 0 || i >= len(shared) {
			return "", false, errors.New("shared string out of range")
		}
		return shared[i], false, nil
	case "inlineStr":
		return c.IS.String(), false, nil
	case "str", "b":
		return c.V, false, nil
	case "e":
		return "", false, errors.New("the cell holds an Excel error")
	case "d":
		// ISO 8601 date cell (strict OOXML); the time, if any, is dropped.
		if len(v) < 10 {
			return "", false, errors.New("date cell not recognized")
		}
		tm, err := time.Parse("2006-01-02", v[:10])
		if err != nil {
			return "", false, errors.New("date cell not recognized")
		}
		return tm.Format("02/01/2006"), false, nil
	case "", "n":
		if v == "" {
			return "", false, nil
		}
		if st.isDate(c.S) {
			d, err := serialDate(v, date1904)
			return d, false, err
		}
		cents, err := numberCents(v)
		if err != nil {
			return "", false, err
		}
		return money.Amount(cents).String(), true, nil
	}
	return "", false, fmt.Errorf("cell type %q not known", c.T)
}

// serialDate converts an Excel date serial to dd/mm/yyyy. The time of day,
// if any, is dropped.
func serialDate(v string, date1904 bool) (string, error) {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) || f < 1 || f > 2958465 {
		return "", errors.New("date serial not valid")
	}
	days := int(math.Floor(f))
	base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	if date1904 {
		base = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	} else if days < 61 {
		// Serials before 1900-03-01 count Excel's fictitious 1900-02-29.
		days++
	}
	return base.AddDate(0, 0, days).Format("02/01/2006"), nil
}

// numberCents reads the decimal text Excel stores and returns cents. A
// value within a millionth of a cent of a whole cent is the float noise of a
// computed cell; anything else has more than two decimals and is refused.
func numberCents(v string) (int64, error) {
	r, ok := new(big.Rat).SetString(v)
	if !ok {
		return 0, errors.New("number not valid")
	}
	r.Mul(r, big.NewRat(100, 1))
	if r.IsInt() {
		if !r.Num().IsInt64() {
			return 0, errors.New("number too large")
		}
		return r.Num().Int64(), nil
	}
	f, _ := r.Float64()
	rounded := math.Round(f)
	if math.Abs(f-rounded) > 1e-6 || math.Abs(rounded) > 1<<53 {
		return 0, errors.New("number with more than two decimals")
	}
	return int64(rounded), nil
}

// sheetError names the sheet, by its position, in a reading error. The
// sheet name is never used: it can carry the holder's name.
func sheetError(n int, err error) error {
	var re *RowError
	if errors.As(err, &re) {
		return &RowError{Line: re.Line, Column: re.Column, Err: re.Err, Sheet: n}
	}
	return fmt.Errorf("sheet %d: %w", n, err)
}
