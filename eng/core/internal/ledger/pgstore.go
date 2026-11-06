package ledger

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/audit"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
)

// PGStore is the Postgres Store. Every call runs in db.InTenant: the tenant
// comes from the context scope and row level security filters every query.
type PGStore struct {
	DB *db.DB
}

// ErrScopeMismatch means the tenant passed to the store is not the tenant
// of the context. It is a programming error.
var ErrScopeMismatch = errors.New("ledger: tenant does not match the context scope")

// ErrRaced means another upload of the same file was accepted between the
// duplicate check and the save. The caller reports it as a duplicate.
var ErrRaced = errors.New("ledger: the same file was accepted concurrently")

func (s *PGStore) inTenant(ctx context.Context, tenantID string, fn func(pgx.Tx) error) error {
	sc, ok := db.ScopeFrom(ctx)
	if !ok {
		return db.ErrNoScope
	}
	if sc.TenantID.String() != tenantID {
		return ErrScopeMismatch
	}
	return s.DB.InTenant(ctx, fn)
}

func (s *PGStore) FileSeen(ctx context.Context, tenantID, fileHash string) (bool, error) {
	h, err := hex.DecodeString(fileHash)
	if err != nil {
		return false, err
	}
	var seen bool
	err = s.inTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`select exists (select 1 from ledger.files where sha256 = $1 and status = 'accepted')`, h).Scan(&seen)
	})
	return seen, err
}

func (s *PGStore) MovementsSeen(ctx context.Context, tenantID, accountID string, keys []string) (map[string]bool, error) {
	out := map[string]bool{}
	err := s.inTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`select key from ledger.movements where account_id = $1 and key = any($2)`, accountID, keys)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				return err
			}
			out[k] = true
		}
		return rows.Err()
	})
	return out, err
}

func (s *PGStore) LastStatement(ctx context.Context, tenantID, accountID string) (*StatementSummary, error) {
	var sum *StatementSummary
	err := s.inTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var from, to *time.Time
		var closing int64
		err := tx.QueryRow(ctx,
			`select period_from, period_to, closing from ledger.statements
			  where account_id = $1 and period_to is not null
			  order by period_to desc, created_at desc limit 1`, accountID).Scan(&from, &to, &closing)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		sum = &StatementSummary{Closing: money.Amount(closing)}
		if from != nil {
			sum.From = *from
		}
		sum.To = *to
		return nil
	})
	return sum, err
}

func (s *PGStore) SaveAccepted(ctx context.Context, u Upload, r Result, fresh []Movement) error {
	h, err := hex.DecodeString(r.FileHash)
	if err != nil {
		return err
	}
	st := r.Statement
	return s.inTenant(ctx, u.TenantID, func(tx pgx.Tx) error {
		var fileID uuid.UUID
		err := tx.QueryRow(ctx,
			`insert into ledger.files (tenant_id, account_id, sha256, filename, status, fingerprint, layout_id)
			 values (app_current_tenant(), $1, $2, $3, 'accepted', $4, $5) returning id`,
			u.AccountID, h, u.Filename, r.Fingerprint, r.LayoutID).Scan(&fileID)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "files_accepted_once" {
			return ErrRaced
		}
		if err != nil {
			return fmt.Errorf("ledger: insert file: %w", err)
		}
		var stID uuid.UUID
		err = tx.QueryRow(ctx,
			`insert into ledger.statements (tenant_id, account_id, file_id, layout_id, period_from, period_to,
			   opening, closing, opening_stated, closing_stated, running)
			 values (app_current_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10) returning id`,
			u.AccountID, fileID, st.LayoutID, nullDate(st.From), nullDate(st.To), int64(st.Opening), int64(st.Closing),
			st.OpeningStated, st.ClosingStated, st.Running).Scan(&stID)
		if err != nil {
			return fmt.Errorf("ledger: insert statement: %w", err)
		}
		batch := &pgx.Batch{}
		for _, m := range fresh {
			var bal, gross *int64
			if m.Balance != nil {
				v := int64(*m.Balance)
				bal = &v
			}
			if m.Gross != nil {
				v := int64(*m.Gross)
				gross = &v
			}
			var fees []byte
			if len(m.Fees) > 0 {
				if fees, err = json.Marshal(m.Fees); err != nil {
					return err
				}
			}
			batch.Queue(`insert into ledger.movements (tenant_id, account_id, statement_id, key, line, date, amount,
			   balance, description, reference, external_ref, gross, fees)
			 values (app_current_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			 on conflict (tenant_id, account_id, key) do nothing`,
				u.AccountID, stID, m.Key, m.Line, m.Date, int64(m.Amount), bal, m.Description, m.Reference,
				m.ExternalRef, gross, fees)
		}
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return fmt.Errorf("ledger: insert movements: %w", err)
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "ledger.statement_accepted", Subject: "ledger.files:" + fileID.String(),
			Detail: map[string]any{"layout": r.LayoutID, "new": r.New, "already_present": r.AlreadyPresent}})
	})
}

func (s *PGStore) SaveQuarantined(ctx context.Context, u Upload, r Result) error {
	h, err := hex.DecodeString(r.FileHash)
	if err != nil {
		return err
	}
	return s.inTenant(ctx, u.TenantID, func(tx pgx.Tx) error {
		var fileID uuid.UUID
		err := tx.QueryRow(ctx,
			`insert into ledger.files (tenant_id, account_id, sha256, filename, status, reason, detail, fingerprint, layout_id)
			 values (app_current_tenant(), $1, $2, $3, 'quarantined', $4, $5, $6, $7) returning id`,
			u.AccountID, h, u.Filename, r.Reason, r.Detail, r.Fingerprint, nullText(r.LayoutID)).Scan(&fileID)
		if err != nil {
			return fmt.Errorf("ledger: insert quarantined file: %w", err)
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "ledger.file_quarantined", Subject: "ledger.files:" + fileID.String(),
			Detail: map[string]any{"reason": r.Reason}})
	})
}

// StoredMovement is a movement as stored, with its database id.
type StoredMovement struct {
	ID        uuid.UUID
	AccountID uuid.UUID
	Movement
}

// MovementsBetween lists the movements of every account of the tenant in the
// context with a date in [from, to].
func (s *PGStore) MovementsBetween(ctx context.Context, from, to time.Time) ([]StoredMovement, error) {
	var out []StoredMovement
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`select id, account_id, key, line, date, amount, balance, description, reference, external_ref, gross, fees
			   from ledger.movements where date between $1 and $2 order by date, id`, from, to)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m StoredMovement
			var amount int64
			var bal, gross *int64
			var fees []byte
			if err := rows.Scan(&m.ID, &m.AccountID, &m.Key, &m.Line, &m.Date, &amount, &bal, &m.Description,
				&m.Reference, &m.ExternalRef, &gross, &fees); err != nil {
				return err
			}
			m.Amount = money.Amount(amount)
			if bal != nil {
				b := money.Amount(*bal)
				m.Balance = &b
			}
			if gross != nil {
				g := money.Amount(*gross)
				m.Gross = &g
			}
			if len(fees) > 0 {
				if err := json.Unmarshal(fees, &m.Fees); err != nil {
					return err
				}
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, err
}

// CreateAccount registers an account for the tenant in the context.
func (s *PGStore) CreateAccount(ctx context.Context, bank Bank, number, name string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`insert into ledger.accounts (tenant_id, bank, number, name) values (app_current_tenant(), $1, $2, $3) returning id`,
			string(bank), nullText(number), name).Scan(&id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "ledger.account_created", Subject: "ledger.accounts:" + id.String()})
	})
	return id, err
}

func nullDate(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func nullText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
