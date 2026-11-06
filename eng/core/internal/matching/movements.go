package matching

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/matching/store"
	"github.com/Tramo-Labs/hq/eng/core/internal/money"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/audit"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
)

// RuleManual is the rule of a link the client made by hand.
var RuleManual = Rule{"manual_link", 1}

// Manual is the tray of a match the client linked by hand.
const Manual Tray = "manual"

// MovementAction is what the client can do with a movement that has no
// candidate (product spec section 4, "Sin propuesta: contanos qué es").
type MovementAction string

const (
	// MovementOther: it is something else; note required, back to the operator.
	MovementOther MovementAction = "other"
	// MovementLink: linked by hand to open items that add up exactly.
	MovementLink MovementAction = "link"
	// MovementRevert undoes the decision in effect, as a new recorded row.
	MovementRevert MovementAction = "revert"
	// MovementResolve: the operator closes a movement the client marked
	// other, saying what was done with it (note required).
	MovementResolve MovementAction = "resolve"
	// MovementReturn: the operator sends it back to the client with what
	// the operator needs (note required). It shows again in Exceptions.
	MovementReturn MovementAction = "return"
	// MovementRejected is written by a client's reject of a match in
	// Exceptions, one row per movement of the match, never by this
	// endpoint: the movement is with the operator like after "other", and
	// the client reverts it on the match.
	MovementRejected MovementAction = "rejected"
)

var (
	// ErrNotInExceptions: the movement is not waiting in the Exceptions tray
	// without a candidate in the latest run of its month.
	ErrNotInExceptions = errors.New("matching: the movement is not in the Exceptions tray without a candidate")
	// ErrLinkDoesNotAdd: the linked items do not add up exactly to the
	// movement. Any difference is an adjustment, and adjustments are the
	// operator's (Suggested).
	ErrLinkDoesNotAdd = errors.New("matching: the items do not add up exactly to the movement")
	// ErrItemTaken: an item is already settled by a match in effect.
	ErrItemTaken = errors.New("matching: an item is already settled by another match")
	// ErrNotWithOperator: the operator acts only on a movement the client
	// marked other, or whose match the client rejected, and nobody
	// answered yet.
	ErrNotWithOperator = errors.New("matching: the movement is not waiting for the operator")
	// ErrUnknownMovement: no movement with that id for the tenant.
	ErrUnknownMovement = fmt.Errorf("%w: unknown movement", ErrInvalid)
	// ErrUnknownItem: an item to link does not exist for the tenant.
	ErrUnknownItem = fmt.Errorf("%w: unknown item", ErrInvalid)
	// ErrRevertOnMatch: a client's reject is reverted on the match it
	// rejected, not on each movement.
	ErrRevertOnMatch = errors.New("matching: revert the reject on the match")
)

// MovementDecisionInput is what the client may say. Who decides and with
// which role are never input.
type MovementDecisionInput struct {
	Key        string // idempotency key
	MovementID string
	Action     MovementAction
	Note       string
	ItemIDs    []string // for MovementLink
}

// MovementDecision is a stored decision on a movement.
type MovementDecision struct {
	MovementID string
	Action     MovementAction
	MatchID    string // the manual match, for MovementLink
	Note       string
	By, Role   string
	Revision   int
	At         time.Time
}

// DecideMovement records a decision on a movement of the Exceptions tray
// that has no candidate, or reverts the one in effect. Role read from
// memberships here. The client (owner or approver) says other, link, or
// reverts its own decision; after the client's other, the operator
// resolves, links or returns it, or reverts its own answer, which puts the
// client's other back in effect. A link creates a manual match (rule
// manual_link@v1) that is settled for the next runs while it is in effect.
func (s *PGStore) DecideMovement(ctx context.Context, in MovementDecisionInput) (MovementDecision, bool, error) {
	sc, ok := db.ScopeFrom(ctx)
	if !ok {
		return MovementDecision{}, false, db.ErrNoScope
	}
	if sc.UserID == uuid.Nil {
		return MovementDecision{}, false, ErrNoUser
	}
	key, err := uuid.Parse(in.Key)
	if err != nil {
		return MovementDecision{}, false, ErrInvalid
	}
	mid, err := uuid.Parse(in.MovementID)
	if err != nil {
		return MovementDecision{}, false, ErrInvalid
	}
	switch in.Action {
	case MovementOther, MovementResolve, MovementReturn:
		if noteMissing(in.Note) {
			return MovementDecision{}, false, ErrNoteRequired
		}
	case MovementLink:
		if len(in.ItemIDs) == 0 {
			return MovementDecision{}, false, ErrInvalid
		}
	case MovementRevert:
	default:
		return MovementDecision{}, false, ErrInvalid
	}
	items, err := uuids(in.ItemIDs)
	if err != nil {
		return MovementDecision{}, false, ErrInvalid
	}
	d := MovementDecision{MovementID: mid.String(), Action: in.Action, Note: in.Note, By: sc.UserID.String()}
	applied := false
	err = s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		// The role first (see Record): someone who decides on no movement
		// is 403 whatever key it sends.
		var err error
		if d.Role, err = memberRole(ctx, tx); err != nil {
			return err
		}
		client, operator := CanDecide(d.Role, Exception), d.Role == identity.RoleOperator
		if !client && !operator {
			return ErrForbidden
		}
		prev, err := q.MovementDecisionByKey(ctx, key)
		if err == nil {
			if prev.MovementID == mid && prev.Action == string(in.Action) && prev.UserID == sc.UserID {
				d.Role, d.Note, d.Revision, d.At = prev.Role, prev.Note, int(prev.Revision), prev.At
				if prev.MatchID != nil {
					d.MatchID = *prev.MatchID
				}
				return nil
			}
			return ErrKeyReused
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		mv, err := q.MovementAmountDate(ctx, mid)
		amount, date := mv.Amount, mv.Date
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnknownMovement
		}
		if err != nil {
			return err
		}
		cur, err := q.MovementState(ctx, mid)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := movementTurn(in.Action, client, MovementAction(cur.Action), cur.Role); err != nil {
			return err
		}

		var reverts *int64
		var matchID *string
		switch in.Action {
		case MovementRevert:
			isClosed, err := s.closed(ctx, tx, []string{period(date)})
			if err != nil {
				return err
			}
			if isClosed {
				return ErrClosed
			}
			reverts = &cur.ID
		default:
			// The first decision on a movement needs it waiting in the
			// client's Exceptions without a candidate. After that the turn
			// is the decision in effect (movementTurn): a rejected match's
			// movement, or one the operator returned, need not be pending.
			waiting := cur.Action != ""
			if !waiting {
				if waiting, err = q.WaitsWithNoCandidate(ctx, mid); err != nil {
					return err
				}
			}
			if !waiting {
				return ErrNotInExceptions
			}
			if in.Action == MovementLink {
				id, err := linkByHand(ctx, tx, mid, money.Amount(amount), date, items)
				if err != nil {
					return err
				}
				matchID, d.MatchID = &id, id
			}
		}
		d.Revision = int(cur.Revision) + 1
		if s.beforeInsert != nil {
			s.beforeInsert()
		}

		rev, err := int32Of(d.Revision)
		if err != nil {
			return err
		}
		d.At, err = q.InsertMovementDecision(ctx, store.InsertMovementDecisionParams{
			Key: key, MovementID: mid, Revision: rev, Action: string(d.Action), MatchID: matchID,
			Note: d.Note, UserID: sc.UserID, Role: d.Role, Reverts: reverts,
		})
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "movement_decisions_tenant_id_movement_id_revision_key" {
			return ErrAlreadyDecided
		}
		if err != nil {
			return err
		}
		if in.Action == MovementRevert && cur.Action == string(MovementLink) {
			linked, err := q.MovementDecisionMatch(ctx, cur.ID)
			if err != nil {
				return err
			}
			if linked != nil {
				d.MatchID = *linked // the manual match that stops being in effect
			}
		}
		applied = true
		detail := map[string]any{"role": d.Role, "revision": d.Revision}
		if d.MatchID != "" {
			detail["match"] = d.MatchID
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "matching.movement_" + string(d.Action),
			Subject: "ledger.movements:" + mid.String(), Detail: detail})
	})
	if err != nil {
		return MovementDecision{}, false, err
	}
	return d, applied, nil
}

// movementTurn says whether the caller's side may take the action given the
// decision in effect (action and the role that took it). The client acts
// on a movement nobody decided or that the operator returned; the operator
// acts on one the client marked other. Each side reverts only its own row.
func movementTurn(a MovementAction, client bool, cur MovementAction, curRole string) error {
	curByClient := curRole == identity.RoleOwner || curRole == identity.RoleApprover
	if a == MovementRevert {
		switch {
		case cur == "":
			return ErrNothingToRevert
		case client != curByClient:
			return ErrForbidden
		case cur == MovementRejected:
			return ErrRevertOnMatch
		}
		return nil
	}
	if client {
		switch a {
		case MovementOther, MovementLink:
		default:
			return ErrForbidden
		}
		if cur != "" && cur != MovementReturn {
			return ErrAlreadyDecided
		}
		return nil
	}
	switch a {
	case MovementResolve, MovementReturn, MovementLink:
	default:
		return ErrForbidden
	}
	if cur != MovementOther && cur != MovementRejected {
		return ErrNotWithOperator
	}
	return nil
}

// OpenItem is an item no match in effect settles.
type OpenItem struct {
	ID         uuid.UUID
	Source     string
	SourceID   string
	Kind       string
	Date       time.Time
	Due        *time.Time
	Amount     money.Amount
	Refs       []string
	Party      string
	Confidence float64
	InvoiceID  *uuid.UUID
}

// OpenItems lists the items no match in effect settles, oldest first and
// then by source id, so the list always comes in the same order: what
// "link by hand" offers. period (YYYY-MM) keeps the items dated in that
// month, "" all; direction "in" keeps positive amounts, "out" negative, ""
// both. Up to limit rows; the bool says there were more.
func (s *PGStore) OpenItems(ctx context.Context, period, direction string, limit int) ([]OpenItem, bool, error) {
	var out []OpenItem
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		rows, err := store.New(tx).OpenItems(ctx, store.OpenItemsParams{
			Period: period, Direction: direction, MaxRows: maxRows(limit),
		})
		for _, r := range rows {
			out = append(out, OpenItem{ID: r.ID, Source: r.Source, SourceID: r.SourceID, Kind: r.Kind, Date: r.Date,
				Due: r.Due, Amount: money.Amount(r.Amount), Refs: r.Refs, Party: r.Party,
				Confidence: math.Round(float64(r.Confidence)*1e4) / 1e4, // real column: no float32 noise
				InvoiceID:  r.InvoiceID})
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

// linkByHand checks the items and creates (or finds) the manual match.
func linkByHand(ctx context.Context, tx pgx.Tx, mid uuid.UUID, amount money.Amount, date time.Time, items []uuid.UUID) (string, error) {
	slices.SortFunc(items, func(a, b uuid.UUID) int { return compareUUID(a, b) })
	items = slices.Compact(items)
	q := store.New(tx)
	cs, err := q.ItemsCountAndSum(ctx, items)
	if err != nil {
		return "", err
	}
	found, sum := cs.Count, cs.Total
	if found != int64(len(items)) {
		return "", ErrUnknownItem
	}
	if money.Amount(sum) != amount {
		return "", ErrLinkDoesNotAdd
	}
	taken, err := q.AnyItemTaken(ctx, items)
	if err != nil {
		return "", err
	}
	if taken {
		return "", ErrItemTaken
	}
	ids := make([]string, len(items))
	for i, u := range items {
		ids[i] = u.String()
	}
	id := matchID(RuleManual, []string{mid.String()}, ids)
	ev, err := json.Marshal(map[string]any{"movement_periods": []string{period(date)}, "linked_by_hand": true})
	if err != nil {
		return "", err
	}
	ver, err := int32Of(RuleManual.Version)
	if err != nil {
		return "", err
	}
	return id, q.InsertManualMatch(ctx, store.InsertManualMatchParams{ID: id, RuleID: RuleManual.ID, RuleVersion: ver,
		MovementIds: []uuid.UUID{mid}, ItemIds: items, Evidence: ev})
}

func compareUUID(a, b uuid.UUID) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}
