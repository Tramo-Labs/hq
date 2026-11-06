package ledger

import (
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	apigen "github.com/Tramo-Labs/hq/eng/core/gen/api"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
	"github.com/Tramo-Labs/hq/eng/core/internal/text"
)

// MaxUpload is the largest statement file accepted (the idempotency
// middleware buffers up to 32 MiB of body, multipart overhead included).
const MaxUpload = 20 << 20

// Handlers implements the ledger operations of the generated strict server.
type Handlers struct {
	store *PGStore
	in    *Ingester
}

// NewHandlers wires the handlers to the Postgres store.
func NewHandlers(s *PGStore) *Handlers { return &Handlers{store: s, in: &Ingester{Store: s}} }

// ErrForbidden is the problem every module answers when the role does not allow the action.
var ErrForbidden = &httpx.Error{Status: http.StatusForbidden, Code: httpx.CodeForbidden, Detail: "your role cannot do this"}

// canUpload: who may upload statements (product/ESPECIFICACION.md section 1).
func canUpload(role string) bool {
	switch role {
	case identity.RoleOwner, identity.RoleApprover, identity.RoleOperator, identity.RoleAccountant:
		return true
	}
	return false
}

func (h *Handlers) ListAccounts(ctx context.Context, _ apigen.ListAccountsRequestObject) (apigen.ListAccountsResponseObject, error) {
	accts, err := h.store.Accounts(ctx)
	if err != nil {
		return nil, err
	}
	out := apigen.ListAccounts200JSONResponse{Items: []apigen.Account{}}
	for _, a := range accts {
		out.Items = append(out.Items, toAPIAccount(a))
	}
	return out, nil
}

func (h *Handlers) CreateAccount(ctx context.Context, req apigen.CreateAccountRequestObject) (apigen.CreateAccountResponseObject, error) {
	p, _ := identity.PrincipalFrom(ctx)
	if p.Role != identity.RoleOwner && p.Role != identity.RoleOperator {
		return nil, ErrForbidden
	}
	number := ""
	if req.Body.Number != nil {
		number = *req.Body.Number
	}
	id, err := h.store.CreateAccount(ctx, Bank(req.Body.Bank), number, req.Body.Name)
	if errors.Is(err, text.ErrInvalidName) {
		// Same code as a person's name: something visible, no zero width
		// or bidi characters (F27).
		return nil, &httpx.Error{Status: http.StatusBadRequest, Code: "invalid_name",
			Detail: "the account name needs a visible letter or number and no invisible or text direction characters"}
	}
	if err != nil {
		return nil, err
	}
	a, err := h.store.GetAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	return apigen.CreateAccount201JSONResponse(toAPIAccount(a)), nil
}

func (h *Handlers) UploadStatement(ctx context.Context, req apigen.UploadStatementRequestObject) (apigen.UploadStatementResponseObject, error) {
	p, _ := identity.PrincipalFrom(ctx)
	if !canUpload(p.Role) {
		return nil, ErrForbidden
	}
	acct, err := h.store.GetAccount(ctx, req.AccountId)
	if errors.Is(err, ErrNotFound) {
		return nil, &httpx.Error{Status: http.StatusNotFound, Code: httpx.CodeNotFound, Detail: "account not found"}
	}
	if err != nil {
		return nil, err
	}
	name, data, err := ReadFilePart(req.Body, MaxUpload)
	if err != nil {
		return nil, err
	}
	r, err := h.in.Ingest(ctx, Upload{TenantID: p.TenantID.String(), AccountID: acct.ID.String(), Bank: acct.Bank,
		AccountNumber: acct.Number, Filename: name, Data: data, ReceivedAt: time.Now()})
	if err != nil {
		return nil, err
	}
	out := apigen.IngestResult{Status: apigen.IngestResultStatus(r.Status), FileHash: r.FileHash,
		New: r.New, AlreadyPresent: r.AlreadyPresent,
		Detail: optStr(r.Detail), Fingerprint: optStr(r.Fingerprint), LayoutId: optStr(r.LayoutID),
		ClosedPeriod: apiClosedPeriod(r.ClosedPeriod)}
	if r.Reason != "" {
		reason := apigen.IngestResultReason(r.Reason)
		out.Reason = &reason
	}
	if r.Status == Duplicate {
		return apigen.UploadStatement200JSONResponse(out), nil
	}
	return apigen.UploadStatement201JSONResponse(out), nil
}

func (h *Handlers) ListQuarantine(ctx context.Context, _ apigen.ListQuarantineRequestObject) (apigen.ListQuarantineResponseObject, error) {
	files, err := h.store.Quarantine(ctx, 200)
	if err != nil {
		return nil, err
	}
	out := apigen.ListQuarantine200JSONResponse{Items: []apigen.QuarantinedFile{}}
	for _, f := range files {
		out.Items = append(out.Items, apigen.QuarantinedFile{Id: f.ID, AccountId: f.AccountID, Filename: optStr(f.Filename),
			Reason: f.Reason, Detail: optStr(f.Detail), Fingerprint: optStr(f.Fingerprint), ReceivedAt: f.ReceivedAt})
	}
	return out, nil
}

const (
	fileListLimit     = 500
	movementListLimit = 5000
)

// ListFiles is the "Archivos" list (F12): every role of the tenant sees it.
func (h *Handlers) ListFiles(ctx context.Context, req apigen.ListFilesRequestObject) (apigen.ListFilesResponseObject, error) {
	var f FileFilter
	if req.Params.Period != nil {
		f.Period = *req.Params.Period
	}
	if req.Params.Status != nil {
		f.Status = Status(*req.Params.Status)
	}
	rows, more, err := h.store.Files(ctx, f, fileListLimit)
	if err != nil {
		return nil, err
	}
	out := apigen.ListFiles200JSONResponse{Items: []apigen.UploadedFile{}, Truncated: more}
	for _, r := range rows {
		// Absent for a mailed file that was not routed.
		item := apigen.UploadedFile{Id: r.ID, AccountId: r.AccountID, AccountName: optStr(r.AccountName), Bank: apigen.Bank(r.Bank),
			Filename: optStr(r.Filename), Status: apigen.UploadedFileStatus(r.Status), Reason: optStr(r.Reason),
			Detail: optStr(r.Detail), LayoutId: optStr(r.LayoutID), ReceivedAt: r.ReceivedAt,
			UploadedBy: r.UploadedBy, UploadedByEmail: optStr(r.UploadedByEmail)}
		item.ClosedPeriod = apiClosedPeriod(r.ClosedPeriod)
		if r.From != nil {
			item.From = &openapi_types.Date{Time: *r.From}
		}
		if r.To != nil {
			item.To = &openapi_types.Date{Time: *r.To}
		}
		if r.Status == Accepted {
			n := r.Movements
			item.Movements = &n
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// ListMovements lists one month's movements (F12): every role sees them.
func (h *Handlers) ListMovements(ctx context.Context, req apigen.ListMovementsRequestObject) (apigen.ListMovementsResponseObject, error) {
	from, err := time.Parse("2006-01", req.Params.Period)
	if err != nil {
		return nil, &httpx.Error{Status: http.StatusBadRequest, Code: httpx.CodeBadRequest, Detail: "period must be YYYY-MM"}
	}
	to := from.AddDate(0, 1, -1)
	rows, more, err := h.store.Movements(ctx, from, to, req.Params.AccountId, movementListLimit)
	if err != nil {
		return nil, err
	}
	out := apigen.ListMovements200JSONResponse{Items: []apigen.LedgerMovement{}, Truncated: more}
	for _, r := range rows {
		item := apigen.LedgerMovement{Id: r.ID, AccountId: r.AccountID, AccountName: r.AccountName, Bank: apigen.Bank(r.Bank),
			FileId: r.FileID, Line: r.Line, Date: openapi_types.Date{Time: r.Date}, Amount: int64(r.Amount),
			Description: r.Description, Reference: optStr(r.Reference), ExternalRef: optStr(r.ExternalRef)}
		if r.Balance != nil {
			b := int64(*r.Balance)
			item.Balance = &b
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func toAPIAccount(a Account) apigen.Account {
	return apigen.Account{Id: a.ID, Bank: apigen.Bank(a.Bank), Name: a.Name, Number: optStr(a.Number), Currency: a.Currency}
}

func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ReadFilePart returns the name and bytes of the "file" part of a multipart
// body, refusing more than max bytes.
func ReadFilePart(mr *multipart.Reader, max int64) (string, []byte, error) {
	bad := func(d string) error {
		return &httpx.Error{Status: http.StatusBadRequest, Code: httpx.CodeBadRequest, Detail: d}
	}
	if mr == nil {
		return "", nil, bad("multipart body required")
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return "", nil, bad("the file part is missing")
		}
		if err != nil {
			return "", nil, bad("malformed multipart body")
		}
		if part.FormName() != "file" {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part, max+1))
		if err != nil {
			return "", nil, bad("could not read the file")
		}
		if int64(len(data)) > max {
			return "", nil, &httpx.Error{Status: http.StatusRequestEntityTooLarge, Code: httpx.CodeTooLarge, Detail: "file too large"}
		}
		if len(data) == 0 {
			return "", nil, bad("the file is empty")
		}
		return part.FileName(), data, nil
	}
}

// apiClosedPeriod shows a closed period with its last day included, as the
// API shows every range.
func apiClosedPeriod(p *ClosedPeriod) *apigen.PeriodRange {
	if p == nil {
		return nil
	}
	return &apigen.PeriodRange{From: openapi_types.Date{Time: p.Start}, To: openapi_types.Date{Time: p.End.AddDate(0, 0, -1)}}
}
