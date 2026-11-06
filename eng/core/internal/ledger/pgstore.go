package ledger

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Tramo-Labs/hq/eng/core/internal/closes"
	"github.com/Tramo-Labs/hq/eng/core/internal/ledger/store"
	"github.com/Tramo-Labs/hq/eng/core/internal/money"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/audit"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/text"
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
		var err error
		seen, err = store.New(tx).FileAccepted(ctx, h)
		return err
	})
	return seen, err
}

func (s *PGStore) MovementsSeen(ctx context.Context, tenantID, accountID string, keys []string) (map[string]string, error) {
	acc, err := uuid.Parse(accountID)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	err = s.inTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := store.New(tx).MovementsWithKeys(ctx, store.MovementsWithKeysParams{AccountID: acc, Keys: keys})
		for _, r := range rows {
			out[r.Key] = r.Description
		}
		return err
	})
	return out, err
}

func (s *PGStore) LastStatement(ctx context.Context, tenantID, accountID string) (*StatementSummary, error) {
	acc, err := uuid.Parse(accountID)
	if err != nil {
		return nil, err
	}
	var sum *StatementSummary
	err = s.inTenant(ctx, tenantID, func(tx pgx.Tx) error {
		r, err := store.New(tx).LastStatement(ctx, acc)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		sum = &StatementSummary{Closing: money.Amount(r.Closing)}
		if r.PeriodFrom != nil {
			sum.From = *r.PeriodFrom
		}
		sum.To = *r.PeriodTo // the query keeps only statements with an end
		return nil
	})
	return sum, err
}

func (s *PGStore) SaveAccepted(ctx context.Context, u Upload, r Result, fresh []Movement) error {
	h, err := hex.DecodeString(r.FileHash)
	if err != nil {
		return err
	}
	acc, err := uuid.Parse(u.AccountID)
	if err != nil {
		return err
	}
	st := r.Statement
	return s.inTenant(ctx, u.TenantID, func(tx pgx.Tx) error {
		q := store.New(tx)
		// New movements in a closed period do not enter. Asked here, in the
		// transaction that writes them and under the close's lock, so a
		// close cannot count the period while they are on their way in.
		dates := make([]time.Time, len(fresh))
		for i, m := range fresh {
			dates[i] = m.Date
		}
		if p, closed, err := closes.ClosedPeriodOf(ctx, tx, dates); err != nil {
			return err
		} else if closed {
			return &PeriodClosedError{Period: ClosedPeriod{Start: p.Start, End: p.End}}
		}
		fileID, err := q.InsertAcceptedFile(ctx, store.InsertAcceptedFileParams{
			AccountID: acc, Sha256: h, Filename: &u.Filename, Fingerprint: &r.Fingerprint, LayoutID: &r.LayoutID,
		})
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "files_accepted_once" {
			return ErrRaced
		}
		if err != nil {
			return fmt.Errorf("ledger: insert file: %w", err)
		}
		stID, err := q.InsertStatement(ctx, store.InsertStatementParams{
			AccountID: acc, FileID: fileID, LayoutID: st.LayoutID, PeriodFrom: nullDate(st.From), PeriodTo: nullDate(st.To),
			Opening: int64(st.Opening), Closing: int64(st.Closing),
			OpeningStated: st.OpeningStated, ClosingStated: st.ClosingStated, Running: st.Running,
		})
		if err != nil {
			return fmt.Errorf("ledger: insert statement: %w", err)
		}
		rows := make([]store.InsertMovementParams, 0, len(fresh))
		for _, m := range fresh {
			line, err := int32Of(m.Line)
			if err != nil {
				return err
			}
			var fees []byte
			if len(m.Fees) > 0 {
				if fees, err = json.Marshal(m.Fees); err != nil {
					return err
				}
			}
			rows = append(rows, store.InsertMovementParams{
				AccountID: acc, StatementID: stID, Key: m.Key, Line: line, Date: m.Date, Amount: int64(m.Amount),
				Balance: cents(m.Balance), Description: m.Description, Reference: m.Reference,
				ExternalRef: m.ExternalRef, Gross: cents(m.Gross), Fees: fees,
			})
		}
		var batchErr error
		q.InsertMovement(ctx, rows).Exec(func(_ int, err error) {
			if err != nil && batchErr == nil {
				batchErr = err
			}
		})
		if batchErr != nil {
			return fmt.Errorf("ledger: insert movements: %w", batchErr)
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
	acc, err := uuid.Parse(u.AccountID)
	if err != nil {
		return err
	}
	var closedStart, closedEnd *time.Time
	if r.ClosedPeriod != nil {
		closedStart, closedEnd = &r.ClosedPeriod.Start, &r.ClosedPeriod.End
	}
	return s.inTenant(ctx, u.TenantID, func(tx pgx.Tx) error {
		fileID, err := store.New(tx).InsertQuarantinedFile(ctx, store.InsertQuarantinedFileParams{
			AccountID: acc, Sha256: h, Filename: &u.Filename, Reason: &r.Reason, Detail: &r.Detail,
			Fingerprint: &r.Fingerprint, LayoutID: nullText(r.LayoutID),
			ClosedStart: closedStart, ClosedEnd: closedEnd,
		})
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
		rows, err := store.New(tx).MovementsBetween(ctx, store.MovementsBetweenParams{DateFrom: from, DateTo: to})
		if err != nil {
			return err
		}
		for _, r := range rows {
			m := StoredMovement{ID: r.ID, AccountID: r.AccountID, Movement: Movement{
				Key: r.Key, Line: int(r.Line), Date: r.Date, Amount: money.Amount(r.Amount),
				Balance: amountOf(r.Balance), Description: r.Description, Reference: r.Reference,
				ExternalRef: r.ExternalRef, Gross: amountOf(r.Gross),
			}}
			if len(r.Fees) > 0 {
				if err := json.Unmarshal(r.Fees, &m.Fees); err != nil {
					return err
				}
			}
			out = append(out, m)
		}
		return nil
	})
	return out, err
}

// MaxAccountName is the longest account name, in characters (the spec's
// maxLength).
const MaxAccountName = 120

// CreateAccount registers an account for the tenant in the context. The
// name follows the rule of a person's name (text.Name, F27): something
// visible, no zero width or bidi characters; it is stored trimmed.
func (s *PGStore) CreateAccount(ctx context.Context, bank Bank, number, name string) (uuid.UUID, error) {
	name, err := text.Name(name, MaxAccountName)
	if err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err = s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		var err error
		id, err = store.New(tx).InsertAccount(ctx, store.InsertAccountParams{Bank: string(bank), Number: nullText(number), Name: name})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "ledger.account_created", Subject: "ledger.accounts:" + id.String()})
	})
	return id, err
}

// Account is a registered account.
type Account struct {
	ID       uuid.UUID
	Bank     Bank
	Number   string
	Name     string
	Currency string
}

// ErrNotFound means the row does not exist for the tenant in the context.
var ErrNotFound = errors.New("ledger: not found")

// Accounts lists the accounts of the tenant in the context.
func (s *PGStore) Accounts(ctx context.Context) ([]Account, error) {
	var out []Account
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		rows, err := store.New(tx).ListAccounts(ctx)
		for _, r := range rows {
			out = append(out, Account{ID: r.ID, Bank: Bank(r.Bank), Number: r.Number, Name: r.Name, Currency: r.Currency})
		}
		return err
	})
	return out, err
}

// GetAccount returns one account of the tenant in the context.
func (s *PGStore) GetAccount(ctx context.Context, id uuid.UUID) (Account, error) {
	a := Account{ID: id}
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		r, err := store.New(tx).GetAccount(ctx, id)
		a.Bank, a.Number, a.Name, a.Currency = Bank(r.Bank), r.Number, r.Name, r.Currency
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// QuarantinedFile is a file that did not enter.
type QuarantinedFile struct {
	ID, AccountID  uuid.UUID
	Filename       string
	Reason, Detail string
	Fingerprint    string
	ReceivedAt     time.Time
}

// Quarantine lists the quarantined files of the tenant, newest first.
func (s *PGStore) Quarantine(ctx context.Context, limit int) ([]QuarantinedFile, error) {
	var out []QuarantinedFile
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		rows, err := store.New(tx).ListQuarantine(ctx, int32(min(limit, math.MaxInt32))) // #nosec G115 -- clamped
		for _, r := range rows {
			out = append(out, QuarantinedFile{ID: r.ID, AccountID: r.AccountID, Filename: r.Filename, Reason: r.Reason,
				Detail: r.Detail, Fingerprint: r.Fingerprint, ReceivedAt: r.ReceivedAt})
		}
		return err
	})
	return out, err
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

// cents and amountOf convert between money and the nullable bigint columns.
func cents(a *money.Amount) *int64 {
	if a == nil {
		return nil
	}
	v := int64(*a)
	return &v
}

func amountOf(c *int64) *money.Amount {
	if c == nil {
		return nil
	}
	a := money.Amount(*c)
	return &a
}

// int32Of refuses a line number the int column cannot hold, as the database
// did before: the file does not enter rather than enter with a wrong line.
func int32Of(n int) (int32, error) {
	if n < math.MinInt32 || n > math.MaxInt32 {
		return 0, fmt.Errorf("ledger: line %d out of range", n)
	}
	return int32(n), nil
}
