// Package documents reads supplier invoices into fields that carry where
// each value came from and how much it can be trusted. Sources are tried in
// a fixed order: the ARCA QR (RG 4291), the text layer of the PDF, OCR, and
// a language model last. A value from the language model is never enough to
// register an invoice or to feed an automatic match.
package documents

import (
	"slices"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// Source of a field value, in the order they are tried.
type Source string

const (
	SourceQR      Source = "arca_qr"
	SourcePDFText Source = "pdf_text"
	SourceOCR     Source = "ocr"
	SourceLLM     Source = "llm"
	SourceManual  Source = "manual" // typed or confirmed by a person
)

// Name of an invoice field. Values are kept in a canonical string form so
// that fields from different sources compare exactly.
type Name string

const (
	IssuerCUIT   Name = "issuer_cuit"   // 11 digits
	DocType      Name = "doc_type"      // ARCA comprobante code, e.g. "1" factura A
	PointOfSale  Name = "point_of_sale" // integer, no padding
	Number       Name = "number"        // integer, no padding
	IssueDate    Name = "issue_date"    // YYYY-MM-DD
	Total        Name = "total"         // cents rendered by money.Amount.String
	Currency     Name = "currency"      // ARCA currency code, PES for pesos
	Rate         Name = "rate"          // exchange rate as written
	ReceiverType Name = "receiver_doc_type"
	ReceiverDoc  Name = "receiver_doc"
	AuthType     Name = "auth_type" // E (CAE) or A (CAEA)
	AuthCode     Name = "auth_code" // 14 digits
	AuthDue      Name = "auth_due"  // YYYY-MM-DD, only printed on the PDF
)

// Required are the fields an invoice needs before it can be registered or
// matched automatically.
var Required = []Name{IssuerCUIT, DocType, PointOfSale, Number, IssueDate, Total, AuthCode}

// AutoThreshold is the minimum confidence for a field to count toward
// registration or an automatic match. Confidences are assigned by rule (see
// README), not measured probabilities, until calibrated on labeled invoices.
const AutoThreshold = 0.85

// LLMCap is the highest confidence a language model value can carry. It is
// below AutoThreshold on purpose: those values always go to review.
const LLMCap = 0.6

// Field is one value with its provenance.
type Field struct {
	Value      string
	Source     Source
	Confidence float64
	// Conflict holds a different value another source read for the same
	// field. A field in conflict is never trusted until a person resolves it.
	Conflict *Field `json:",omitempty"`
	Note     string `json:",omitempty"`
}

// Trusted reports whether the field can feed registration or an automatic
// match on its own.
func (f Field) Trusted() bool {
	return f.Value != "" && f.Conflict == nil && f.Source != SourceLLM && f.Confidence >= AutoThreshold
}

// Invoice is the extraction result.
type Invoice struct {
	Fields map[Name]Field
	// Trail records each stage that ran and what it found, for the audit log.
	Trail []string
	// ItemReason says why the stored invoice generated no open item ("" when
	// it did, or before it is stored). See DraftItem.
	ItemReason string `json:"-"`
	// DirectionUncertain: the stored invoice generated an item whose
	// direction (receivable or payable) is not certain, because it was not
	// read from the QR. Such an invoice is not eligible for automatic.
	DirectionUncertain bool `json:"-"`
}

// TrailDirectionUncertain is the trail line of an invoice whose direction
// is not certain.
const TrailDirectionUncertain = "direction: not read from the ARCA QR; receivable or payable is not certain, so it can only be suggested"

func newInvoice() *Invoice { return &Invoice{Fields: map[Name]Field{}} }

// Get returns a field and whether it is set.
func (inv *Invoice) Get(n Name) (Field, bool) {
	f, ok := inv.Fields[n]
	return f, ok && f.Value != ""
}

// merge adds a value from a later source. An empty slot takes it. A slot
// already filled keeps its value; if the new one differs it is recorded as a
// conflict, unless the new value is from the language model (a model
// disagreeing with the QR or the text layer is noise, not evidence).
func (inv *Invoice) merge(n Name, f Field) {
	cur, ok := inv.Get(n)
	if !ok {
		inv.Fields[n] = f
		return
	}
	if cur.Value == f.Value || f.Source == SourceLLM {
		return
	}
	c := f
	cur.Conflict = &c
	inv.Fields[n] = cur
}

// Missing lists required fields with no value.
func (inv *Invoice) Missing() []Name {
	var out []Name
	for _, n := range Required {
		if _, ok := inv.Get(n); !ok {
			out = append(out, n)
		}
	}
	return out
}

// Untrusted lists required fields that are missing or not trusted.
func (inv *Invoice) Untrusted() []Name {
	var out []Name
	for _, n := range Required {
		if f, ok := inv.Get(n); !ok || !f.Trusted() {
			out = append(out, n)
		}
	}
	return out
}

// AutoEligible reports whether every required field is trusted. Only then
// may the invoice be registered without review or feed an automatic match.
// An invoice that is someone else's (F21) never is, nor one whose item has
// an uncertain direction: its item can only be suggested, and the invoice
// says the same.
func (inv *Invoice) AutoEligible() bool {
	return inv.ItemReason != NoItemNotThisTenant && !inv.DirectionUncertain && len(inv.Untrusted()) == 0
}

// MinConfidence over the required fields (0 if any is missing). A field in
// conflict or from the language model counts at most LLMCap. Matching uses
// it to keep low confidence documents out of the automatic tray.
func (inv *Invoice) MinConfidence() float64 {
	lowest := 1.0
	for _, n := range Required {
		f, ok := inv.Get(n)
		if !ok {
			return 0
		}
		c := f.Confidence
		if f.Conflict != nil || f.Source == SourceLLM {
			c = min(c, LLMCap)
		}
		lowest = min(lowest, c)
	}
	return lowest
}

// TotalAmount parses the total field.
func (inv *Invoice) TotalAmount() (money.Amount, bool) {
	f, ok := inv.Get(Total)
	if !ok {
		return 0, false
	}
	a, err := money.Parse(f.Value, money.DecimalDot)
	return a, err == nil
}

// Date parses the issue date field.
func (inv *Invoice) Date() (time.Time, bool) {
	f, ok := inv.Get(IssueDate)
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", f.Value)
	return t, err == nil
}

// Sources lists the distinct sources that contributed a field.
func (inv *Invoice) Sources() []Source {
	var out []Source
	for _, f := range inv.Fields {
		if !slices.Contains(out, f.Source) {
			out = append(out, f.Source)
		}
	}
	slices.Sort(out)
	return out
}
