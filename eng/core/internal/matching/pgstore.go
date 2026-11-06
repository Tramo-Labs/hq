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

	"github.com/Tramo-Labs/hq/eng/core/internal/documents"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/money"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/audit"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
)

// PGStore persists items, runs and decisions, always through db.InTenant.
// It reads movements straight from ledger.movements: same database, same
// tenant transaction, read only.
type PGStore struct {
	DB *db.DB
	// Closed answers whether a period is closed. Nil means no period is
	// closed (the weekly close does not exist yet). See closed.
	Closed ClosedFunc

	// beforeInsert runs after every check of a decision and right before
	// its insert. Nil in production; tests use it to line two concurrent
	// decisions up so both pass the checks and collide on the insert.
	beforeInsert func()
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
		return audit.Record(ctx, tx, audit.Entry{Action: "matching.items_loaded", Subject: "matching.items:source=" + source,
			Detail: map[string]any{"source": source, "items": len(items)}})
	})
	return ids, err
}

// InvoiceItemTx stores the open item of an invoice inside the invoice's own
// transaction (documents.PGStore.OnSaved). Source "document", source id the
// invoice id, tied to it by invoice_id: the item follows the invoice, and
// the database refuses to change an item a match in effect settles.
func (s *PGStore) InvoiceItemTx(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID, d documents.ItemDraft) error {
	refs := d.Refs
	if refs == nil {
		refs = []string{}
	}
	_, err := tx.Exec(ctx,
		`insert into matching.items (tenant_id, source, source_id, invoice_id, kind, date, amount, refs, confidence, party)
		 values (app_current_tenant(), 'document', $1, $2, $3, $4, $5, $6, $7, $8)
		 on conflict (tenant_id, source, source_id) do update set kind = excluded.kind, date = excluded.date,
		   amount = excluded.amount, refs = excluded.refs, confidence = excluded.confidence, party = excluded.party`,
		invoiceID.String(), invoiceID, d.Kind, d.Date, int64(d.Amount), refs, d.Confidence, d.Party)
	return err
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

		// By the decisions in effect (reverted ones do not count):
		// settled is automatic, approved in either tray, or a manual link in
		// effect; turned down is rejected in either tray or "other" from the
		// client. Escalated and undecided is neither: it waits in Exceptions.
		rows, err = tx.Query(ctx,
			`select m.rule_id, m.rule_version, m.tray, m.movement_ids, m.item_ids,
			        coalesce(bool_or(d.action = 'approve'), false)
			          or (m.tray = 'manual' and exists (select 1 from matching.movement_decision_state s
			              where s.tenant_id = m.tenant_id and s.match_id = m.id and s.action = 'link')),
			        coalesce(bool_or(d.action in ('reject', 'other')), false)
			   from matching.matches m left join matching.decision_state d on d.tenant_id = m.tenant_id and d.match_id = m.id
			  group by m.tenant_id, m.id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m Match
			var tray string
			var approved, turnedDown bool
			var mv, it []uuid.UUID
			if err := rows.Scan(&m.Rule.ID, &m.Rule.Version, &tray, &mv, &it, &approved, &turnedDown); err != nil {
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
			switch {
			case turnedDown:
				in.Rejected = append(in.Rejected, m)
			case approved || m.Tray == Auto:
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

// ErrForbidden means the caller's role cannot decide on that tray.
var ErrForbidden = errors.New("matching: your role cannot decide on this tray")

// CanDecide follows product/ESPECIFICACION.md sections 1 and 5: Suggested
// is decided by an operator, Exceptions by the client (owner or approver),
// and nobody decides on Automatic.
func CanDecide(role string, tray Tray) bool {
	switch tray {
	case Suggested:
		return role == identity.RoleOperator
	case Exception:
		return role == identity.RoleOwner || role == identity.RoleApprover
	}
	return false
}

// actionFits says which actions each tray offers.
func actionFits(a Action, tray Tray) bool {
	switch a {
	case Approve, Reject:
		return true
	case Escalate:
		return tray == Suggested
	case Other:
		return tray == Exception
	}
	return false
}

// ErrActionNotInTray means the action is not offered in the item's tray
// (escalate outside Suggested, other outside Exceptions or without a note).
var ErrActionNotInTray = errors.New("matching: this action is not available in the item's tray")

// DecisionInput is everything a caller may say about a decision. Who
// decides and with which role are never input: the user comes from the
// scope and the role from the membership, read in the same transaction.
type DecisionInput struct {
	Key    string // idempotency key
	Match  Match
	Action Action
	Note   string
}

var (
	// ErrNothingToRevert: the item has no decision in effect.
	ErrNothingToRevert = errors.New("matching: there is no decision in effect to revert")
	// ErrClosed: the period of the item is closed; what closed is not reverted.
	ErrClosed = errors.New("matching: the period is closed")
)

// ClosedFunc tells whether any of the periods (YYYY-MM) is closed for the
// tenant in the context.
type ClosedFunc func(ctx context.Context, tx pgx.Tx, periods []string) (bool, error)

// closed is the single place where a reversion checks the close. The
// weekly close does not exist yet, so with no Closed hook it always lets
// the reversion through. When it exists, this is where it plugs in: what
// already closed is not reverted.
func (s *PGStore) closed(ctx context.Context, tx pgx.Tx, periods []string) (bool, error) {
	if s.Closed == nil {
		return false, nil
	}
	return s.Closed(ctx, tx, periods)
}

// memberRole is the caller's role read from memberships in tx, not from the
// token: a token can be up to ten minutes old, and someone whose role was
// lowered cannot decide for one more minute.
func memberRole(ctx context.Context, tx pgx.Tx) (string, error) {
	var role string
	err := tx.QueryRow(ctx,
		`select role::text from memberships where user_id = app_current_user() and tenant_id = app_current_tenant()`).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrForbidden
	}
	return role, err
}

// seesSuggested reports whether the caller may see the Suggested tray:
// only an operator (product spec section 1: the client never sees
// Suggested, nor internal rules not yet confirmed; the accountant neither).
// The role is the membership's, read in tx like for decisions. No
// membership (a job, a stranger) sees nothing of it.
func seesSuggested(ctx context.Context, tx pgx.Tx) (bool, error) {
	role, err := memberRole(ctx, tx)
	if errors.Is(err, ErrForbidden) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return role == identity.RoleOperator, nil
}

// mustSeeTray refuses a Suggested listing to anyone but an operator.
func mustSeeTray(ctx context.Context, tx pgx.Tx, tray Tray) error {
	if tray != Suggested {
		return nil
	}
	ok, err := seesSuggested(ctx, tx)
	if err != nil {
		return err
	}
	if !ok {
		return ErrForbidden
	}
	return nil
}

// trayState is the decision in effect in one tray of a match.
type trayState struct {
	id       int64
	revision int    // latest revision, 0 when the tray has no rows
	action   Action // "" when nothing is in effect (none, or reverted)
}

// matchState reads the stored tray of a match, the decision in effect in
// each tray, and the periods of its movements.
func matchState(ctx context.Context, tx pgx.Tx, id string) (Tray, map[Tray]trayState, []string, error) {
	var stored string
	var periods []string
	err := tx.QueryRow(ctx,
		`select tray, coalesce(array(select jsonb_array_elements_text(evidence->'movement_periods')), '{}')
		   from matching.matches where id = $1`, id).Scan(&stored, &periods)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, nil, fmt.Errorf("%w: unknown match", ErrInvalid)
	}
	if err != nil {
		return "", nil, nil, err
	}
	states := map[Tray]trayState{}
	rows, err := tx.Query(ctx, `select tray, id, revision, coalesce(action, '') from matching.decision_state where match_id = $1`, id)
	if err != nil {
		return "", nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tray, action string
		var st trayState
		if err := rows.Scan(&tray, &st.id, &st.revision, &action); err != nil {
			return "", nil, nil, err
		}
		st.action = Action(action)
		states[Tray(tray)] = st
	}
	return Tray(stored), states, periods, rows.Err()
}

// Record stores a human decision on a match, or the reversion of the one in
// effect (Action Revert). Rules: the same decision sent twice is applied
// once; a key is not reused for something else; nothing is edited, a
// reversion is a new row. The decider is the scope's user, the role is the
// membership's (read here), the tray is the stored one plus any escalation.
//
// Revert undoes the latest decision in effect: the client's in Exceptions
// if there is one, otherwise the operator's in Suggested (including an
// escalation, which sends the match back to Suggested). Only who decides
// that tray may revert it, and never in a closed period.
//
// Known limit: once decisions are written to the client's ERP, reverting
// one that was written will need a compensating entry there, never a
// deletion. ERP writes do not exist yet.
func (s *PGStore) Record(ctx context.Context, in DecisionInput) (Decision, bool, error) {
	sc, ok := db.ScopeFrom(ctx)
	if !ok {
		return Decision{}, false, db.ErrNoScope
	}
	if sc.UserID == uuid.Nil {
		return Decision{}, false, ErrNoUser
	}
	key, err := uuid.Parse(in.Key)
	if err != nil || in.Match.ID == "" ||
		in.Match.ID != matchID(in.Match.Rule, in.Match.MovementIDs, in.Match.ItemIDs) {
		return Decision{}, false, ErrInvalid
	}
	switch in.Action {
	case Approve, Reject, Escalate, Other, Revert:
	default:
		return Decision{}, false, ErrInvalid
	}
	d := Decision{Key: in.Key, MatchID: in.Match.ID, Match: in.Match, Action: in.Action, Note: in.Note,
		By: sc.UserID.String()}
	snap, err := json.Marshal(d.Match)
	if err != nil {
		return Decision{}, false, err
	}
	applied := false
	err = s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		// Replay first: the same key, match, action and user answers what
		// was stored, before anything else is looked at again.
		var prevMatch, prevAction, prevRole, prevNote, prevTray string
		var prevUser uuid.UUID
		var prevAt time.Time
		err := tx.QueryRow(ctx, `select match_id, action, user_id, role, at, note, tray from matching.decisions where key = $1`, key).
			Scan(&prevMatch, &prevAction, &prevUser, &prevRole, &prevAt, &prevNote, &prevTray)
		if err == nil {
			if prevMatch == d.MatchID && prevAction == string(d.Action) && prevUser == sc.UserID {
				d.At, d.Role, d.Note, d.Tray = prevAt, prevRole, prevNote, Tray(prevTray)
				return nil
			}
			return ErrKeyReused
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		stored, states, periods, err := matchState(ctx, tx, d.MatchID)
		if err != nil {
			return err
		}
		if d.Role, err = memberRole(ctx, tx); err != nil {
			return err
		}

		var target trayState
		var reverts *int64
		if d.Action == Revert {
			switch {
			case states[Exception].action != "":
				d.Tray = Exception
			case states[Suggested].action != "":
				d.Tray = Suggested
			default:
				return ErrNothingToRevert
			}
			target = states[d.Tray]
			if !CanDecide(d.Role, d.Tray) {
				return ErrForbidden
			}
			isClosed, err := s.closed(ctx, tx, periods)
			if err != nil {
				return err
			}
			if isClosed {
				return ErrClosed
			}
			reverts = &target.id
		} else {
			// The tray is the stored one, never the caller's: a suggested
			// match is in Exceptions while an escalation is in effect.
			d.Tray = stored
			if stored == Suggested && states[Suggested].action == Escalate {
				d.Tray = Exception
			}
			if !CanDecide(d.Role, d.Tray) {
				return ErrForbidden
			}
			if !actionFits(d.Action, d.Tray) || (d.Action == Other && d.Note == "") {
				return ErrActionNotInTray
			}
			target = states[d.Tray]
			if target.action != "" {
				return ErrAlreadyDecided
			}
		}
		d.Revision = target.revision + 1
		if s.beforeInsert != nil {
			s.beforeInsert()
		}

		err = tx.QueryRow(ctx,
			`insert into matching.decisions (tenant_id, key, match_id, tray, revision, action, user_id, role, note, snapshot, reverts)
			 values (app_current_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10) returning at`,
			key, d.MatchID, string(d.Tray), d.Revision, string(d.Action), sc.UserID, d.Role, d.Note, snap, reverts).Scan(&d.At)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "decisions_tenant_id_match_id_tray_revision_key" {
			return ErrAlreadyDecided // another decision landed on the same revision first
		}
		if err != nil {
			return err
		}
		applied = true
		return audit.Record(ctx, tx, audit.Entry{Action: "matching." + string(d.Action), Subject: "matching.matches:" + d.MatchID,
			Detail: map[string]any{"rule": d.Match.Rule.String(), "role": d.Role, "tray": string(d.Tray), "revision": d.Revision}})
	})
	if err != nil {
		return Decision{}, false, err
	}
	return d, applied, nil
}

// ErrNotFound means the match does not exist for the tenant in the context.
var ErrNotFound = errors.New("matching: not found")

const matchColumns = `id, rule_id, rule_version, tray, movement_ids, item_ids, adjustments, evidence`

func scanMatch(row pgx.Row) (Match, error) {
	var m Match
	var tray string
	var mv, it []uuid.UUID
	var adj, ev []byte
	if err := row.Scan(&m.ID, &m.Rule.ID, &m.Rule.Version, &tray, &mv, &it, &adj, &ev); err != nil {
		return m, err
	}
	m.Tray = Tray(tray)
	for _, u := range mv {
		m.MovementIDs = append(m.MovementIDs, u.String())
	}
	for _, u := range it {
		m.ItemIDs = append(m.ItemIDs, u.String())
	}
	if err := json.Unmarshal(adj, &m.Adjustments); err != nil {
		return m, err
	}
	var e struct {
		Reference     string   `json:"reference"`
		DateDiffDays  int      `json:"date_diff_days"`
		CrossPeriod   bool     `json:"cross_period"`
		MovePeriods   []string `json:"movement_periods"`
		ItemPeriods   []string `json:"item_periods"`
		LowConfidence bool     `json:"low_confidence"`
	}
	if err := json.Unmarshal(ev, &e); err != nil {
		return m, err
	}
	m.Reference, m.DateDiffDays, m.CrossPeriod = e.Reference, e.DateDiffDays, e.CrossPeriod
	m.MovePeriods, m.ItemPeriods, m.LowConfidence = e.MovePeriods, e.ItemPeriods, e.LowConfidence
	return m, nil
}

// GetMatch returns a stored match of the tenant in the context.
func (s *PGStore) GetMatch(ctx context.Context, id string) (Match, error) {
	var m Match
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		var err error
		m, err = scanMatch(tx.QueryRow(ctx, `select `+matchColumns+` from matching.matches where id = $1`, id))
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// Undecided lists the matches of a tray that nobody decided on yet.
func (s *PGStore) Undecided(ctx context.Context, tray Tray, limit int) ([]Match, error) {
	return s.UndecidedIn(ctx, tray, "", limit)
}

// UndecidedIn is Undecided restricted to matches with a movement dated in
// period (YYYY-MM); "" means every period.
func (s *PGStore) UndecidedIn(ctx context.Context, tray Tray, period string, limit int) ([]Match, error) {
	var out []Match
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		if err := mustSeeTray(ctx, tx, tray); err != nil {
			return err
		}
		// auto: nobody decides, listed for review. suggested: no operator
		// decision yet. exception: escalated, no client decision yet.
		rows, err := tx.Query(ctx, `select `+matchColumns+` from matching.matches m
			where case $1
			  when 'auto' then m.tray = 'auto'
			  when 'suggested' then m.tray = 'suggested' and not exists (select 1 from matching.decision_state d
			    where d.tenant_id = m.tenant_id and d.match_id = m.id and d.tray = 'suggested' and d.action is not null)
			  when 'exception' then exists (select 1 from matching.decision_state d
			    where d.tenant_id = m.tenant_id and d.match_id = m.id and d.tray = 'suggested' and d.action = 'escalate')
			    and not exists (select 1 from matching.decision_state d
			    where d.tenant_id = m.tenant_id and d.match_id = m.id and d.tray = 'exception' and d.action is not null)
			  else false end
			  and ($3 = '' or exists (select 1 from ledger.movements mv
			    where mv.id = any(m.movement_ids) and to_char(mv.date, 'YYYY-MM') = $3))
			order by created_at, id limit $2`, string(tray), limit, period)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			m, err := scanMatch(rows)
			if err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, err
}

// PendingRow is a pending movement as stored by a run.
type PendingRow struct {
	RunID uuid.UUID
	Note  string // the client's note, for ReasonClientOther
	// The movement itself, so each pending row can be told apart.
	Date        time.Time
	Amount      money.Amount
	Description string
	AccountID   uuid.UUID
	AccountName string
	Bank        string
	Pending
}

// ReasonClientOther: the client said the movement is something else; it
// is back with the operator, with the client's note.
const ReasonClientOther PendingReason = "client_other"

// ReasonOperatorReturned: the operator sent a client_other movement back
// to the client, with the operator's note.
const ReasonOperatorReturned PendingReason = "operator_returned"

// LatestPending lists the movements waiting in a tray according to the
// latest run of each period.
func (s *PGStore) LatestPending(ctx context.Context, tray Tray, limit int) ([]PendingRow, error) {
	return s.LatestPendingIn(ctx, tray, "", limit)
}

// LatestPendingIn is LatestPending for the latest run of one period
// (YYYY-MM); "" means the latest run of every period.
func (s *PGStore) LatestPendingIn(ctx context.Context, tray Tray, period string, limit int) ([]PendingRow, error) {
	var out []PendingRow
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		if err := mustSeeTray(ctx, tx, tray); err != nil {
			return err
		}
		// A movement the client decided leaves the Exceptions tray; if the
		// decision in effect is "other", it shows in Suggested for the
		// operator with the client's note. If the operator returned it, it
		// is back in Exceptions with the operator's note; if the operator
		// resolved or linked it, it is in neither. A reverted decision puts
		// the one before it back in effect.
		rows, err := tx.Query(ctx, `
			with latest as (select distinct on (period) id from matching.runs
			                 where $3 = '' or period = $3 order by period, started_at desc, id),
			     cur as (select p.*, s.action as decided, s.note as client_note from matching.pending p
			               left join matching.movement_decision_state s on s.tenant_id = p.tenant_id and s.movement_id = p.movement_id
			              where p.run_id in (select id from latest))
			select cur.run_id, cur.movement_id,
			       case when cur.tray = 'exception' and decided = 'other' then 'suggested' else cur.tray end,
			       case when cur.tray = 'exception' and decided = 'other' then 'client_other'
			            when cur.tray = 'exception' and decided = 'return' then 'operator_returned' else reason end,
			       candidates, coalesce(client_note, ''),
			       l.date, l.amount, l.description, a.id, a.name, a.bank
			  from cur
			  join ledger.movements l on l.id = cur.movement_id
			  join ledger.accounts a on a.id = l.account_id
			 where case $1
			         when 'exception' then tray = 'exception' and (decided is null or decided = 'return')
			         when 'suggested' then tray = 'suggested' or (tray = 'exception' and decided = 'other')
			         else false end
			 order by movement_id limit $2`, string(tray), limit, period)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r PendingRow
			var mid uuid.UUID
			var t, reason string
			var cands []byte
			var amount int64
			if err := rows.Scan(&r.RunID, &mid, &t, &reason, &cands, &r.Note,
				&r.Date, &amount, &r.Description, &r.AccountID, &r.AccountName, &r.Bank); err != nil {
				return err
			}
			r.Amount = money.Amount(amount)
			r.MovementID, r.Tray, r.Reason = mid.String(), Tray(t), PendingReason(reason)
			if r.Reason != ReasonClientOther && r.Reason != ReasonOperatorReturned {
				r.Note = ""
			}
			if err := json.Unmarshal(cands, &r.Candidates); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
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
