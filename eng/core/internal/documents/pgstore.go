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
	trail, err := json.Marshal(inv.Trail)
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
		err := tx.QueryRow(ctx,
			`insert into documents.invoices (tenant_id, sha256, fields, trail, auto_eligible, min_confidence,
			   issuer_cuit, doc_type, point_of_sale, number, issue_date, total)
			 values (app_current_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) returning id`,
			sum[:], fields, trail, inv.AutoEligible(), inv.MinConfidence(),
			keyValue(inv, IssuerCUIT), keyInt(inv, DocType), keyInt(inv, PointOfSale), keyInt(inv, Number),
			date, total).Scan(&id)
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
		return audit.Record(ctx, tx, audit.Entry{Action: "documents.invoice_stored", Subject: "documents.invoices:" + id.String(),
			Detail: map[string]any{"auto_eligible": inv.AutoEligible(), "sources": inv.Sources()}})
	})
	return id, err
}

// Get returns a stored invoice of the tenant in the context.
func (s *PGStore) Get(ctx context.Context, id uuid.UUID) (*Invoice, error) {
	inv := newInvoice()
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		var fields, trail []byte
		if err := tx.QueryRow(ctx, `select fields, trail from documents.invoices where id = $1`, id).Scan(&fields, &trail); err != nil {
			return err
		}
		if err := json.Unmarshal(fields, &inv.Fields); err != nil {
			return err
		}
		return json.Unmarshal(trail, &inv.Trail)
	})
	if err != nil {
		return nil, err
	}
	return inv, nil
}
