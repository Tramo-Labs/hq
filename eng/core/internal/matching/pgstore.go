package matching

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Tramo-Labs/hq/eng/core/internal/documents"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/listrange"
	"github.com/Tramo-Labs/hq/eng/core/internal/matching/store"
	"github.com/Tramo-Labs/hq/eng/core/internal/money"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/audit"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/text"
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
			refs := it.Refs
			if refs == nil {
				refs = []string{}
			}
			id, err := store.New(tx).UpsertItem(ctx, store.UpsertItemParams{
				Source: source, SourceID: it.SourceID, Kind: it.Kind, Date: it.Date, Due: nullTime(it.Due),
				Amount: int64(it.Amount), Refs: refs, Confidence: float32(it.Confidence), Party: it.Party,
			})
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
	return store.New(tx).UpsertInvoiceItem(ctx, store.UpsertInvoiceItemParams{
		InvoiceID: invoiceID, Kind: d.Kind, Date: d.Date, Amount: int64(d.Amount), Refs: refs,
		Confidence: float32(d.Confidence), Party: d.Party,
	})
}

// LoadInput builds the engine input for movements dated in [from, to]:
// those movements, every item dated up to the window after the period, the
// settled matches (automatic ones nobody rejected, and approved ones) and
// the rejected ones.
func (s *PGStore) LoadInput(ctx context.Context, from, to time.Time, cfg Config) (Input, error) {
	var in Input
	pad := time.Duration(cfg.WindowDays) * 24 * time.Hour
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		mvs, err := q.InputMovements(ctx, store.InputMovementsParams{DateFrom: from, DateTo: to})
		if err != nil {
			return err
		}
		for _, r := range mvs {
			m := Movement{ID: r.ID.String(), AccountID: r.AccountID.String(), Date: r.Date, Amount: money.Amount(r.Amount),
				Description: r.Description, Reference: r.Reference, ExternalRef: r.ExternalRef}
			if r.Gross != nil {
				g := money.Amount(*r.Gross)
				m.Gross = &g
			}
			if len(r.Fees) > 0 {
				if err := json.Unmarshal(r.Fees, &m.Fees); err != nil {
					return err
				}
			}
			in.Movements = append(in.Movements, m)
		}

		// Items from long before the period stay candidates for the
		// reference pass (Duotach case 5); the date window is the engine's.
		its, err := q.InputItems(ctx, to.Add(pad))
		if err != nil {
			return err
		}
		for _, r := range its {
			it := Item{ID: r.ID.String(), Kind: r.Kind, Date: r.Date, Amount: money.Amount(r.Amount), Refs: r.Refs,
				Confidence: float64(r.Confidence), Party: r.Party}
			if r.Due != nil {
				it.Due = *r.Due
			}
			in.Items = append(in.Items, it)
		}

		// By the decisions in effect (reverted ones do not count): settled
		// is a match in effect (matching.matches_in_effect); turned down is
		// rejected in either tray or "other" from the client. Escalated and
		// undecided is neither: it waits in Exceptions.
		ms, err := q.InputMatches(ctx)
		if err != nil {
			return err
		}
		for _, r := range ms {
			m := Match{Rule: Rule{ID: r.RuleID, Version: int(r.RuleVersion)}, Tray: Tray(r.Tray),
				MovementIDs: strs(r.MovementIds), ItemIDs: strs(r.ItemIds)}
			m.ID = matchID(m.Rule, m.MovementIDs, m.ItemIDs)
			switch {
			case r.TurnedDown:
				in.Rejected = append(in.Rejected, m)
			case r.InEffect:
				in.Settled = append(in.Settled, m)
			}
		}
		return nil
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
		q := store.New(tx)
		var err error
		if runID, err = q.InsertRun(ctx, store.InsertRunParams{Period: period, Config: cfgJSON}); err != nil {
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
			ver, err := int32Of(m.Rule.Version)
			if err != nil {
				return err
			}
			n, err := q.InsertMatch(ctx, store.InsertMatchParams{ID: m.ID, RunID: &runID, RuleID: m.Rule.ID,
				RuleVersion: ver, Tray: string(m.Tray), MovementIds: mv, ItemIds: it, Adjustments: adj, Evidence: ev})
			if err != nil {
				return fmt.Errorf("matching: insert match: %w", err)
			}
			inserted += int(n)
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
			if err := q.InsertPending(ctx, store.InsertPendingParams{RunID: runID, MovementID: mid,
				Tray: string(p.Tray), Reason: string(p.Reason), Candidates: cands}); err != nil {
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
// (escalate outside Suggested, other outside Exceptions).
var ErrActionNotInTray = errors.New("matching: this action is not available in the item's tray")

// ErrNoteRequired means the action needs a note saying why, and none came
// (blank counts as none).
var ErrNoteRequired = errors.New("matching: this action requires a note")

// noteMissing reports whether a note says nothing: no letter or number that
// shows (spaces, zero width or bidi characters and blank looking letters
// alone are no reason, F28).
func noteMissing(note string) bool { return !text.HasLetterOrNumber(note) }

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
	role, err := store.New(tx).MemberRole(ctx)
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
	q := store.New(tx)
	tp, err := q.MatchTrayAndPeriods(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, nil, fmt.Errorf("%w: unknown match", ErrInvalid)
	}
	if err != nil {
		return "", nil, nil, err
	}
	rows, err := q.MatchDecisionStates(ctx, id)
	if err != nil {
		return "", nil, nil, err
	}
	states := map[Tray]trayState{}
	for _, r := range rows {
		states[Tray(r.Tray)] = trayState{id: r.ID, revision: int(r.Revision), action: Action(r.Action)}
	}
	return Tray(tp.Tray), states, tp.Periods, nil
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
		// The role first, read from the membership: someone who decides no
		// tray is 403 whatever key it sends, never a replay nor a 422.
		var err error
		if d.Role, err = memberRole(ctx, tx); err != nil {
			return err
		}
		if !CanDecide(d.Role, Suggested) && !CanDecide(d.Role, Exception) {
			return ErrForbidden
		}
		// Then the replay: the same key, match, action and user answers what
		// was stored, before anything else is looked at again.
		q := store.New(tx)
		prev, err := q.DecisionByKey(ctx, key)
		if err == nil {
			if prev.MatchID == d.MatchID && prev.Action == string(d.Action) && prev.UserID == sc.UserID {
				d.At, d.Role, d.Note, d.Tray = prev.At, prev.Role, prev.Note, Tray(prev.Tray)
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
			if !actionFits(d.Action, d.Tray) {
				return ErrActionNotInTray
			}
			// "Other" says what it is; "reject" says why (product spec
			// section 4). Both in either tray, never optional.
			if (d.Action == Other || d.Action == Reject) && noteMissing(d.Note) {
				return ErrNoteRequired
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

		rev, err := int32Of(d.Revision)
		if err != nil {
			return err
		}
		d.At, err = q.InsertDecision(ctx, store.InsertDecisionParams{Key: key, MatchID: d.MatchID, Tray: string(d.Tray),
			Revision: rev, Action: string(d.Action), UserID: sc.UserID, Role: d.Role, Note: d.Note, Snapshot: snap,
			Reverts: reverts})
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "decisions_tenant_id_match_id_tray_revision_key" {
			return ErrAlreadyDecided // another decision landed on the same revision first
		}
		if err != nil {
			return err
		}
		// A client's reject goes back to the operator through the movements
		// (client_rejected); reverting it takes them back, only while the
		// operator has not answered.
		if d.Tray == Exception && d.Action == Reject {
			if err := rejectToOperator(ctx, tx, d, key, sc.UserID); err != nil {
				return err
			}
		}
		if d.Tray == Exception && d.Action == Revert && target.action == Reject {
			if err := takeBackReject(ctx, tx, d, key, sc.UserID); err != nil {
				return err
			}
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

// ErrOperatorAnswered: the client tried to revert a reject the operator
// already answered on one of its movements. The operator's answer is
// reverted first.
var ErrOperatorAnswered = errors.New("matching: the operator already answered this reject")

// rejectToOperator writes one 'rejected' movement decision per movement of
// the match, with the client's reason: the movement is the operator's now,
// with the same turn as after "other". Each row's key derives from the
// decision's, so the rows are as idempotent as the decision.
func rejectToOperator(ctx context.Context, tx pgx.Tx, d Decision, key, user uuid.UUID) error {
	q := store.New(tx)
	for _, mv := range d.Match.MovementIDs {
		mid, err := uuid.Parse(mv)
		if err != nil {
			return ErrInvalid
		}
		cur, err := q.MovementState(ctx, mid)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if cur.Action != "" {
			return ErrAlreadyDecided
		}
		matchID := d.MatchID
		if _, err := q.InsertMovementDecision(ctx, store.InsertMovementDecisionParams{
			Key: uuid.NewSHA1(key, []byte(mv)), MovementID: mid, Revision: cur.Revision + 1, Action: string(MovementRejected),
			MatchID: &matchID, Note: d.Note, UserID: user, Role: d.Role,
		}); err != nil {
			return err
		}
	}
	return nil
}

// takeBackReject reverts the 'rejected' rows of a reject the client is
// reverting. If the operator answered any of them, nothing is reverted.
func takeBackReject(ctx context.Context, tx pgx.Tx, d Decision, key, user uuid.UUID) error {
	q := store.New(tx)
	for _, mv := range d.Match.MovementIDs {
		mid, err := uuid.Parse(mv)
		if err != nil {
			return ErrInvalid
		}
		cur, err := q.MovementState(ctx, mid)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOperatorAnswered
		}
		if err != nil {
			return err
		}
		if cur.Action != string(MovementRejected) || cur.MatchID != d.MatchID {
			return ErrOperatorAnswered
		}
		id := cur.ID
		if _, err := q.InsertMovementDecision(ctx, store.InsertMovementDecisionParams{
			Key: uuid.NewSHA1(key, []byte(mv)), MovementID: mid, Revision: cur.Revision + 1, Action: string(MovementRevert),
			UserID: user, Role: d.Role, Reverts: &id,
		}); err != nil {
			return err
		}
	}
	return nil
}

// ErrNotFound means the match does not exist for the tenant in the context.
var ErrNotFound = errors.New("matching: not found")

// storedMatch builds a Match from its stored columns.
func storedMatch(id, ruleID string, ruleVersion int32, tray string, mv, it []uuid.UUID, adj, ev []byte) (Match, error) {
	m := Match{ID: id, Rule: Rule{ID: ruleID, Version: int(ruleVersion)}, Tray: Tray(tray),
		MovementIDs: strs(mv), ItemIDs: strs(it)}
	return m, unmarshalEvidence(adj, ev, &m)
}

// GetMatch returns a stored match of the tenant in the context.
func (s *PGStore) GetMatch(ctx context.Context, id string) (Match, error) {
	var m Match
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		r, err := store.New(tx).GetMatch(ctx, id)
		if err != nil {
			return err
		}
		m, err = storedMatch(r.ID, r.RuleID, r.RuleVersion, r.Tray, r.MovementIds, r.ItemIds, r.Adjustments, r.Evidence)
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
	r, err := monthFilter(period)
	if err != nil {
		return nil, err
	}
	return s.UndecidedInRange(ctx, tray, r, limit)
}

// monthFilter turns a period (YYYY-MM, "" for none) into a range.
func monthFilter(period string) (*listrange.Range, error) {
	if period == "" {
		return nil, nil
	}
	r, err := listrange.Parse(&period, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return r, nil
}

// UndecidedInRange is Undecided restricted to matches with a movement dated
// in the range; nil means every date.
func (s *PGStore) UndecidedInRange(ctx context.Context, tray Tray, r *listrange.Range, limit int) ([]Match, error) {
	from, last := r.Bounds()
	var out []Match
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		if err := mustSeeTray(ctx, tx, tray); err != nil {
			return err
		}
		// See the query: which matches each tray lists as undecided.
		rows, err := store.New(tx).UndecidedMatches(ctx, store.UndecidedMatchesParams{
			Tray: string(tray), DateFrom: from, DateTo: last, MaxRows: rowLimit(limit),
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			m, err := storedMatch(r.ID, r.RuleID, r.RuleVersion, r.Tray, r.MovementIds, r.ItemIds, r.Adjustments, r.Evidence)
			if err != nil {
				return err
			}
			out = append(out, m)
		}
		return nil
	})
	return out, err
}

// PendingRow is a pending movement as stored by a run.
type PendingRow struct {
	RunID uuid.UUID
	// Note is the client's note for client_other and client_rejected, the
	// operator's for operator_returned.
	Note string
	// MatchID is the match the client rejected, for client_rejected.
	MatchID string
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

// ReasonClientRejected: the client rejected the match proposed for the
// movement; it is back with the operator, with the client's reason.
const ReasonClientRejected PendingReason = "client_rejected"

// LatestPending lists the movements waiting in a tray according to the
// latest run of each period.
func (s *PGStore) LatestPending(ctx context.Context, tray Tray, limit int) ([]PendingRow, error) {
	return s.LatestPendingIn(ctx, tray, "", limit)
}

// LatestPendingIn is LatestPending for the latest run of one period
// (YYYY-MM); "" means the latest run of every period.
func (s *PGStore) LatestPendingIn(ctx context.Context, tray Tray, period string, limit int) ([]PendingRow, error) {
	r, err := monthFilter(period)
	if err != nil {
		return nil, err
	}
	return s.LatestPendingInRange(ctx, tray, r, limit)
}

// LatestPendingInRange is LatestPending for the movements dated in the
// range (each is in the latest run of its own month); nil means every date.
func (s *PGStore) LatestPendingInRange(ctx context.Context, tray Tray, r *listrange.Range, limit int) ([]PendingRow, error) {
	from, last := r.Bounds()
	var out []PendingRow
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		if err := mustSeeTray(ctx, tx, tray); err != nil {
			return err
		}
		// What each tray holds, decisions included: matching.waiting, the
		// same definition the results panel counts.
		rows, err := store.New(tx).Waiting(ctx, store.WaitingParams{
			Tray: string(tray), DateFrom: from, DateTo: last, MaxRows: rowLimit(limit),
		})
		if err != nil {
			return err
		}
		for _, w := range rows {
			r := PendingRow{RunID: w.RunID, Note: w.Note, MatchID: w.MatchID, Date: w.Date, Amount: money.Amount(w.Amount),
				Description: w.Description, AccountID: w.AccountID, AccountName: w.AccountName, Bank: w.Bank,
				Pending: Pending{MovementID: w.MovementID.String(), Tray: Tray(w.Tray), Reason: PendingReason(w.Reason)}}
			if err := json.Unmarshal(w.Candidates, &r.Candidates); err != nil {
				return err
			}
			out = append(out, r)
		}
		return nil
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

// strs turns stored ids into the engine's strings (nil when there are none).
func strs(ids []uuid.UUID) []string {
	var out []string
	for _, u := range ids {
		out = append(out, u.String())
	}
	return out
}

// int32Of refuses a value the int columns cannot hold.
func int32Of(n int) (int32, error) {
	if n < math.MinInt32 || n > math.MaxInt32 {
		return 0, fmt.Errorf("matching: %d out of range", n)
	}
	return int32(n), nil
}

// rowLimit clamps a list limit to what the query's int parameter holds.
func rowLimit(limit int) int32 {
	return int32(min(max(limit, 0), math.MaxInt32)) // #nosec G115 -- clamped
}

// maxRows asks for one row past limit, to tell whether there were more.
func maxRows(limit int) int32 {
	return int32(min(max(limit, 0), math.MaxInt32-1) + 1) // #nosec G115 -- clamped
}
