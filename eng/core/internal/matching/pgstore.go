package matching

import (
	"context"
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

// PGStore persists items, runs and decisions, always through db.InTenant.
// It reads movements straight from ledger.movements: same database, same
// tenant transaction, read only.
type PGStore struct {
	DB *db.DB
}

// ItemIn is an item as a source delivers it (ERP open item, invoice).
type ItemIn struct {
	SourceID   string
	Kind       string
	Date, Due  time.Time
	Amount     money.Amount
	Refs       []string
	Confidence float64
	Party      string
}

// UpsertItems loads items from one source. Loading the same source id again
// updates the row instead of adding one.
func (s *PGStore) UpsertItems(ctx context.Context, source string, items []ItemIn) (map[string]uuid.UUID, error) {
	ids := map[string]uuid.UUID{}
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		for _, it := range items {
			var id uuid.UUID
			refs := it.Refs
			if refs == nil {
				refs = []string{}
			}
			err := tx.QueryRow(ctx,
				`insert into matching.items (tenant_id, source, source_id, kind, date, due, amount, refs, confidence, party)
				 values (app_current_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9)
				 on conflict (tenant_id, source, source_id) do update set kind = excluded.kind, date = excluded.date,
				   due = excluded.due, amount = excluded.amount, refs = excluded.refs,
				   confidence = excluded.confidence, party = excluded.party
				 returning id`,
				source, it.SourceID, it.Kind, it.Date, nullTime(it.Due), int64(it.Amount), refs, it.Confidence, it.Party).Scan(&id)
			if err != nil {
				return fmt.Errorf("matching: upsert item: %w", err)
			}
			ids[it.SourceID] = id
		}
		return nil
	})
	return ids, err
}

// LoadInput builds the engine input for movements dated in [from, to]:
// those movements, every item dated up to the window after the period, the
// settled matches (automatic ones nobody rejected, and approved ones) and
// the rejected ones.
func (s *PGStore) LoadInput(ctx context.Context, from, to time.Time, cfg Config) (Input, error) {
	var in Input
	pad := time.Duration(cfg.WindowDays) * 24 * time.Hour
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`select id, account_id, date, amount, description, reference, external_ref, gross, fees
			   from ledger.movements where date between $1 and $2`, from, to)
		if err != nil {
			return err
		}
		for rows.Next() {
			var m Movement
			var id, acct uuid.UUID
			var amount int64
			var gross *int64
			var fees []byte
			if err := rows.Scan(&id, &acct, &m.Date, &amount, &m.Description, &m.Reference, &m.ExternalRef, &gross, &fees); err != nil {
				rows.Close()
				return err
			}
			m.ID, m.AccountID, m.Amount = id.String(), acct.String(), money.Amount(amount)
			if gross != nil {
				g := money.Amount(*gross)
				m.Gross = &g
			}
			if len(fees) > 0 {
				if err := json.Unmarshal(fees, &m.Fees); err != nil {
					rows.Close()
					return err
				}
			}
			in.Movements = append(in.Movements, m)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		// Items from long before the period stay candidates for the
		// reference pass (Duotach case 5); the date window is the engine's.
		rows, err = tx.Query(ctx,
			`select id, kind, date, due, amount, refs, confidence, party from matching.items
			  where coalesce(due, date) <= $1`, to.Add(pad))
		if err != nil {
			return err
		}
		for rows.Next() {
			var it Item
			var id uuid.UUID
			var due *time.Time
			var amount int64
			var conf float32
			if err := rows.Scan(&id, &it.Kind, &it.Date, &due, &amount, &it.Refs, &conf, &it.Party); err != nil {
				rows.Close()
				return err
			}
			it.ID, it.Amount, it.Confidence = id.String(), money.Amount(amount), float64(conf)
			if due != nil {
				it.Due = *due
			}
			in.Items = append(in.Items, it)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		rows, err = tx.Query(ctx,
			`select m.rule_id, m.rule_version, m.tray, m.movement_ids, m.item_ids, d.action
			   from matching.matches m left join matching.decisions d on d.tenant_id = m.tenant_id and d.match_id = m.id
			  where m.tray = 'auto' or d.action is not null`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m Match
			var tray string
			var action *string
			var mv, it []uuid.UUID
			if err := rows.Scan(&m.Rule.ID, &m.Rule.Version, &tray, &mv, &it, &action); err != nil {
				return err
			}
			m.Tray = Tray(tray)
			for _, u := range mv {
				m.MovementIDs = append(m.MovementIDs, u.String())
			}
			for _, u := range it {
				m.ItemIDs = append(m.ItemIDs, u.String())
			}
			m.ID = matchID(m.Rule, m.MovementIDs, m.ItemIDs)
			if action != nil && *action == string(Reject) {
				in.Rejected = append(in.Rejected, m)
			} else { // automatic, or approved
				in.Settled = append(in.Settled, m)
			}
		}
		return rows.Err()
	})
	return in, err
}

// SaveRun stores the outcome of a run. Matches already stored (same ID) are
// left as they are; it returns how many matches are new.
func (s *PGStore) SaveRun(ctx context.Context, period string, cfg Config, r Result) (uuid.UUID, int, error) {
	var runID uuid.UUID
	inserted := 0
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return runID, 0, err
	}
	err = s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`insert into matching.runs (tenant_id, period, config) values (app_current_tenant(), $1, $2) returning id`,
			period, cfgJSON).Scan(&runID); err != nil {
			return err
		}
		for _, m := range r.Matches {
			mv, err := uuids(m.MovementIDs)
			if err != nil {
				return err
			}
			it, err := uuids(m.ItemIDs)
			if err != nil {
				return err
			}
			adj, err := json.Marshal(m.Adjustments)
			if err != nil {
				return err
			}
			ev, err := json.Marshal(map[string]any{"reference": m.Reference, "date_diff_days": m.DateDiffDays,
				"cross_period": m.CrossPeriod, "movement_periods": m.MovePeriods, "item_periods": m.ItemPeriods,
				"low_confidence": m.LowConfidence})
			if err != nil {
				return err
			}
			tag, err := tx.Exec(ctx,
				`insert into matching.matches (tenant_id, id, run_id, rule_id, rule_version, tray, movement_ids, item_ids, adjustments, evidence)
				 values (app_current_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9)
				 on conflict (tenant_id, id) do nothing`,
				m.ID, runID, m.Rule.ID, m.Rule.Version, string(m.Tray), mv, it, adj, ev)
			if err != nil {
				return fmt.Errorf("matching: insert match: %w", err)
			}
			inserted += int(tag.RowsAffected())
		}
		for _, p := range append(append([]Pending{}, r.Pending...), r.Exceptions...) {
			mid, err := uuid.Parse(p.MovementID)
			if err != nil {
				return err
			}
			cands, err := json.Marshal(p.Candidates)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx,
				`insert into matching.pending (tenant_id, run_id, movement_id, tray, reason, candidates)
				 values (app_current_tenant(), $1, $2, $3, $4, $5)`,
				runID, mid, string(p.Tray), string(p.Reason), cands); err != nil {
				return fmt.Errorf("matching: insert pending: %w", err)
			}
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "matching.run", Subject: "matching.runs:" + runID.String(),
			Detail: map[string]any{"period": period, "matches": len(r.Matches), "new": inserted,
				"pending": len(r.Pending), "exceptions": len(r.Exceptions)}})
	})
	return runID, inserted, err
}

// ErrNoUser means a decision was attempted without a person in the scope.
var ErrNoUser = errors.New("matching: a decision needs a user in the scope")

// Record stores a human decision with the same rules as Book: the same
// decision sent twice is applied once, a decided match is not decided again,
// a key is not reused for something else. The person is the scope's user,
// never a value from the request.
func (s *PGStore) Record(ctx context.Context, d Decision) (Decision, bool, error) {
	sc, ok := db.ScopeFrom(ctx)
	if !ok {
		return Decision{}, false, db.ErrNoScope
	}
	if sc.UserID == uuid.Nil {
		return Decision{}, false, ErrNoUser
	}
	key, err := uuid.Parse(d.Key)
	if err != nil || d.Match.ID == "" || (d.Action != Approve && d.Action != Reject) ||
		d.Match.ID != matchID(d.Match.Rule, d.Match.MovementIDs, d.Match.ItemIDs) {
		return Decision{}, false, ErrInvalid
	}
	d.MatchID, d.By = d.Match.ID, sc.UserID.String()
	snap, err := json.Marshal(d.Match)
	if err != nil {
		return Decision{}, false, err
	}
	applied := false
	err = s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		var prevMatch, prevAction, prevRole, prevNote string
		var prevUser uuid.UUID
		var prevAt time.Time
		err := tx.QueryRow(ctx, `select match_id, action, user_id, role, at, note from matching.decisions where key = $1`, key).
			Scan(&prevMatch, &prevAction, &prevUser, &prevRole, &prevAt, &prevNote)
		if err == nil {
			if prevMatch == d.MatchID && prevAction == string(d.Action) && prevUser == sc.UserID {
				d.At, d.Role, d.Note = prevAt, prevRole, prevNote
				return nil
			}
			return ErrKeyReused
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		err = tx.QueryRow(ctx,
			`insert into matching.decisions (tenant_id, key, match_id, action, user_id, role, note, snapshot)
			 values (app_current_tenant(), $1, $2, $3, $4, $5, $6, $7) returning at`,
			key, d.MatchID, string(d.Action), sc.UserID, d.Role, d.Note, snap).Scan(&d.At)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch {
			case pgErr.Code == "23505" && pgErr.ConstraintName == "decisions_tenant_id_match_id_key":
				return ErrAlreadyDecided
			case pgErr.Code == "23503":
				return fmt.Errorf("%w: unknown match", ErrInvalid)
			}
		}
		if err != nil {
			return err
		}
		applied = true
		return audit.Record(ctx, tx, audit.Entry{Action: "matching." + string(d.Action), Subject: "matching.matches:" + d.MatchID,
			Detail: map[string]any{"rule": d.Match.Rule.String(), "role": d.Role}})
	})
	if err != nil {
		return Decision{}, false, err
	}
	return d, applied, nil
}

func uuids(ids []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, len(ids))
	for i, s := range ids {
		u, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("matching: id %q is not a uuid", s)
		}
		out[i] = u
	}
	return out, nil
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
