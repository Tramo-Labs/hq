package documents

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Tramo-Labs/hq/eng/core/internal/documents/store"
	"github.com/Tramo-Labs/hq/eng/core/internal/listrange"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/audit"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
)

// PGStore keeps extracted invoices in documents.invoices, always through
// db.InTenant.
type PGStore struct {
	DB *db.DB
	// OnSaved stores the open item an invoice generates, inside the same
	// transaction as the invoice (wired to matching in internal/app). Nil
	// stores invoices only.
	OnSaved func(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID, d ItemDraft) error
}

var (
	// ErrSameDocument: this exact file was already stored for the tenant.
	ErrSameDocument = errors.New("documents: the same file was already stored")
	// ErrDuplicateInvoice: another document already carries the same issuer,
	// type, point of sale and number (all read from trusted fields).
	ErrDuplicateInvoice = errors.New("documents: duplicate invoice")
)

// keyValue returns the field value only when it is trusted: the duplicate
// key never relies on an uncertain reading.
func keyValue(inv *Invoice, n Name) *string {
	f, ok := inv.Get(n)
	if !ok || !f.Trusted() {
		return nil
	}
	return &f.Value
}

func keyInt(inv *Invoice, n Name) *int64 {
	v := keyValue(inv, n)
	if v == nil {
		return nil
	}
	i, err := strconv.ParseInt(*v, 10, 64)
	if err != nil {
		return nil
	}
	return &i
}

// keyInt32 is keyInt for the int columns. A trusted value that does not
// fit is an error, as the database would have refused it: the invoice does
// not enter rather than enter without its key.
func keyInt32(inv *Invoice, n Name) (*int32, error) {
	v := keyInt(inv, n)
	if v == nil {
		return nil, nil
	}
	if *v < math.MinInt32 || *v > math.MaxInt32 {
		return nil, fmt.Errorf("documents: %s out of range", n)
	}
	i := int32(*v)
	return &i, nil
}

// Save stores an extracted invoice for the tenant in the context.
func (s *PGStore) Save(ctx context.Context, doc []byte, inv *Invoice) (uuid.UUID, error) {
	sum := sha256.Sum256(doc)
	fields, err := json.Marshal(inv.Fields)
	if err != nil {
		return uuid.Nil, err
	}
	var date *time.Time
	if v := keyValue(inv, IssueDate); v != nil {
		if d, err := time.Parse("2006-01-02", *v); err == nil {
			date = &d
		}
	}
	var total *int64
	if v := keyValue(inv, Total); v != nil {
		if a, ok := inv.TotalAmount(); ok {
			t := int64(a)
			total = &t
		}
	}
	docType, err := keyInt32(inv, DocType)
	if err != nil {
		return uuid.Nil, err
	}
	pos, err := keyInt32(inv, PointOfSale)
	if err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err = s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		// The tenant's CUIT, read in this transaction, tells which side of
		// the invoice the tenant is on.
		q := store.New(tx)
		cuit, err := q.TenantCUIT(ctx)
		if err != nil {
			return err
		}
		own := ""
		if cuit != nil {
			own = fmt.Sprintf("%011d", *cuit)
		}
		draft, reason := DraftItem(inv, own)
		inv.ItemReason = reason
		inv.DirectionUncertain = reason == "" && !draft.DirectionCertain
		if inv.DirectionUncertain {
			inv.Trail = append(inv.Trail, TrailDirectionUncertain)
		}
		trail, err := json.Marshal(inv.Trail)
		if err != nil {
			return err
		}
		var itemReason *string
		if reason != "" {
			itemReason = &reason
		}
		id, err = q.InsertInvoice(ctx, store.InsertInvoiceParams{
			Sha256: sum[:], Fields: fields, Trail: trail,
			AutoEligible: inv.AutoEligible(), MinConfidence: float32(inv.MinConfidence()),
			IssuerCuit: keyValue(inv, IssuerCUIT), DocType: docType,
			PointOfSale: pos, Number: keyInt(inv, Number),
			IssueDate: date, Total: total, ItemReason: itemReason, DirectionUncertain: inv.DirectionUncertain,
		})
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if pgErr.ConstraintName == "invoices_tenant_id_sha256_key" {
				return ErrSameDocument
			}
			return ErrDuplicateInvoice
		}
		if err != nil {
			return fmt.Errorf("documents: insert: %w", err)
		}
		if reason == "" && s.OnSaved != nil {
			if err := s.OnSaved(ctx, tx, id, draft); err != nil {
				return fmt.Errorf("documents: item: %w", err)
			}
		}
		detail := map[string]any{"auto_eligible": inv.AutoEligible(), "sources": inv.Sources()}
		if reason != "" {
			detail["no_item"] = reason
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "documents.invoice_stored", Subject: "documents.invoices:" + id.String(),
			Detail: detail})
	})
	return id, err
}

// OwnCUIT returns the CUIT of the tenant in the context ("" when not set).
// The extractor excludes it from the issuer candidates.
func (s *PGStore) OwnCUIT(ctx context.Context) (string, error) {
	var cuit *int64
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		var err error
		cuit, err = store.New(tx).TenantCUIT(ctx)
		return err
	})
	if err != nil || cuit == nil {
		return "", err
	}
	return fmt.Sprintf("%011d", *cuit), nil
}

// Summary is a stored invoice as listed: its key columns (filled only from
// trusted fields), whether it qualifies for automatic, and what is missing.
type Summary struct {
	ID            uuid.UUID
	IssuerCUIT    *string
	DocType       *int32
	PointOfSale   *int32
	Number        *int64
	IssueDate     *time.Time
	Total         *int64
	AutoEligible  bool
	MinConfidence float64
	Missing       []Name
	ItemReason    string // why no open item was generated; "" when one was
	CreatedAt     time.Time
}

// ListFilter narrows List.
type ListFilter struct {
	Period       string           // YYYY-MM, Argentina time; "" all
	Range        *listrange.Range // days stored, Argentina time, both included; not with Period
	AutoEligible *bool            // only those that do or do not qualify
	Review       bool             // only those to review: not eligible, or no open item
}

// List returns the invoices of the tenant in the context, newest first.
func (s *PGStore) List(ctx context.Context, f ListFilter, limit int) ([]Summary, error) {
	r := f.Range
	if f.Period != "" {
		if r != nil {
			return nil, fmt.Errorf("%w: period does not go with a range", listrange.ErrInvalid)
		}
		var err error
		if r, err = listrange.Parse(&f.Period, nil, nil); err != nil {
			return nil, err
		}
	}
	from, last := r.Bounds()
	var out []Summary
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		rows, err := store.New(tx).ListInvoices(ctx, store.ListInvoicesParams{
			DateFrom: from, DateTo: last, AutoEligible: f.AutoEligible, Review: f.Review, MaxRows: int32(limit), // #nosec G115 -- callers clamp limit
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			sm := Summary{ID: r.ID, IssuerCUIT: r.IssuerCuit, DocType: r.DocType, PointOfSale: r.PointOfSale,
				Number: r.Number, IssueDate: r.IssueDate, Total: r.Total, AutoEligible: r.AutoEligible,
				MinConfidence: float64(r.MinConfidence), ItemReason: r.ItemReason, CreatedAt: r.CreatedAt}
			inv := newInvoice()
			if err := json.Unmarshal(r.Fields, &inv.Fields); err != nil {
				return err
			}
			sm.Missing = inv.Missing()
			out = append(out, sm)
		}
		return nil
	})
	return out, err
}

// ErrNotFound means the invoice does not exist for the tenant in the context.
var ErrNotFound = errors.New("documents: not found")

// Get returns a stored invoice of the tenant in the context.
func (s *PGStore) Get(ctx context.Context, id uuid.UUID) (*Invoice, error) {
	inv := newInvoice()
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		r, err := store.New(tx).GetInvoice(ctx, id)
		if err != nil {
			return err
		}
		inv.DirectionUncertain = r.DirectionUncertain
		if r.ItemReason != nil {
			inv.ItemReason = *r.ItemReason
		}
		if err := json.Unmarshal(r.Fields, &inv.Fields); err != nil {
			return err
		}
		return json.Unmarshal(r.Trail, &inv.Trail)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return inv, nil
}
