package documents

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// Confidence assigned by the text rules. These are rule levels, not measured
// rates; they get replaced by measured precision once there is a labeled set
// of real invoices.
const (
	confLabeledUnique = 0.90 // label found, value validates, one distinct value
	confLabeledMany   = 0.50 // label found more than once with different values
	confDerived       = 0.86 // value derived from other labeled text (type from letter)
	ocrPenalty        = 0.15 // OCR text is read with the same rules, one level lower
)

// The patterns run over a folded copy of the text: lowercase, no accents,
// single spaces. They follow the layout of invoices issued from ARCA's
// "Comprobantes en línea" and common billing software.
var (
	rePosNum     = regexp.MustCompile(`punto de venta:?\s*(\d{1,5})\s+comp\.?\s*nro:?\s*(\d{1,8})`)
	rePosNumDash = regexp.MustCompile(`\bn(?:ro|o|°)?\.?\s*(\d{4,5})\s*-\s*(\d{8})\b`)
	reIssueDate  = regexp.MustCompile(`fecha(?: de)? emision:?\s*(\d{2}/\d{2}/\d{4})`)
	reCUIT       = regexp.MustCompile(`c\.?u\.?i\.?t\.?:?\s*(\d{2})-?(\d{8})-?(\d)\b`)
	reTotal      = regexp.MustCompile(`importe total:?\s*\$?\s*([0-9][0-9.,]*)`)
	reCAE        = regexp.MustCompile(`\bcae(?:a)?\s*(?:n(?:ro|o|°)?\.?)?:?\s*(\d{14})\b`)
	reCAEDue     = regexp.MustCompile(`fecha de vto\.? de caea?:?\s*(\d{2}/\d{2}/\d{4})`)
	reCode       = regexp.MustCompile(`\bcod(?:igo)?\.?\s*(?:n(?:ro|o|°)?\.?)?\s*(\d{1,3})\b`)
	reKind       = regexp.MustCompile(`\b(factura|nota de credito|nota de debito)\s+([abcm])\b`)
)

// docTypes maps the printed kind and letter to the ARCA comprobante code.
var docTypes = map[string]int{
	"factura a": 1, "nota de debito a": 2, "nota de credito a": 3,
	"factura b": 6, "nota de debito b": 7, "nota de credito b": 8,
	"factura c": 11, "nota de debito c": 12, "nota de credito c": 13,
	"factura m": 51, "nota de debito m": 52, "nota de credito m": 53,
}

func fold(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		r = foldRune(r)
		if r == ' ' || r == '\t' || r == '\r' || r == ' ' {
			if !space {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

func foldRune(r rune) rune {
	switch r {
	case 'á', 'à', 'ä':
		return 'a'
	case 'é', 'è', 'ë':
		return 'e'
	case 'í', 'ì', 'ï':
		return 'i'
	case 'ó', 'ò', 'ö':
		return 'o'
	case 'ú', 'ù', 'ü':
		return 'u'
	case 'ñ':
		return 'n'
	case 'º':
		return '°'
	}
	return r
}

// candidates collects distinct values in order of appearance.
type candidates struct{ vals []string }

func (c *candidates) add(v string) {
	for _, x := range c.vals {
		if x == v {
			return
		}
	}
	c.vals = append(c.vals, v)
}

func (c *candidates) field(src Source, label string) (Field, bool) {
	switch len(c.vals) {
	case 0:
		return Field{}, false
	case 1:
		return Field{Value: c.vals[0], Source: src, Confidence: confLabeledUnique}, true
	default:
		return Field{Value: c.vals[0], Source: src, Confidence: confLabeledMany,
			Note: strconv.Itoa(len(c.vals)) + " different values labeled " + label}, true
	}
}

// fieldsFromText applies the rules to a text layer. ownCUIT is the CUIT of
// the tenant receiving the invoice: it is printed on the invoice too and is
// excluded from the issuer candidates.
func fieldsFromText(text string, src Source, ownCUIT string) map[Name]Field {
	t := fold(text)
	out := map[Name]Field{}
	put := func(n Name, c *candidates, label string) {
		if f, ok := c.field(src, label); ok {
			out[n] = f
		}
	}

	var pos, num candidates
	for _, m := range rePosNum.FindAllStringSubmatch(t, -1) {
		pos.add(trimZeros(m[1]))
		num.add(trimZeros(m[2]))
	}
	if len(pos.vals) == 0 {
		for _, m := range rePosNumDash.FindAllStringSubmatch(t, -1) {
			pos.add(trimZeros(m[1]))
			num.add(trimZeros(m[2]))
		}
	}
	put(PointOfSale, &pos, "punto de venta")
	put(Number, &num, "comprobante nro")

	var date candidates
	for _, m := range reIssueDate.FindAllStringSubmatch(t, -1) {
		if d, err := time.Parse("02/01/2006", m[1]); err == nil {
			date.add(d.Format("2006-01-02"))
		}
	}
	put(IssueDate, &date, "fecha de emision")

	var cuit candidates
	for _, m := range reCUIT.FindAllStringSubmatch(t, -1) {
		c := m[1] + m[2] + m[3]
		if ValidCUIT(c) && c != ownCUIT {
			cuit.add(c)
		}
	}
	put(IssuerCUIT, &cuit, "cuit")

	var total candidates
	for _, m := range reTotal.FindAllStringSubmatch(t, -1) {
		if a, err := money.Parse(strings.TrimRight(m[1], ".,"), money.DecimalComma); err == nil && a > 0 {
			total.add(a.String())
		}
	}
	put(Total, &total, "importe total")

	var cae, due candidates
	for _, m := range reCAE.FindAllStringSubmatch(t, -1) {
		cae.add(m[1])
	}
	for _, m := range reCAEDue.FindAllStringSubmatch(t, -1) {
		if d, err := time.Parse("02/01/2006", m[1]); err == nil {
			due.add(d.Format("2006-01-02"))
		}
	}
	put(AuthCode, &cae, "cae")
	put(AuthDue, &due, "fecha de vto de cae")

	// Type: the printed code wins; the kind and letter confirm it or, alone,
	// give a derived value one step lower.
	var code, kind candidates
	for _, m := range reCode.FindAllStringSubmatch(t, -1) {
		code.add(trimZeros(m[1]))
	}
	for _, m := range reKind.FindAllStringSubmatch(t, -1) {
		if c, ok := docTypes[m[1]+" "+m[2]]; ok {
			kind.add(strconv.Itoa(c))
		}
	}
	switch f, ok := code.field(src, "cod"); {
	case ok && len(kind.vals) == 1 && kind.vals[0] != f.Value:
		f.Confidence, f.Note = confLabeledMany, "printed code and letter disagree"
		out[DocType] = f
	case ok:
		out[DocType] = f
	case len(kind.vals) == 1:
		out[DocType] = Field{Value: kind.vals[0], Source: src, Confidence: confDerived, Note: "from kind and letter"}
	case len(kind.vals) > 1:
		out[DocType] = Field{Value: kind.vals[0], Source: src, Confidence: confLabeledMany, Note: "several kinds printed"}
	}

	if src == SourceOCR {
		for n, f := range out {
			f.Confidence -= ocrPenalty
			out[n] = f
		}
	}
	return out
}

func trimZeros(s string) string {
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return "0"
	}
	return s
}
