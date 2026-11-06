// Package inbox is the client's statement mailbox (D-22): an address per
// tenant with a random code the owner rotates, the senders the owner
// authorized, and a receipt per message. Attachments go to ingestion
// (datos) through Ingest; the body, the subject and the headers are never
// stored. The mail provider is behind Message: an adapter turns whatever
// it receives into one and calls Receive. See README.md.
package inbox

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/inbox/store"
	"github.com/Tramo-Labs/hq/eng/core/internal/ledger"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/audit"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/jobs"
)

// Message is one mail as the provider's adapter hands it over. SPFAligned
// and DKIMAligned are the provider's verdicts that SPF or a DKIM signature
// passes for the From's domain (alignment, DMARC style); the adapter must
// not set them from headers the sender wrote.
type Message struct {
	To, From    string
	MessageID   string
	SPFAligned  bool
	DKIMAligned bool
	ReceivedAt  time.Time
	Attachments []Attachment
}

// Attachment is opened lazily and read up to the limits, never whole: the
// adapter should stream it from the provider, so a 30 MB attachment never
// sits in memory.
type Attachment struct {
	Filename    string
	ContentType string
	Open        func() (io.ReadCloser, error)
}

// Unassigned is an attachment for ingestion with no account given: datos
// routes it by bank and account number, and quarantines what it cannot.
type Unassigned struct {
	Filename   string
	Data       []byte
	ReceivedAt time.Time
	Sender     string
}

// Ingest is what ingestion (datos) provides. It runs under the tenant's
// scope with no user, like a job, and opens its own transaction.
type Ingest interface {
	IngestUnassigned(ctx context.Context, a Unassigned) (ledger.Result, error)
}

// Receipt status, refusal reasons and attachment outcomes (api/openapi.yaml,
// InboxReceipt and InboxAttachment).
const (
	Accepted = "accepted"
	Refused  = "refused"

	ReasonSenderNotAuthorized = "sender_not_authorized"
	ReasonNotAuthenticated    = "not_authenticated"
	ReasonTooManyAttachments  = "too_many_attachments"
	ReasonMessageTooLarge     = "message_too_large"
	ReasonDailyLimit          = "daily_limit"
	ReasonSenderDailyLimit    = "sender_daily_limit"

	Delivered    = "delivered"
	NotDelivered = "not_delivered"
	AttRefused   = "refused"

	ReasonUnsupportedKind = "unsupported_kind"
	ReasonTooLarge        = "too_large"
)

// AttachmentResult is what became of one attachment.
type AttachmentResult struct {
	Filename string         `json:"filename"`
	Size     int64          `json:"size"`
	SHA256   string         `json:"sha256,omitempty"`
	Status   string         `json:"status"`
	Reason   string         `json:"reason,omitempty"`
	Ingest   *IngestOutcome `json:"ingest,omitempty"`
}

// IngestOutcome is what ingestion did with a delivered attachment, as the
// receipt keeps it: the result without the parsed statement.
type IngestOutcome struct {
	Status         string `json:"status"`
	Reason         string `json:"reason,omitempty"`
	Detail         string `json:"detail,omitempty"` // for the tenant only, like the upload's; never logged
	FileHash       string `json:"fileHash"`
	LayoutID       string `json:"layoutId,omitempty"`
	New            int    `json:"new"`
	AlreadyPresent int    `json:"alreadyPresent"`
}

func outcome(r ledger.Result) *IngestOutcome {
	return &IngestOutcome{Status: string(r.Status), Reason: r.Reason, Detail: r.Detail, FileHash: r.FileHash,
		LayoutID: r.LayoutID, New: r.New, AlreadyPresent: r.AlreadyPresent}
}

// Receipt is the record of one message. Never the body nor the subject.
type Receipt struct {
	ID          uuid.UUID
	ReceivedAt  time.Time
	Sender      string
	Status      string
	Reason      string
	Attachments []AttachmentResult
}

var (
	// ErrUnknownAddress: no active address has that code (or the domain is
	// not ours). The adapter drops the mail; nothing is stored.
	ErrUnknownAddress = errors.New("inbox: no such address")
	// ErrDropped: refused past the day's cap of refused receipts; nothing
	// is stored.
	ErrDropped = errors.New("inbox: refused and dropped (daily cap of refused receipts)")
	// ErrForbidden: the caller's role cannot do this.
	ErrForbidden = errors.New("inbox: your role cannot do this")
	// ErrAlreadyAuthorized: the sender is already authorized.
	ErrAlreadyAuthorized = errors.New("inbox: sender already authorized")
	// ErrNotFound: no such sender.
	ErrNotFound = errors.New("inbox: not found")
	// ErrInvalidEmail: not one address.
	ErrInvalidEmail = errors.New("inbox: not an email address")
)

// Service is the mailbox.
type Service struct {
	DB     *db.DB
	Ingest Ingest // nil until datos provides it: attachments are not_delivered
	Domain string // the mailbox domain (INBOX_DOMAIN); empty shows no address
	Limits Limits // zero means DefaultLimits
	Now    func() time.Time
}

func (s *Service) limits() Limits {
	if s.Limits == (Limits{}) {
		return DefaultLimits
	}
	return s.Limits
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

var argentina = time.FixedZone("ART", -3*3600)

func dayOf(t time.Time) time.Time {
	y, m, d := t.In(argentina).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// localPart returns the code of a recipient address on our domain.
func (s *Service) localPart(to string) (string, bool) {
	a, err := mail.ParseAddress(to)
	if err != nil {
		return "", false
	}
	at := strings.LastIndexByte(a.Address, '@')
	if at <= 0 {
		return "", false
	}
	if s.Domain != "" && !strings.EqualFold(a.Address[at+1:], s.Domain) {
		return "", false
	}
	return strings.ToLower(a.Address[:at]), true
}

func normalizeEmail(raw string) (string, error) {
	// One bare address: no display name, no angle brackets, no list.
	raw = strings.TrimSpace(raw)
	a, err := mail.ParseAddress(raw)
	if err != nil || a.Name != "" || a.Address != raw {
		return "", ErrInvalidEmail
	}
	return strings.ToLower(a.Address), nil
}

// Receive takes one message. Checked in order, each refusal stopping the
// rest: the address (unknown: ErrUnknownAddress, nothing stored), a retry
// of a message already received (its receipt again), the sender authorized
// by the owner, SPF or DKIM aligned, how many attachments, the day's limits,
// then each attachment's kind and size and the message's total. Accepted
// attachments go to Ingest one by one.
func (s *Service) Receive(ctx context.Context, m Message) (Receipt, error) {
	code, ok := s.localPart(m.To)
	if !ok {
		return Receipt{}, ErrUnknownAddress
	}
	tenant, ok, err := s.DB.InboxTenant(ctx, code)
	if err != nil {
		return Receipt{}, err
	}
	if !ok {
		return Receipt{}, ErrUnknownAddress
	}
	ctx = jobs.TenantContext(ctx, tenant)
	lim := s.limits()

	sender := ""
	if a, err := mail.ParseAddress(m.From); err == nil {
		sender = strings.ToLower(a.Address)
	}
	idHash := messageHash(m.MessageID)
	received := m.ReceivedAt
	if received.IsZero() {
		received = s.now()
	}

	// Cheap checks first: nothing of an unauthorized message is read.
	var prior *Receipt
	refusal := ""
	err = s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		if r, err := q.ReceiptByMessage(ctx, idHash); err == nil {
			rc, err := toReceipt(r.ID, r.ReceivedAt, r.Sender, r.Status, r.Reason, r.Attachments)
			prior = &rc
			return err
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		authorized := false
		if sender != "" {
			if authorized, err = q.SenderAuthorized(ctx, sender); err != nil {
				return err
			}
		}
		switch {
		case !authorized:
			refusal = ReasonSenderNotAuthorized
		case !m.SPFAligned && !m.DKIMAligned:
			refusal = ReasonNotAuthenticated
		case len(m.Attachments) > lim.AttachmentsPerMessage:
			refusal = ReasonTooManyAttachments
		}
		return nil
	})
	if err != nil {
		return Receipt{}, err
	}
	if prior != nil {
		return *prior, nil
	}
	if sender == "" {
		sender = "(unreadable)"
	}
	if refusal != "" {
		return s.store(ctx, idHash, received, sender, refusal, nil, lim)
	}

	read, tooLarge, err := readAll(m.Attachments, lim)
	if err != nil {
		return Receipt{}, err
	}
	if tooLarge {
		return s.store(ctx, idHash, received, sender, ReasonMessageTooLarge, read, lim)
	}
	return s.store(ctx, idHash, received, sender, "", read, lim)
}

// read is one attachment as read: data only when it is to be delivered.
type read struct {
	result AttachmentResult
	data   []byte
}

// readAll reads each attachment up to its limit and the message's total.
// It stops reading as soon as the message is over its total (tooLarge).
func readAll(atts []Attachment, lim Limits) ([]read, bool, error) {
	out := make([]read, 0, len(atts))
	var total int64
	for _, a := range atts {
		name := a.Filename
		if name == "" {
			name = "(no name)"
		}
		r := read{result: AttachmentResult{Filename: name}}
		rc, err := a.Open()
		if err != nil {
			return nil, false, fmt.Errorf("inbox: open attachment: %w", err)
		}
		// One byte past the attachment's limit tells "too large" apart
		// from "exactly the limit"; never more than the message has left.
		budget := lim.BytesPerAttachment + 1
		if left := lim.BytesPerMessage - total + 1; left < budget {
			budget = left
		}
		data, err := io.ReadAll(io.LimitReader(rc, budget))
		_ = rc.Close()
		if err != nil {
			return nil, false, fmt.Errorf("inbox: read attachment: %w", err)
		}
		total += int64(len(data))
		if total > lim.BytesPerMessage {
			r.result.Size, r.result.Status, r.result.Reason = int64(len(data)), AttRefused, ReasonTooLarge
			out = append(out, r)
			return out, true, nil
		}
		switch {
		case int64(len(data)) > lim.BytesPerAttachment:
			r.result.Size, r.result.Status, r.result.Reason = lim.BytesPerAttachment, AttRefused, ReasonTooLarge
		default:
			sum := sha256.Sum256(data)
			r.result.Size, r.result.SHA256 = int64(len(data)), hex.EncodeToString(sum[:])
			head := data
			if len(head) > 8192 {
				head = head[:8192]
			}
			if kindOf(name, head) == "" {
				r.result.Status, r.result.Reason = AttRefused, ReasonUnsupportedKind
			} else {
				r.data = data
			}
		}
		out = append(out, r)
	}
	return out, false, nil
}

// store writes the receipt under the tenant's lock: the day's limits are
// checked there, so concurrent deliveries cannot pass them together. An
// accepted message's attachments are delivered inside, before the receipt.
func (s *Service) store(ctx context.Context, idHash []byte, received time.Time, sender, refusal string, atts []read, lim Limits) (Receipt, error) {
	var out Receipt
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.LockReceive(ctx); err != nil {
			return err
		}
		if r, err := q.ReceiptByMessage(ctx, idHash); err == nil {
			out, err = toReceipt(r.ID, r.ReceivedAt, r.Sender, r.Status, r.Reason, r.Attachments)
			return err
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		day := dayOf(received)
		n, err := q.CountDay(ctx, store.CountDayParams{Sender: sender, Day: day})
		if err != nil {
			return err
		}
		if refusal == "" {
			switch {
			case int(n.Accepted) >= lim.MessagesPerDay:
				refusal = ReasonDailyLimit
			case int(n.FromSender) >= lim.MessagesPerSenderPerDay:
				refusal = ReasonSenderDailyLimit
			}
		}
		if refusal != "" && int(n.Total-n.Accepted) >= lim.RefusedPerDay {
			return ErrDropped
		}

		results := make([]AttachmentResult, 0, len(atts))
		for _, a := range atts {
			res := a.result
			if refusal == "" && a.data != nil {
				if s.Ingest == nil {
					res.Status = NotDelivered
				} else {
					ir, err := s.Ingest.IngestUnassigned(ctx, Unassigned{Filename: res.Filename, Data: a.data,
						ReceivedAt: received, Sender: sender})
					if err != nil {
						return fmt.Errorf("inbox: deliver attachment: %w", err)
					}
					res.Status, res.Ingest = Delivered, outcome(ir)
				}
			} else if res.Status == "" {
				res.Status = NotDelivered // the message was refused: nothing went on
			}
			results = append(results, res)
		}
		raw, err := json.Marshal(results)
		if err != nil {
			return err
		}
		status, reason := Accepted, (*string)(nil)
		if refusal != "" {
			status, reason = Refused, &refusal
		}
		row, err := q.InsertReceipt(ctx, store.InsertReceiptParams{MessageIDHash: idHash, ReceivedAt: received,
			Day: day, Sender: sender, Status: status, Reason: reason, Attachments: raw})
		if err != nil {
			return err
		}
		out, err = toReceipt(row.ID, row.ReceivedAt, row.Sender, row.Status, row.Reason, row.Attachments)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "inbox.receive", Subject: "inbox.receipts:" + out.ID.String(),
			Detail: map[string]any{"status": status, "reason": refusal, "attachments": len(results)}})
	})
	return out, err
}

func messageHash(id string) []byte {
	id = strings.TrimSpace(id)
	if id == "" {
		// No Message-ID: nothing to recognize a retry by, so a fresh value.
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		return b
	}
	sum := sha256.Sum256([]byte(id))
	return sum[:]
}

func toReceipt(id uuid.UUID, at time.Time, sender, status string, reason *string, atts []byte) (Receipt, error) {
	r := Receipt{ID: id, ReceivedAt: at, Sender: sender, Status: status}
	if reason != nil {
		r.Reason = *reason
	}
	if err := json.Unmarshal(atts, &r.Attachments); err != nil {
		return Receipt{}, fmt.Errorf("inbox: receipt %s: %w", id, err)
	}
	if r.Attachments == nil {
		r.Attachments = []AttachmentResult{}
	}
	return r, nil
}

// Mailbox is what GET /v1/inbox shows.
type Mailbox struct {
	Code      string // empty before the owner creates one
	CreatedAt time.Time
	Senders   []Sender
}

// Sender is an authorized sender.
type Sender struct {
	ID      uuid.UUID
	Email   string
	AddedBy *uuid.UUID
	AddedAt time.Time
}

// Address is the full address of code on the service's domain, or "" when
// there is no code or no domain configured.
func (s *Service) Address(code string) string {
	if code == "" || s.Domain == "" {
		return ""
	}
	return code + "@" + s.Domain
}

func role(ctx context.Context, q *store.Queries) (string, error) {
	r, err := q.MemberRole(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrForbidden
	}
	return r, err
}

func canSee(r string) bool { return r == "owner" || r == "accountant" || r == "operator" }

// Get returns the mailbox of the tenant in scope (owner, accountant,
// operator).
func (s *Service) Get(ctx context.Context) (Mailbox, error) {
	var mb Mailbox
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		r, err := role(ctx, q)
		if err != nil {
			return err
		}
		if !canSee(r) {
			return ErrForbidden
		}
		mb, err = mailbox(ctx, q)
		return err
	})
	return mb, err
}

func mailbox(ctx context.Context, q *store.Queries) (Mailbox, error) {
	var mb Mailbox
	a, err := q.ActiveAddress(ctx)
	switch {
	case err == nil:
		mb.Code, mb.CreatedAt = a.Code, a.CreatedAt
	case !errors.Is(err, pgx.ErrNoRows):
		return mb, err
	}
	rows, err := q.ActiveSenders(ctx)
	if err != nil {
		return mb, err
	}
	mb.Senders = make([]Sender, 0, len(rows))
	for _, r := range rows {
		mb.Senders = append(mb.Senders, Sender{ID: r.ID, Email: r.Email, AddedBy: r.AddedBy, AddedAt: r.AddedAt})
	}
	return mb, nil
}

var codeEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// newCode is 130 random bits as 26 lowercase base32 characters.
func newCode() string {
	b := make([]byte, 17)
	_, _ = rand.Read(b)
	return strings.ToLower(codeEncoding.EncodeToString(b))[:26]
}

// Rotate creates the tenant's address, or replaces it: the previous one
// stops accepting mail in the same transaction. Owner only.
func (s *Service) Rotate(ctx context.Context) (Mailbox, error) {
	var mb Mailbox
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		r, err := role(ctx, q)
		if err != nil {
			return err
		}
		if r != "owner" {
			return ErrForbidden
		}
		if err := q.RevokeAddress(ctx); err != nil {
			return err
		}
		a, err := q.InsertAddress(ctx, newCode())
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Entry{Action: "inbox.rotate", Subject: "inbox.addresses:" + a.ID.String()}); err != nil {
			return err
		}
		mb, err = mailbox(ctx, q)
		return err
	})
	return mb, err
}

// AddSender authorizes a sender. Owner only.
func (s *Service) AddSender(ctx context.Context, email string) (Sender, error) {
	e, err := normalizeEmail(email)
	if err != nil {
		return Sender{}, err
	}
	var out Sender
	err = s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		r, err := role(ctx, q)
		if err != nil {
			return err
		}
		if r != "owner" {
			return ErrForbidden
		}
		row, err := q.InsertSender(ctx, e)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAlreadyAuthorized
		}
		if err != nil {
			return err
		}
		out = Sender{ID: row.ID, Email: row.Email, AddedBy: row.AddedBy, AddedAt: row.AddedAt}
		return audit.Record(ctx, tx, audit.Entry{Action: "inbox.sender.add", Subject: "inbox.senders:" + row.ID.String()})
	})
	return out, err
}

// RemoveSender stops accepting mail from a sender. Owner only.
func (s *Service) RemoveSender(ctx context.Context, id uuid.UUID) error {
	return s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		r, err := role(ctx, q)
		if err != nil {
			return err
		}
		if r != "owner" {
			return ErrForbidden
		}
		n, err := q.RemoveSender(ctx, id)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "inbox.sender.remove", Subject: "inbox.senders:" + id.String()})
	})
}

// ReceiptsShown is how many receipts the list carries.
const ReceiptsShown = 200

// Receipts lists the latest receipts (owner, accountant, operator).
func (s *Service) Receipts(ctx context.Context) ([]Receipt, error) {
	var out []Receipt
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		r, err := role(ctx, q)
		if err != nil {
			return err
		}
		if !canSee(r) {
			return ErrForbidden
		}
		rows, err := q.RecentReceipts(ctx, ReceiptsShown)
		if err != nil {
			return err
		}
		out = make([]Receipt, 0, len(rows))
		for _, row := range rows {
			rc, err := toReceipt(row.ID, row.ReceivedAt, row.Sender, row.Status, row.Reason, row.Attachments)
			if err != nil {
				return err
			}
			out = append(out, rc)
		}
		return nil
	})
	return out, err
}
