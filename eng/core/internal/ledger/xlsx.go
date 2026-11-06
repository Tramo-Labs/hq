package ledger

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/binary"
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
//
// Parts are streamed, never read whole, and every part is charged against
// one budget for the workbook (see the limits below).

// SheetDelimiter is the delimiter of a layout read from a workbook sheet.
// It only takes part in the fingerprint, so an Excel layout never collides
// with a CSV layout of the same columns.
const SheetDelimiter rune = 0

// Limits on what a workbook may hold. A 20 MiB upload can expand a
// thousand times over, so the limits the zip can state (directory size,
// entry count, declared part sizes) are checked before anything is
// decompressed, and every limit is enforced again while reading, since a zip
// can lie about sizes. A file over any limit is an unsupported format and
// goes to quarantine.
const (
	xlsxMaxDirectory     = 1 << 20  // bytes of the zip central directory
	xlsxMaxEntries       = 1000     // entries in the zip
	xlsxMaxXML           = 64 << 20 // decompressed XML read, all parts together
	xlsxMaxToken         = 1 << 20  // one XML token: a tag, a text run, a comment
	xlsxMaxDepth         = 32       // element nesting
	xlsxMaxCellText      = 128 << 10
	maxSheets            = 50
	xlsxMaxRelations     = 4096
	xlsxMaxRows          = 200_000   // rows with cells, all sheets together
	xlsxMaxCells         = 1_000_000 // cells, all sheets together, padding included
	xlsxMaxSharedStrings = 500_000
	xlsxMaxStyles        = 65_536 // cell formats (Excel's own limit is 64,000)
	xlsxMaxNumFmts       = 4096
	maxColumn            = 16383 // XFD, the last column Excel has
)

var (
	zipMagic = []byte("PK\x03\x04")
	oleMagic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}
)

// ErrUnsupportedFormat means the bytes are a container the ledger does not
// read (Excel 97-2003, a zip that is not a workbook), a workbook over the
// size limits, or one whose sheets cannot be told apart safely.
var ErrUnsupportedFormat = errors.New("unsupported format")

func isWorkbook(b []byte) bool  { return bytes.HasPrefix(b, zipMagic) }
func isLegacyXLS(b []byte) bool { return bytes.HasPrefix(b, oleMagic) }

func tooLarge(what string) error {
	return fmt.Errorf("%w: the workbook is over the limit of %s", ErrUnsupportedFormat, what)
}

// budget is what is left to read of the workbook as a whole.
type budget struct {
	xml   int64
	rows  int
	cells int
}

// readWorkbook returns one sheet per worksheet, in workbook order.
func readWorkbook(data []byte) ([]*sheet, error) {
	if err := checkDirectory(data); err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: not a readable zip", ErrUnsupportedFormat)
	}
	if len(zr.File) > xlsxMaxEntries {
		return nil, tooLarge("zip entries")
	}
	// Every entry counts, whatever its name: a sheet part can be called
	// anything. Images barely compress, so they stay near their zipped size.
	// The zip reader refuses a part that decompresses past its declared size.
	parts := map[string]*zip.File{}
	var declared uint64
	for _, f := range zr.File {
		parts[f.Name] = f
		declared += f.UncompressedSize64
	}
	if declared > xlsxMaxXML {
		return nil, tooLarge("decompressed size")
	}
	if parts["xl/workbook.xml"] == nil {
		return nil, fmt.Errorf("%w: a zip without a workbook", ErrUnsupportedFormat)
	}
	b := &budget{xml: xlsxMaxXML, rows: xlsxMaxRows, cells: xlsxMaxCells}
	wb, err := readWorkbookXML(parts, b)
	if err != nil {
		return nil, err
	}
	shared, err := readSharedStrings(parts, b)
	if err != nil {
		return nil, err
	}
	st, err := readStyles(parts, b)
	if err != nil {
		return nil, err
	}
	var out []*sheet
	for _, target := range wb.sheets {
		f := parts[target]
		if f == nil {
			return nil, fmt.Errorf("%w: a sheet part is missing", ErrUnsupportedFormat)
		}
		s, err := readWorksheet(f, b, shared, st, wb.date1904)
		if err != nil {
			return nil, sheetError(len(out)+1, err)
		}
		out = append(out, s)
	}
	return out, nil
}

// checkDirectory bounds the zip central directory before the zip reader
// sees it: the reader keeps one entry per directory record, so a directory
// of hundreds of thousands of empty entries costs memory on its own. The
// end record must sit right after the directory (no prepended data, no
// zip64), which is how spreadsheet programs write a workbook.
func checkDirectory(data []byte) error {
	const endLen = 22
	n := len(data)
	end := -1
	for i := n - endLen; i >= max(n-endLen-65535, 0); i-- {
		if data[i] == 'P' && data[i+1] == 'K' && data[i+2] == 5 && data[i+3] == 6 {
			comment := int(binary.LittleEndian.Uint16(data[i+20:]))
			if i+endLen+comment <= n {
				end = i
				break
			}
		}
	}
	if end < 0 {
		return fmt.Errorf("%w: not a readable zip", ErrUnsupportedFormat)
	}
	records := binary.LittleEndian.Uint16(data[end+10:])
	size := binary.LittleEndian.Uint32(data[end+12:])
	offset := binary.LittleEndian.Uint32(data[end+16:])
	switch {
	case records == 0xffff || size == 0xffffffff || offset == 0xffffffff:
		return fmt.Errorf("%w: a zip64 archive", ErrUnsupportedFormat)
	case size > xlsxMaxDirectory || int(records) > xlsxMaxEntries:
		return tooLarge("zip entries")
	case int64(offset)+int64(size) != int64(end):
		return fmt.Errorf("%w: the zip directory is not where its end record says", ErrUnsupportedFormat)
	}
	return nil
}

// partReader feeds one part to the XML decoder a byte at a time, charging
// the workbook budget and refusing a token longer than xlsxMaxToken: the
// decoder buffers a whole token, so this bounds its memory too.
type partReader struct {
	r    *bufio.Reader
	b    *budget
	pos  int64
	mark int64 // where the current token started
	err  error
}

func (p *partReader) ReadByte() (byte, error) {
	switch {
	case p.err != nil:
		return 0, p.err
	case p.b.xml <= 0:
		p.err = tooLarge("decompressed size")
		return 0, p.err
	case p.pos-p.mark >= xlsxMaxToken:
		p.err = tooLarge("one XML token")
		return 0, p.err
	}
	c, err := p.r.ReadByte()
	if err != nil {
		return 0, err
	}
	p.pos++
	p.b.xml--
	return c, nil
}

func (p *partReader) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	c, err := p.ReadByte()
	if err != nil {
		return 0, err
	}
	b[0] = c
	return 1, nil
}

// xmlStream walks one part token by token, keeping the path of local
// names from the root.
type xmlStream struct {
	rc   io.ReadCloser
	pr   *partReader
	d    *xml.Decoder
	path []string
}

func openStream(f *zip.File, b *budget) (*xmlStream, error) {
	if f.UncompressedSize64 > uint64(b.xml) {
		return nil, tooLarge("decompressed size")
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedFormat, err)
	}
	pr := &partReader{r: bufio.NewReaderSize(rc, 32<<10), b: b}
	return &xmlStream{rc: rc, pr: pr, d: xml.NewDecoder(pr)}, nil
}

func (s *xmlStream) close() { s.rc.Close() }

// next returns the next token, io.EOF at the end of the part. Char data is
// only valid until the next call.
func (s *xmlStream) next() (xml.Token, error) {
	t, err := s.d.Token()
	if s.pr.err != nil {
		return nil, s.pr.err
	}
	if err == io.EOF {
		return nil, io.EOF
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedFormat, err)
	}
	s.pr.mark = s.d.InputOffset()
	switch t := t.(type) {
	case xml.StartElement:
		if len(s.path) >= xlsxMaxDepth {
			return nil, tooLarge("element nesting")
		}
		s.path = append(s.path, t.Name.Local)
	case xml.EndElement:
		s.path = s.path[:len(s.path)-1]
	}
	return t, nil
}

// at reports whether the path ends with the given names.
func (s *xmlStream) at(names ...string) bool {
	if len(s.path) < len(names) {
		return false
	}
	tail := s.path[len(s.path)-len(names):]
	for i, n := range names {
		if tail[i] != n {
			return false
		}
	}
	return true
}

// inText reports whether the stream is on the text of a string item (si or
// is): a plain <t> or a run <r><t>. Phonetic runs (rPh) are not text.
func (s *xmlStream) inText(item string) bool {
	return s.at(item, "t") || s.at(item, "r", "t")
}

func attr(e xml.StartElement, space, local string) string {
	for _, a := range e.Attr {
		if a.Name.Local == local && a.Name.Space == space {
			return a.Value
		}
	}
	return ""
}

// intAttr reads an optional integer attribute; absent is 0.
func intAttr(e xml.StartElement, local string) (int, error) {
	v := attr(e, "", local)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%w: attribute %s=%q is not a number", ErrUnsupportedFormat, local, v)
	}
	return n, nil
}

// textBuilder collects the text of one string item or cell value, up to
// the most text a cell can hold.
type textBuilder struct{ strings.Builder }

func (t *textBuilder) add(b []byte) error {
	if t.Len()+len(b) > xlsxMaxCellText {
		return tooLarge("text in one cell")
	}
	t.Write(b)
	return nil
}

type workbookInfo struct {
	sheets   []string // part names of the worksheets, in workbook order
	date1904 bool
}

const relNS = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"

func readWorkbookXML(parts map[string]*zip.File, b *budget) (workbookInfo, error) {
	var info workbookInfo
	s, err := openStream(parts["xl/workbook.xml"], b)
	if err != nil {
		return info, err
	}
	defer s.close()
	var rids []string
	for {
		t, err := s.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return info, fmt.Errorf("workbook: %w", err)
		}
		e, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		switch {
		case s.at("workbook", "workbookPr"):
			v := attr(e, "", "date1904")
			info.date1904 = v == "1" || v == "true"
		case s.at("workbook", "sheets", "sheet"):
			if len(rids) == maxSheets {
				return info, tooLarge(fmt.Sprintf("%d sheets", maxSheets))
			}
			rids = append(rids, attr(e, relNS, "id"))
		}
	}
	rels := map[string]string{}
	if f := parts["xl/_rels/workbook.xml.rels"]; f != nil {
		s, err := openStream(f, b)
		if err != nil {
			return info, err
		}
		defer s.close()
		for {
			t, err := s.next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return info, fmt.Errorf("workbook relations: %w", err)
			}
			e, ok := t.(xml.StartElement)
			if !ok || !s.at("Relationships", "Relationship") {
				continue
			}
			if len(rels) == xlsxMaxRelations {
				return info, tooLarge("workbook relations")
			}
			target := attr(e, "", "Target")
			if strings.HasPrefix(target, "/") {
				target = strings.TrimPrefix(target, "/")
			} else {
				target = path.Join("xl", target)
			}
			rels[attr(e, "", "Id")] = target
		}
	}
	for _, id := range rids {
		t, ok := rels[id]
		if !ok || id == "" {
			return info, fmt.Errorf("%w: a sheet without its part", ErrUnsupportedFormat)
		}
		info.sheets = append(info.sheets, t)
	}
	return info, nil
}

func readSharedStrings(parts map[string]*zip.File, b *budget) ([]string, error) {
	f := parts["xl/sharedStrings.xml"]
	if f == nil {
		return nil, nil
	}
	s, err := openStream(f, b)
	if err != nil {
		return nil, err
	}
	defer s.close()
	var out []string
	var text textBuilder
	for {
		t, err := s.next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("shared strings: %w", err)
		}
		switch t := t.(type) {
		case xml.StartElement:
			if s.at("sst", "si") {
				if len(out) == xlsxMaxSharedStrings {
					return nil, tooLarge("shared strings")
				}
				text.Reset()
			}
		case xml.CharData:
			if s.inText("si") {
				if err := text.add(t); err != nil {
					return nil, err
				}
			}
		case xml.EndElement:
			if t.Name.Local == "si" && s.at("sst") {
				out = append(out, text.String())
			}
		}
	}
}

// styles says, for each cell style index, whether its number format is a date.
type styles []bool

func readStyles(parts map[string]*zip.File, b *budget) (styles, error) {
	f := parts["xl/styles.xml"]
	if f == nil {
		return nil, nil
	}
	s, err := openStream(f, b)
	if err != nil {
		return nil, err
	}
	defer s.close()
	custom := map[int]string{}
	var xfs []int
	for {
		t, err := s.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("styles: %w", err)
		}
		e, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		switch {
		case s.at("styleSheet", "numFmts", "numFmt"):
			if len(custom) == xlsxMaxNumFmts {
				return nil, tooLarge("number formats")
			}
			id, err := intAttr(e, "numFmtId")
			if err != nil {
				return nil, fmt.Errorf("styles: %w", err)
			}
			custom[id] = attr(e, "", "formatCode")
		case s.at("styleSheet", "cellXfs", "xf"):
			if len(xfs) == xlsxMaxStyles {
				return nil, tooLarge("cell formats")
			}
			id, err := intAttr(e, "numFmtId")
			if err != nil {
				return nil, fmt.Errorf("styles: %w", err)
			}
			xfs = append(xfs, id)
		}
	}
	out := make(styles, len(xfs))
	for i, id := range xfs {
		if code, ok := custom[id]; ok {
			out[i] = isDateFormat(code)
		} else {
			out[i] = builtinDate(id)
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

// xmlCell is one <c> as read: its reference, type, style, the text of <v>
// and the text of an inline string <is>.
type xmlCell struct {
	Ref string
	T   string
	S   int
	V   string
	IS  string
}

func readWorksheet(f *zip.File, b *budget, shared []string, st styles, date1904 bool) (*sheet, error) {
	s, err := openStream(f, b)
	if err != nil {
		return nil, err
	}
	defer s.close()
	out := &sheet{Delimiter: SheetDelimiter, Header: -1}
	next := 1
	var r row
	var c xmlCell
	var v, is textBuilder
	for {
		t, err := s.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("worksheet: %w", err)
		}
		switch t := t.(type) {
		case xml.StartElement:
			switch {
			case s.at("worksheet", "sheetData", "row"):
				line, err := intAttr(t, "r")
				if err != nil {
					return nil, fmt.Errorf("worksheet: %w", err)
				}
				if line == 0 {
					line = next
				}
				if line < next {
					return nil, &RowError{Line: line, Column: "", Err: errors.New("rows out of order")}
				}
				next = line + 1
				r = row{Line: line}
			case s.at("sheetData", "row", "c"):
				style, err := intAttr(t, "s")
				if err != nil {
					return nil, fmt.Errorf("worksheet: %w", err)
				}
				c = xmlCell{Ref: attr(t, "", "r"), T: attr(t, "", "t"), S: style}
				v.Reset()
				is.Reset()
			}
		case xml.CharData:
			switch {
			case s.at("sheetData", "row", "c", "v"):
				if err := v.add(t); err != nil {
					return nil, err
				}
			case s.inText("is") && len(s.path) > 3 && s.path[3] == "c":
				if err := is.add(t); err != nil {
					return nil, err
				}
			}
		case xml.EndElement:
			switch {
			case t.Name.Local == "c" && s.at("worksheet", "sheetData", "row"):
				c.V, c.IS = v.String(), is.String()
				if err := addCell(&r, c, b, shared, st, date1904); err != nil {
					return nil, err
				}
			case t.Name.Local == "row" && s.at("worksheet", "sheetData"):
				// A row Excel keeps only for its formatting has no cells: it
				// counts as missing, like the empty lines a CSV reader skips.
				if len(r.Cells) == 0 {
					continue
				}
				if b.rows == 0 {
					return nil, tooLarge(fmt.Sprintf("%d rows", xlsxMaxRows))
				}
				b.rows--
				out.Rows = append(out.Rows, r)
			}
		}
	}
	out.Header = headerCandidate(out.Rows)
	return out, nil
}

// addCell renders one cell into its row, padding the columns it skips. The
// padding is charged to the cell budget: it is what costs memory.
func addCell(r *row, c xmlCell, b *budget, shared []string, st styles, date1904 bool) error {
	col := len(r.Cells)
	if c.Ref != "" {
		var err error
		if col, err = columnIndex(c.Ref); err != nil {
			return &RowError{Line: r.Line, Column: "", Err: err}
		}
	}
	if col < len(r.Cells) {
		return &RowError{Line: r.Line, Column: c.Ref, Err: errors.New("cells out of order")}
	}
	if col > maxColumn {
		return &RowError{Line: r.Line, Column: c.Ref, Err: errors.New("column beyond the sheet")}
	}
	cost := col - len(r.Cells) + 1
	if cost > b.cells {
		return tooLarge(fmt.Sprintf("%d cells", xlsxMaxCells))
	}
	b.cells -= cost
	for len(r.Cells) < col {
		r.Cells = append(r.Cells, "")
		r.Num = append(r.Num, false)
	}
	text, num, err := cellText(c, shared, st, date1904)
	if err != nil {
		return &RowError{Line: r.Line, Column: c.Ref, Err: err}
	}
	r.Cells = append(r.Cells, strings.TrimSpace(text))
	r.Num = append(r.Num, num)
	return nil
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
		return c.IS, false, nil
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
	if c, ok := plainCents(v); ok {
		return c, nil
	}
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

// plainCents reads the common case without allocating: an optional minus,
// up to 15 digits and up to two decimals. Anything else goes the long way.
func plainCents(v string) (int64, bool) {
	neg := strings.HasPrefix(v, "-")
	if neg {
		v = v[1:]
	}
	whole, frac, dot := strings.Cut(v, ".")
	if whole == "" || len(whole) > 15 || len(frac) > 2 || (dot && frac == "") {
		return 0, false
	}
	var n int64
	for i := 0; i < len(whole); i++ {
		if whole[i] < '0' || whole[i] > '9' {
			return 0, false
		}
		n = n*10 + int64(whole[i]-'0')
	}
	for i := 0; i < 2; i++ {
		n *= 10
		if i < len(frac) {
			if frac[i] < '0' || frac[i] > '9' {
				return 0, false
			}
			n += int64(frac[i] - '0')
		}
	}
	if neg {
		n = -n
	}
	return n, true
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
