package documents

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

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
	var id uuid.UUID
	err = s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		// The tenant's CUIT, read in this transaction, tells which side of
		// the invoice the tenant is on.
		var cuit *int64
		if err := tx.QueryRow(ctx, `select cuit from tenants where id = app_current_tenant()`).Scan(&cuit); err != nil {
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
		err = tx.QueryRow(ctx,
			`insert into documents.invoices (tenant_id, sha256, fields, trail, auto_eligible, min_confidence,
			   issuer_cuit, doc_type, point_of_sale, number, issue_date, total, item_reason, direction_uncertain)
			 values (app_current_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) returning id`,
			sum[:], fields, trail, inv.AutoEligible(), inv.MinConfidence(),
			keyValue(inv, IssuerCUIT), keyInt(inv, DocType), keyInt(inv, PointOfSale), keyInt(inv, Number),
			date, total, itemReason, inv.DirectionUncertain).Scan(&id)
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
		return tx.QueryRow(ctx, `select cuit from tenants where id = app_current_tenant()`).Scan(&cuit)
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
	Period       string // YYYY-MM, Argentina time; "" all
	AutoEligible *bool  // only those that do or do not qualify
	Review       bool   // only those to review: not eligible, or no open item
}

// List returns the invoices of the tenant in the context, newest first.
func (s *PGStore) List(ctx context.Context, f ListFilter, limit int) ([]Summary, error) {
	var out []Summary
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			select id, issuer_cuit, doc_type, point_of_sale, number, issue_date, total, auto_eligible, min_confidence,
			       fields, coalesce(item_reason, ''), created_at
			  from documents.invoices
			 where ($1 = '' or to_char(created_at at time zone 'America/Argentina/Buenos_Aires', 'YYYY-MM') = $1)
			   and ($2::boolean is null or auto_eligible = $2)
			   and (not $3 or not auto_eligible or item_reason is not null)
			 order by created_at desc, id limit $4`, f.Period, f.AutoEligible, f.Review, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var sm Summary
			var conf float32
			var fields []byte
			if err := rows.Scan(&sm.ID, &sm.IssuerCUIT, &sm.DocType, &sm.PointOfSale, &sm.Number, &sm.IssueDate, &sm.Total,
				&sm.AutoEligible, &conf, &fields, &sm.ItemReason, &sm.CreatedAt); err != nil {
				return err
			}
			sm.MinConfidence = float64(conf)
			inv := newInvoice()
			if err := json.Unmarshal(fields, &inv.Fields); err != nil {
				return err
			}
			sm.Missing = inv.Missing()
			out = append(out, sm)
		}
		return rows.Err()
	})
	return out, err
}

// ErrNotFound means the invoice does not exist for the tenant in the context.
var ErrNotFound = errors.New("documents: not found")

// Get returns a stored invoice of the tenant in the context.
func (s *PGStore) Get(ctx context.Context, id uuid.UUID) (*Invoice, error) {
	inv := newInvoice()
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		var fields, trail []byte
		var reason *string
		if err := tx.QueryRow(ctx, `select fields, trail, item_reason, direction_uncertain from documents.invoices where id = $1`, id).
			Scan(&fields, &trail, &reason, &inv.DirectionUncertain); err != nil {
			return err
		}
		if reason != nil {
			inv.ItemReason = *reason
		}
		if err := json.Unmarshal(fields, &inv.Fields); err != nil {
			return err
		}
		return json.Unmarshal(trail, &inv.Trail)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return inv, nil
}
