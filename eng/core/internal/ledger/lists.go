package ledger

import (
	"context"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/ledger/store"
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
		rows, err := store.New(tx).ListFiles(ctx, store.ListFilesParams{
			Period: f.Period, Status: string(f.Status), MaxRows: maxRows(limit),
		})
		for _, r := range rows {
			out = append(out, FileRow{ID: r.ID, AccountID: r.AccountID, AccountName: r.AccountName, Bank: Bank(r.Bank),
				Filename: r.Filename, Status: Status(r.Status), Reason: r.Reason, Detail: r.Detail, LayoutID: r.LayoutID,
				From: r.PeriodFrom, To: r.PeriodTo, Movements: int(r.Movements), ReceivedAt: r.ReceivedAt,
				UploadedBy: r.UploadedBy, UploadedByEmail: r.UploadedByEmail})
		}
		return err
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
		rows, err := store.New(tx).ListMovements(ctx, store.ListMovementsParams{
			DateFrom: from, DateTo: to, AccountID: account, MaxRows: maxRows(limit),
		})
		for _, r := range rows {
			out = append(out, MovementRow{ID: r.ID, AccountID: r.AccountID, FileID: r.FileID, AccountName: r.AccountName,
				Bank: Bank(r.Bank), Line: int(r.Line), Date: r.Date, Amount: money.Amount(r.Amount),
				Balance: amountOf(r.Balance), Description: r.Description, Reference: r.Reference, ExternalRef: r.ExternalRef})
		}
		return err
	})
	if err != nil {
		return nil, false, err
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// maxRows asks for one row past limit, to tell whether there were more.
func maxRows(limit int) int32 {
	return int32(min(limit, math.MaxInt32-1) + 1) // #nosec G115 -- clamped
}
