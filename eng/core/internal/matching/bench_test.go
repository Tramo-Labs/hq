package matching

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/money"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db/dbtest"
)

// BenchmarkPGHeavyQueries times the queries that read the trays and the
// panel on a synthetic tenant: three months of 1500 movements each, 60% with
// one exact item (automatic), 20% with two equal items (pending, ambiguous),
// 20% with none (exceptions), and the client's "other" on 100 exceptions of
// each month. Same data on both sides of a change; it measures, it does not
// assert speed.
func BenchmarkPGHeavyQueries(b *testing.B) {
	d := dbtest.New(b)
	ctx, owner := seedBench(b, d)
	s := &PGStore{DB: d}

	b.Run("LatestPendingSuggested", func(b *testing.B) {
		for b.Loop() {
			if _, err := s.LatestPendingIn(ctx, Suggested, "2026-09", 500); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("LatestPendingException", func(b *testing.B) {
		for b.Loop() {
			if _, err := s.LatestPendingIn(owner, Exception, "", 500); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Metrics", func(b *testing.B) {
		now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
		for b.Loop() {
			if _, err := s.Metrics(ctx, "2026-09", now); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("OpenItems", func(b *testing.B) {
		for b.Loop() {
			if _, _, err := s.OpenItems(ctx, "", "", 500); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("UndecidedSuggested", func(b *testing.B) {
		for b.Loop() {
			if _, err := s.UndecidedIn(ctx, Suggested, "2026-09", 500); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// seedBench loads the synthetic tenant and returns an operator's and an
// owner's context.
func seedBench(b *testing.B, d *db.DB) (op, owner context.Context) {
	b.Helper()
	bg := context.Background()
	tenant, opUser, ownUser := uuid.New(), uuid.New(), uuid.New()
	acct := uuid.New()
	const perMonth = 1500
	months := []time.Time{
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
	}
	var items []ItemIn
	err := d.System(bg, func(tx pgx.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`insert into tenants (id, slug, name) values ($1, 'bench', 'bench')`, []any{tenant}},
			{`insert into users (id, email) values ($1, 'op@bench.test'), ($2, 'owner@bench.test')`, []any{opUser, ownUser}},
			{`insert into memberships (user_id, tenant_id, role) values ($1, $3, 'operator'), ($2, $3, 'owner')`, []any{opUser, ownUser, tenant}},
		} {
			if _, err := tx.Exec(bg, q.sql, q.args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	op = session(db.Scope{TenantID: tenant, UserID: opUser}, identity.RoleOperator)
	owner = session(db.Scope{TenantID: tenant, UserID: ownUser}, identity.RoleOwner)
	// Ledger rows go in through the tenant's own transaction, as the app does.
	err = d.InTenant(op, func(tx pgx.Tx) error {
		if _, err := tx.Exec(bg, `insert into ledger.accounts (id, tenant_id, bank, name) values ($1, $2, 'bancor', 'Cuenta')`,
			acct, tenant); err != nil {
			return err
		}
		for mi, first := range months {
			file, st := uuid.New(), uuid.New()
			if _, err := tx.Exec(bg, `insert into ledger.files (id, tenant_id, account_id, sha256, status)
				values ($1, $2, $3, $4, 'accepted')`, file, tenant, acct, []byte(fmt.Sprintf("bench-%d", mi))); err != nil {
				return err
			}
			if _, err := tx.Exec(bg, `insert into ledger.statements (id, tenant_id, account_id, file_id, layout_id,
				opening, closing, opening_stated, closing_stated, running) values ($1, $2, $3, $4, 'bench', 0, 0, true, true, false)`,
				st, tenant, acct, file); err != nil {
				return err
			}
			rows := make([][]any, 0, perMonth)
			for i := range perMonth {
				date := first.AddDate(0, 0, i%28)
				amount := int64(mi*1_000_000+i)*100 + 17
				rows = append(rows, []any{tenant, acct, st, fmt.Sprintf("bench-%d-%d", mi, i), int32(i + 1), date, amount, "DEPOSITO", "", ""})
				switch {
				case i%5 < 3:
					items = append(items, ItemIn{SourceID: fmt.Sprintf("A-%d-%d", mi, i), Kind: "invoice", Date: date,
						Amount: money.Amount(amount), Confidence: 1})
				case i%5 == 3:
					for k := range 2 {
						items = append(items, ItemIn{SourceID: fmt.Sprintf("B-%d-%d-%d", mi, i, k), Kind: "invoice", Date: date,
							Amount: money.Amount(amount), Confidence: 1})
					}
				}
			}
			// COPY is refused under row level security: one batch instead.
			batch := &pgx.Batch{}
			for _, r := range rows {
				batch.Queue(`insert into ledger.movements (tenant_id, account_id, statement_id, key, line, date, amount,
					description, reference, external_ref) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, r...)
			}
			if err := tx.SendBatch(bg, batch).Close(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	s := &PGStore{DB: d}
	if _, err := s.UpsertItems(op, "erp", items); err != nil {
		b.Fatal(err)
	}
	for _, m := range months {
		if _, err := s.RunMonth(op, m.Format("2006-01"), DefaultConfig); err != nil {
			b.Fatal(err)
		}
	}
	// The client's "other" on 100 exceptions of each month.
	for _, m := range months {
		rows, err := s.LatestPendingIn(owner, Exception, m.Format("2006-01"), 100)
		if err != nil || len(rows) != 100 {
			b.Fatalf("%s: %d exceptions (%v)", m.Format("2006-01"), len(rows), err)
		}
		for _, r := range rows {
			if _, _, err := s.DecideMovement(owner, MovementDecisionInput{Key: uuid.NewString(), MovementID: r.MovementID,
				Action: MovementOther, Note: "otra cosa"}); err != nil {
				b.Fatal(err)
			}
		}
	}
	return op, owner
}
