package inbox

import (
	"context"
	"errors"
	"net/http"

	apigen "github.com/Tramo-Labs/hq/eng/core/gen/api"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

// Handlers implements the inbox operations of the generated strict server.
type Handlers struct{ svc *Service }

// NewHandlers returns the inbox handlers over the service.
func NewHandlers(s *Service) *Handlers { return &Handlers{svc: s} }

func problem(err error) error {
	switch {
	case errors.Is(err, ErrForbidden):
		return &httpx.Error{Status: http.StatusForbidden, Code: httpx.CodeForbidden, Detail: "your role cannot do this"}
	case errors.Is(err, ErrAlreadyAuthorized):
		return &httpx.Error{Status: http.StatusConflict, Code: "already_authorized", Detail: "that sender is already authorized"}
	case errors.Is(err, ErrNotFound):
		return &httpx.Error{Status: http.StatusNotFound, Code: httpx.CodeNotFound, Detail: "no such sender"}
	case errors.Is(err, ErrInvalidEmail):
		return &httpx.Error{Status: http.StatusBadRequest, Code: httpx.CodeBadRequest, Detail: "email must be one plain address"}
	}
	return err
}

func (h *Handlers) toAPI(mb Mailbox) apigen.Inbox {
	l := h.svc.limits()
	out := apigen.Inbox{Senders: make([]apigen.InboxSender, 0, len(mb.Senders)), Limits: apigen.InboxLimits{
		AttachmentsPerMessage: l.AttachmentsPerMessage, BytesPerAttachment: int(l.BytesPerAttachment),
		BytesPerMessage: int(l.BytesPerMessage), MessagesPerDay: l.MessagesPerDay,
		MessagesPerSenderPerDay: l.MessagesPerSenderPerDay, Kinds: append([]string{}, Kinds...)}}
	if a := h.svc.Address(mb.Code); a != "" {
		out.Address = &a
		at := mb.CreatedAt
		out.CreatedAt = &at
	}
	for _, s := range mb.Senders {
		out.Senders = append(out.Senders, apiSender(s))
	}
	return out
}

func apiSender(s Sender) apigen.InboxSender {
	return apigen.InboxSender{Id: s.ID, Email: s.Email, AddedAt: s.AddedAt, AddedBy: s.AddedBy}
}

// GetInbox returns the mailbox of the active tenant.
func (h *Handlers) GetInbox(ctx context.Context, _ apigen.GetInboxRequestObject) (apigen.GetInboxResponseObject, error) {
	mb, err := h.svc.Get(ctx)
	if err != nil {
		return nil, problem(err)
	}
	return apigen.GetInbox200JSONResponse(h.toAPI(mb)), nil
}

// RotateInboxAddress creates or replaces the address (owner).
func (h *Handlers) RotateInboxAddress(ctx context.Context, _ apigen.RotateInboxAddressRequestObject) (apigen.RotateInboxAddressResponseObject, error) {
	mb, err := h.svc.Rotate(ctx)
	if err != nil {
		return nil, problem(err)
	}
	return apigen.RotateInboxAddress201JSONResponse(h.toAPI(mb)), nil
}

// AddInboxSender authorizes a sender (owner).
func (h *Handlers) AddInboxSender(ctx context.Context, req apigen.AddInboxSenderRequestObject) (apigen.AddInboxSenderResponseObject, error) {
	s, err := h.svc.AddSender(ctx, string(req.Body.Email))
	if err != nil {
		return nil, problem(err)
	}
	return apigen.AddInboxSender201JSONResponse(apiSender(s)), nil
}

// RemoveInboxSender removes a sender (owner).
func (h *Handlers) RemoveInboxSender(ctx context.Context, req apigen.RemoveInboxSenderRequestObject) (apigen.RemoveInboxSenderResponseObject, error) {
	if err := h.svc.RemoveSender(ctx, req.SenderId); err != nil {
		return nil, problem(err)
	}
	return apigen.RemoveInboxSender204Response{}, nil
}

// ListInboxReceipts lists the latest receipts.
func (h *Handlers) ListInboxReceipts(ctx context.Context, _ apigen.ListInboxReceiptsRequestObject) (apigen.ListInboxReceiptsResponseObject, error) {
	rs, err := h.svc.Receipts(ctx)
	if err != nil {
		return nil, problem(err)
	}
	out := apigen.ListInboxReceipts200JSONResponse{Items: make([]apigen.InboxReceipt, 0, len(rs))}
	for _, r := range rs {
		ar := apigen.InboxReceipt{Id: r.ID, ReceivedAt: r.ReceivedAt, Sender: r.Sender,
			Status: apigen.InboxReceiptStatus(r.Status), Attachments: make([]apigen.InboxAttachment, 0, len(r.Attachments))}
		if r.Reason != "" {
			reason := apigen.InboxReceiptReason(r.Reason)
			ar.Reason = &reason
		}
		for _, a := range r.Attachments {
			aa := apigen.InboxAttachment{Filename: a.Filename, Size: a.Size, Status: apigen.InboxAttachmentStatus(a.Status)}
			if a.SHA256 != "" {
				sum := a.SHA256
				aa.Sha256 = &sum
			}
			if a.Reason != "" {
				reason := apigen.InboxAttachmentReason(a.Reason)
				aa.Reason = &reason
			}
			if a.Ingest != nil {
				aa.Ingest = &apigen.IngestResult{Status: apigen.IngestResultStatus(a.Ingest.Status), FileHash: a.Ingest.FileHash,
					New: a.Ingest.New, AlreadyPresent: a.Ingest.AlreadyPresent, Detail: opt(a.Ingest.Detail), LayoutId: opt(a.Ingest.LayoutID)}
				if a.Ingest.Reason != "" {
					reason := apigen.IngestResultReason(a.Ingest.Reason)
					aa.Ingest.Reason = &reason
				}
			}
			ar.Attachments = append(ar.Attachments, aa)
		}
		out.Items = append(out.Items, ar)
	}
	return out, nil
}

func opt(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
