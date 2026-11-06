package documents

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sort"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	apigen "github.com/Tramo-Labs/hq/eng/core/gen/api"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

// MaxUpload is the largest invoice file accepted.
const MaxUpload = 15 << 20

// Handlers implements the documents operations of the generated strict
// server. No OCR and no language model are configured: see README.
type Handlers struct {
	store *PGStore
	ex    *Extractor
}

// NewHandlers wires the handlers to the store.
func NewHandlers(s *PGStore) *Handlers { return &Handlers{store: s, ex: &Extractor{}} }

func (h *Handlers) UploadInvoice(ctx context.Context, req apigen.UploadInvoiceRequestObject) (apigen.UploadInvoiceResponseObject, error) {
	p, _ := identity.PrincipalFrom(ctx)
	if p.Role == identity.RoleViewer || p.Role == "" {
		return nil, &httpx.Error{Status: http.StatusForbidden, Code: httpx.CodeForbidden, Detail: "your role cannot do this"}
	}
	data, err := readFile(req)
	if err != nil {
		return nil, err
	}
	own, err := h.store.OwnCUIT(ctx)
	if err != nil {
		return nil, err
	}
	ex := *h.ex
	ex.OwnCUIT = own
	in := Input{Image: data}
	if bytes.HasPrefix(data, []byte("%PDF-")) {
		in = Input{PDF: data}
	}
	inv, err := ex.Extract(ctx, in)
	if err != nil {
		return nil, &httpx.Error{Status: http.StatusBadRequest, Code: httpx.CodeBadRequest, Detail: "the file is not a readable PDF or image"}
	}
	id, err := h.store.Save(ctx, data, inv)
	switch {
	case errors.Is(err, ErrSameDocument):
		return nil, &httpx.Error{Status: http.StatusConflict, Code: httpx.CodeConflict, Detail: "this file was already uploaded"}
	case errors.Is(err, ErrDuplicateInvoice):
		return nil, &httpx.Error{Status: http.StatusConflict, Code: httpx.CodeConflict, Detail: "this invoice was already uploaded in another file"}
	case err != nil:
		return nil, err
	}
	return apigen.UploadInvoice201JSONResponse(ToAPI(id, inv)), nil
}

func (h *Handlers) GetInvoice(ctx context.Context, req apigen.GetInvoiceRequestObject) (apigen.GetInvoiceResponseObject, error) {
	inv, err := h.store.Get(ctx, req.InvoiceId)
	if errors.Is(err, ErrNotFound) {
		return nil, &httpx.Error{Status: http.StatusNotFound, Code: httpx.CodeNotFound, Detail: "invoice not found"}
	}
	if err != nil {
		return nil, err
	}
	return apigen.GetInvoice200JSONResponse(ToAPI(req.InvoiceId, inv)), nil
}

// ListInvoices lists the tenant's invoices (the rows behind the invoice
// metrics).
func (h *Handlers) ListInvoices(ctx context.Context, req apigen.ListInvoicesRequestObject) (apigen.ListInvoicesResponseObject, error) {
	f := ListFilter{AutoEligible: req.Params.AutoEligible}
	if req.Params.Period != nil {
		f.Period = *req.Params.Period
	}
	if req.Params.Review != nil {
		f.Review = *req.Params.Review
	}
	list, err := h.store.List(ctx, f, 500)
	if err != nil {
		return nil, err
	}
	out := apigen.ListInvoices200JSONResponse{Items: []apigen.InvoiceSummary{}}
	for _, s := range list {
		item := apigen.InvoiceSummary{Id: s.ID, IssuerCuit: s.IssuerCUIT, Number: s.Number, AutoEligible: s.AutoEligible,
			ItemReason:    itemReason(s.ItemReason),
			MinConfidence: s.MinConfidence, Missing: []string{}, CreatedAt: s.CreatedAt, Total: s.Total}
		if s.DocType != nil {
			v := int(*s.DocType)
			item.DocType = &v
		}
		if s.PointOfSale != nil {
			v := int(*s.PointOfSale)
			item.PointOfSale = &v
		}
		if s.IssueDate != nil {
			item.IssueDate = &openapi_types.Date{Time: *s.IssueDate}
		}
		for _, n := range s.Missing {
			item.Missing = append(item.Missing, string(n))
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// ToAPI renders an invoice for the API.
func ToAPI(id uuid.UUID, inv *Invoice) apigen.Invoice {
	out := apigen.Invoice{Id: id, Fields: map[string]apigen.InvoiceField{}, AutoEligible: inv.AutoEligible(),
		MinConfidence: inv.MinConfidence(), Missing: []string{}, Trail: inv.Trail, ItemReason: itemReason(inv.ItemReason)}
	if out.Trail == nil {
		out.Trail = []string{}
	}
	for n, f := range inv.Fields {
		af := apigen.InvoiceField{Value: f.Value, Source: apigen.InvoiceFieldSource(f.Source), Confidence: f.Confidence,
			Trusted: f.Trusted()}
		if f.Note != "" {
			note := f.Note
			af.Note = &note
		}
		if f.Conflict != nil {
			v, s := f.Conflict.Value, string(f.Conflict.Source)
			af.ConflictValue, af.ConflictSource = &v, &s
		}
		out.Fields[string(n)] = af
	}
	for _, n := range inv.Missing() {
		out.Missing = append(out.Missing, string(n))
	}
	sort.Strings(out.Missing)
	return out
}

func readFile(req apigen.UploadInvoiceRequestObject) ([]byte, error) {
	bad := func(d string) error {
		return &httpx.Error{Status: http.StatusBadRequest, Code: httpx.CodeBadRequest, Detail: d}
	}
	if req.Body == nil {
		return nil, bad("multipart body required")
	}
	for {
		part, err := req.Body.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, bad("the file part is missing")
		}
		if err != nil {
			return nil, bad("malformed multipart body")
		}
		if part.FormName() != "file" {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part, MaxUpload+1))
		if err != nil {
			return nil, bad("could not read the file")
		}
		if len(data) > MaxUpload {
			return nil, &httpx.Error{Status: http.StatusRequestEntityTooLarge, Code: httpx.CodeTooLarge, Detail: "file too large"}
		}
		if len(data) == 0 {
			return nil, bad("the file is empty")
		}
		return data, nil
	}
}

// itemReason renders why no open item was generated (nil when one was).
func itemReason(r string) *apigen.ItemReason {
	if r == "" {
		return nil
	}
	v := apigen.ItemReason(r)
	return &v
}
