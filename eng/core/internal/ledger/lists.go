package ledger

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// FileRow is an uploaded file as the "Archivos" list shows it (F12).
type FileRow struct {
	ID, AccountID   uuid.UUID
	AccountName     string
	Bank            Bank
	Filename        string
	Status          Status
	Reason, Detail  string
	LayoutID        string
	From, To        *time.Time // statement days, accepted only
	Movements       int        // movements this file stored, accepted only
	ReceivedAt      time.Time
	UploadedBy      *uuid.UUID
	UploadedByEmail string // empty when the uploader is no longer a member
}

// FileFilter narrows Files. Empty values mean any.
type FileFilter struct {
	Period string // YYYY-MM, received in that month (Argentina time)
	Status Status
}

// Files lists the tenant's uploaded files, newest first, up to limit rows;
// the bool says there were more.
func (s *PGStore) Files(ctx context.Context, f FileFilter, limit int) ([]FileRow, bool, error) {
	var out []FileRow
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			select f.id, f.account_id, a.name, a.bank, coalesce(f.filename, ''), f.status,
			       coalesce(f.reason, ''), coalesce(f.detail, ''), coalesce(f.layout_id, ''),
			       st.period_from, st.period_to,
			       (select count(*) from ledger.movements m where m.statement_id = st.id),
			       f.received_at, f.uploaded_by, coalesce(u.email, '')
			  from ledger.files f
			  join ledger.accounts a on a.id = f.account_id
			  left join ledger.statements st on st.file_id = f.id
			  left join users u on u.id = f.uploaded_by
			 where ($1 = '' or to_char(f.received_at at time zone 'America/Argentina/Buenos_Aires', 'YYYY-MM') = $1)
			   and ($2 = '' or f.status = $2)
			 order by f.received_at desc, f.id
			 limit $3`, f.Period, string(f.Status), limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r FileRow
			var bank, status string
			if err := rows.Scan(&r.ID, &r.AccountID, &r.AccountName, &bank, &r.Filename, &status,
				&r.Reason, &r.Detail, &r.LayoutID, &r.From, &r.To, &r.Movements,
				&r.ReceivedAt, &r.UploadedBy, &r.UploadedByEmail); err != nil {
				return err
			}
			r.Bank, r.Status = Bank(bank), Status(status)
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, false, err
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// MovementRow is a stored movement as the movements list shows it (F12).
type MovementRow struct {
	ID, AccountID, FileID uuid.UUID
	AccountName           string
	Bank                  Bank
	Line                  int
	Date                  time.Time
	Amount                money.Amount
	Balance               *money.Amount
	Description           string
	Reference             string
	ExternalRef           string
}

// Movements lists the tenant's movements dated from..to (inclusive),
// optionally of one account, in date, account and line order, up to limit
// rows; the bool says there were more. Unlike MovementsBetween (the full
// export), it is bounded and carries the file and line each came from.
func (s *PGStore) Movements(ctx context.Context, from, to time.Time, account *uuid.UUID, limit int) ([]MovementRow, bool, error) {
	var out []MovementRow
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			select m.id, m.account_id, st.file_id, a.name, a.bank, m.line, m.date, m.amount, m.balance,
			       m.description, m.reference, m.external_ref
			  from ledger.movements m
			  join ledger.accounts a on a.id = m.account_id
			  join ledger.statements st on st.id = m.statement_id
			 where m.date between $1 and $2 and ($3::uuid is null or m.account_id = $3)
			 order by m.date, a.name, m.account_id, st.created_at, m.line
			 limit $4`, from, to, account, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r MovementRow
			var bank string
			var amount int64
			var balance *int64
			if err := rows.Scan(&r.ID, &r.AccountID, &r.FileID, &r.AccountName, &bank, &r.Line, &r.Date, &amount,
				&balance, &r.Description, &r.Reference, &r.ExternalRef); err != nil {
				return err
			}
			r.Bank, r.Amount = Bank(bank), money.Amount(amount)
			if balance != nil {
				b := money.Amount(*balance)
				r.Balance = &b
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, false, err
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}
