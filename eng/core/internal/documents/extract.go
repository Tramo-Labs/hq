package documents

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// OCR reads text from a scanned document. No implementation ships yet; the
// extractor runs without it and says so in the trail.
type OCR interface {
	Text(ctx context.Context, doc []byte) (string, error)
}

// LLM proposes values for the fields still missing. Whatever it returns is
// validated like any other source and capped at LLMCap confidence.
type LLM interface {
	Fields(ctx context.Context, text string, missing []Name) (map[Name]string, error)
}

// Extractor runs the sources in order: QR, PDF text, OCR, LLM.
type Extractor struct {
	OCR     OCR // optional
	LLM     LLM // optional
	OwnCUIT string
}

// Input is one document: a PDF, or an image (photo or scan).
type Input struct {
	PDF   []byte
	Image []byte
}

// Extract reads the document. It returns an error only when the input is
// unreadable as a whole (not a PDF, not an image); missing fields are not an
// error, they show in Missing and Untrusted.
func (e *Extractor) Extract(ctx context.Context, in Input) (*Invoice, error) {
	inv := newInvoice()
	var text string

	// 1. ARCA QR.
	var qrTexts []string
	switch {
	case in.PDF != nil:
		doc, err := readPDF(in.PDF)
		if err != nil {
			return nil, err
		}
		text = doc.Text
		qrTexts = append(qrTexts, QRURLPattern.FindAllString(string(in.PDF), -1)...)
		qrTexts = append(qrTexts, QRURLPattern.FindAllString(text, -1)...)
		for _, img := range doc.Images {
			if s, err := DecodeQRImage(img); err == nil {
				qrTexts = append(qrTexts, s)
			}
		}
	case in.Image != nil:
		s, err := DecodeQRImage(in.Image)
		if err == nil {
			qrTexts = append(qrTexts, s)
		} else {
			inv.Trail = append(inv.Trail, "qr: none found in the image")
		}
	default:
		return nil, fmt.Errorf("documents: empty input")
	}
	inv.Trail = append(inv.Trail, e.qrStage(inv, qrTexts))

	// 2. Text layer of the PDF.
	if strings.TrimSpace(text) != "" {
		got := fieldsFromText(text, SourcePDFText, e.OwnCUIT)
		mergeAll(inv, got)
		inv.Trail = append(inv.Trail, fmt.Sprintf("pdf_text: %d fields", len(got)))
	} else if in.PDF != nil {
		inv.Trail = append(inv.Trail, "pdf_text: no text layer")
	}

	// 3. OCR, only when there is no text layer to read.
	if len(inv.Missing()) > 0 && strings.TrimSpace(text) == "" {
		if e.OCR == nil {
			inv.Trail = append(inv.Trail, "ocr: not configured")
		} else {
			doc := in.PDF
			if doc == nil {
				doc = in.Image
			}
			t, err := e.OCR.Text(ctx, doc)
			if err != nil {
				inv.Trail = append(inv.Trail, "ocr: "+err.Error())
			} else {
				text = t
				got := fieldsFromText(t, SourceOCR, e.OwnCUIT)
				mergeAll(inv, got)
				inv.Trail = append(inv.Trail, fmt.Sprintf("ocr: %d fields", len(got)))
			}
		}
	}

	// 4. Language model, last, only for what is still missing.
	if missing := inv.Missing(); len(missing) > 0 && e.LLM != nil {
		vals, err := e.LLM.Fields(ctx, text, missing)
		if err != nil {
			inv.Trail = append(inv.Trail, "llm: "+err.Error())
		} else {
			n := 0
			for _, name := range missing {
				v, ok := canonical(name, vals[name])
				if !ok {
					continue
				}
				inv.merge(name, Field{Value: v, Source: SourceLLM, Confidence: LLMCap, Note: "proposed by a model; needs review"})
				n++
			}
			inv.Trail = append(inv.Trail, fmt.Sprintf("llm: %d fields proposed", n))
		}
	}
	return inv, nil
}

// qrStage takes the first QR text that validates. Several valid QRs with
// different content in one document mean it is not one invoice: none is used.
func (e *Extractor) qrStage(inv *Invoice, texts []string) string {
	var valid []*QR
	seen := map[string]bool{}
	var firstErr error
	for _, s := range texts {
		if seen[s] {
			continue
		}
		seen[s] = true
		q, err := ParseQRURL(s)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		dup := false
		for _, v := range valid {
			if *v == *q {
				dup = true
			}
		}
		if !dup {
			valid = append(valid, q)
		}
	}
	switch {
	case len(valid) == 1:
		mergeAll(inv, valid[0].Fields())
		return "qr: valid"
	case len(valid) > 1:
		return fmt.Sprintf("qr: %d different valid QRs, none used", len(valid))
	case firstErr != nil:
		return "qr: rejected: " + firstErr.Error()
	default:
		return "qr: none found"
	}
}

func mergeAll(inv *Invoice, fs map[Name]Field) {
	names := make([]Name, 0, len(fs))
	for n := range fs {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		inv.merge(n, fs[n])
	}
}

// canonical validates a value proposed by the model and puts it in the
// canonical form of its field. Anything that does not validate is dropped.
func canonical(n Name, v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", false
	}
	digits := func(max int) (string, bool) {
		d := strings.Map(func(r rune) rune {
			if r == '-' || r == ' ' {
				return -1
			}
			return r
		}, v)
		if !reDigits.MatchString(d) || len(d) > max {
			return "", false
		}
		return trimZeros(d), true
	}
	switch n {
	case IssuerCUIT:
		d := strings.ReplaceAll(v, "-", "")
		return d, ValidCUIT(d)
	case DocType:
		return digits(3)
	case PointOfSale:
		return digits(5)
	case Number:
		return digits(8)
	case IssueDate, AuthDue:
		for _, l := range []string{"2006-01-02", "02/01/2006"} {
			if t, err := time.Parse(l, v); err == nil {
				return t.Format("2006-01-02"), true
			}
		}
		return "", false
	case Total:
		for _, st := range []money.Style{money.DecimalComma, money.DecimalDot} {
			if a, err := money.Parse(v, st); err == nil && a > 0 {
				return a.String(), true
			}
		}
		return "", false
	case AuthCode:
		return v, reDigits.MatchString(v) && len(v) == 14
	}
	return "", false
}
