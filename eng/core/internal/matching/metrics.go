package matching

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/matching/store"
)

// Metric is one number of the results panel for a tenant and a month.
// Value is nil when there is nothing to measure: that is "no data", never
// zero. Source says what is counted over which rows; List is the API path
// that returns those rows.
//
// These numbers describe this tenant's own stored data. They are not an
// automation rate of the product and are not used on the landing page or in
// sales until there are real statements labeled by a person.
type Metric struct {
	Key    string
	Label  string
	Unit   string // percent, count, days
	Period string
	Value  *float64
	Source string
	List   string
}

func measured(v float64) *float64 { return &v }

func percent(part, whole int) *float64 {
	return measured(math.Round(float64(part)*1000/float64(whole)) / 10)
}

// monthRange returns the first and last day of a YYYY-MM period.
func monthRange(period string) (time.Time, time.Time, error) {
	from, err := time.Parse("2006-01", period)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: period must be YYYY-MM", ErrInvalid)
	}
	return from, from.AddDate(0, 1, -1), nil
}

// span is the dates a panel covers, both ends included. A month is named
// by its period (YYYY-MM) and its lists filter by it; any other span (the
// fortnight of a close) by its dates.
type span struct {
	From, Last time.Time
	Period     string // YYYY-MM when the span is a whole month, else empty
}

func monthSpan(period string) (span, error) {
	from, last, err := monthRange(period)
	if err != nil {
		return span{}, err
	}
	return span{From: from, Last: last, Period: period}, nil
}

// query is the span's filter in a list path: the period of a month, else
// from and to, both included, as the API takes them.
func (sp span) query() string {
	if sp.Period != "" {
		return "period=" + sp.Period
	}
	return "from=" + sp.From.Format("2006-01-02") + "&to=" + sp.Last.Format("2006-01-02")
}

// of and in name the span in a source text.
func (sp span) of() string {
	if sp.Period != "" {
		return "del mes"
	}
	return "del período"
}

func (sp span) in() string {
	if sp.Period != "" {
		return "en el mes"
	}
	return "en el período"
}

// pendingReasons are the reasons counted one by one in the Suggested tray.
var pendingReasons = []PendingReason{ReasonAmbiguous, ReasonSharedCandidate, ReasonComboAmbiguous, ReasonRefAmountDiffers,
	ReasonClientOther, ReasonClientRejected}

// Metrics computes the results panel of the tenant in the context for one
// month, in one tenant transaction. now dates the age of open exceptions.
func (s *PGStore) Metrics(ctx context.Context, period string, now time.Time) ([]Metric, error) {
	sp, err := monthSpan(period)
	if err != nil {
		return nil, err
	}
	var out []Metric
	err = s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		// Metrics about the Suggested tray are the operator's only.
		operator, err := seesSuggested(ctx, tx)
		if err != nil {
			return err
		}
		out, err = panel(ctx, tx, sp, now, operator)
		return err
	})
	return out, err
}

// panel computes the results panel over a span in the caller's tenant
// transaction, without opening another: a close counts and freezes the
// numbers in its own.
// operator says whether the Suggested metrics are included: they are the
// operator's only (spec section 1).
func panel(ctx context.Context, tx pgx.Tx, sp span, now time.Time, operator bool) ([]Metric, error) {
	from, to, period := sp.From, sp.Last, sp.Period
	of, in, filter := sp.of(), sp.in(), sp.query()
	exportList := "/v1/exports/matches?from=" + from.Format("2006-01-02") + "&to=" + to.Format("2006-01-02")
	var out []Metric
	err := func() error {
		var movements, autoMoves, approved, rejected, openMoves, openMatches, invoices, held int
		var invoicesEver bool
		var hasRun bool
		var oldest *time.Time
		pending := map[PendingReason]int{}

		q := store.New(tx)
		mc, err := q.MonthCounts(ctx, store.MonthCountsParams{DateFrom: from, DateTo: to})
		if err != nil {
			return err
		}
		movements, hasRun, autoMoves = int(mc.Movements), mc.HasRun, int(mc.AutoMovements)

		// Suggested matches of the month decided by the operator, by the
		// decision in effect (a reverted one does not count).
		sd, err := q.SuggestedDecided(ctx, store.SuggestedDecidedParams{DateFrom: from, DateTo: to})
		if err != nil {
			return err
		}
		approved, rejected = int(sd.Approved), int(sd.Rejected)

		// Open exceptions: the movements the client's Exceptions list shows
		// for the month (matching.waiting, the same definition), and
		// escalated matches of the month the client has not decided.
		oe, err := q.OpenExceptionMovements(ctx, store.OpenExceptionMovementsParams{DateFrom: from, DateTo: to})
		if err != nil {
			return err
		}
		openMoves = int(oe.Count)
		if oe.Oldest != "" {
			d, err := time.Parse("2006-01-02", oe.Oldest)
			if err != nil {
				return err
			}
			oldest = &d
		}
		om, err := q.OpenEscalatedMatches(ctx, store.OpenEscalatedMatchesParams{DateFrom: from, DateTo: to})
		if err != nil {
			return err
		}
		openMatches = int(om)

		// Pending by reason in Suggested: what the operator's list shows.
		byReason, err := q.SuggestedPendingByReason(ctx, store.SuggestedPendingByReasonParams{DateFrom: from, DateTo: to})
		if err != nil {
			return err
		}
		for _, r := range byReason {
			pending[PendingReason(r.Reason)] = int(r.Count)
		}

		// Invoices stored in the month (Argentina time), and how many need review.
		ic, err := q.InvoiceCounts(ctx, store.InvoiceCountsParams{DateFrom: from, DateTo: to})
		if err != nil {
			return err
		}
		invoicesEver, invoices, held = ic.Ever, int(ic.Month), int(ic.Held)

		noRun := "El mes todavía no se corrió"
		if period == "" {
			noRun = "El período todavía no se corrió"
		}
		m := func(key, label, unit, list string, v *float64, source string) {
			out = append(out, Metric{Key: key, Label: label, Unit: unit, Period: period, Value: v, Source: source, List: list})
		}

		switch {
		case movements == 0:
			m("automation_rate", "Conciliación automática", "percent", "/v1/matching/matches?tray=auto&"+filter, nil,
				"Sin movimientos cargados "+in)
		case !hasRun:
			m("automation_rate", "Conciliación automática", "percent", "/v1/matching/matches?tray=auto&"+filter, nil, noRun)
		default:
			m("automation_rate", "Conciliación automática", "percent", "/v1/matching/matches?tray=auto&"+filter,
				percent(autoMoves, movements),
				fmt.Sprintf("Movimientos %s registrados por una regla automática (%d) sobre todos los movimientos %s (%d)",
					of, autoMoves, of, movements))
		}

		suggestedOnly := m
		m = func(key, label, unit, list string, v *float64, source string) {
			if !operator && (key == "suggested_acceptance_rate" || strings.HasPrefix(key, "pending_")) {
				return
			}
			suggestedOnly(key, label, unit, list, v, source)
		}

		if approved+rejected == 0 {
			m("suggested_acceptance_rate", "Sugerencias aceptadas", "percent", exportList, nil,
				"Ninguna sugerencia "+of+" tiene todavía una decisión del operador")
		} else {
			m("suggested_acceptance_rate", "Sugerencias aceptadas", "percent", exportList, percent(approved, approved+rejected),
				fmt.Sprintf("Sugerencias %s aprobadas (%d) sobre las decididas (%d), según la decisión vigente",
					of, approved, approved+rejected))
		}

		if !hasRun {
			m("open_exception_movements", "Movimientos sin explicar", "count", "/v1/matching/pending?tray=exception&"+filter, nil, noRun)
			m("open_exception_matches", "Sugerencias esperando al cliente", "count", "/v1/matching/matches?tray=exception&"+filter, nil, noRun)
			m("oldest_open_exception_days", "Antigüedad de la excepción más vieja", "days", "/v1/matching/pending?tray=exception&"+filter, nil, noRun)
			for _, r := range pendingReasons {
				m("pending_"+string(r), "Pendientes: "+reasonLabel[r], "count", "/v1/matching/pending?tray=suggested&"+filter, nil, noRun)
			}
		} else {
			m("open_exception_movements", "Movimientos sin explicar", "count", "/v1/matching/pending?tray=exception&"+filter,
				measured(float64(openMoves)), "Movimientos "+of+" en Excepciones sin candidato, sin decisión del cliente o devueltos por el operador, según la última corrida")
			m("open_exception_matches", "Sugerencias esperando al cliente", "count", "/v1/matching/matches?tray=exception&"+filter,
				measured(float64(openMatches)), "Sugerencias "+of+" que el operador mandó a Excepciones y el cliente no decidió")
			if oldest == nil {
				m("oldest_open_exception_days", "Antigüedad de la excepción más vieja", "days", "/v1/matching/pending?tray=exception&"+filter,
					nil, "No hay movimientos "+of+" esperando en Excepciones")
			} else {
				days := math.Floor(civilDay(now).Sub(*oldest).Hours() / 24)
				m("oldest_open_exception_days", "Antigüedad de la excepción más vieja", "days", "/v1/matching/pending?tray=exception&"+filter,
					measured(days), "Días desde la fecha del movimiento más viejo que espera en Excepciones")
			}
			for _, r := range pendingReasons {
				m("pending_"+string(r), "Pendientes: "+reasonLabel[r], "count", "/v1/matching/pending?tray=suggested&"+filter,
					measured(float64(pending[r])), "Movimientos "+of+" en Sugerido por este motivo, según la última corrida")
			}
		}

		invList := "/v1/documents/invoices?" + filter
		if !invoicesEver {
			m("invoices_processed", "Facturas procesadas", "count", invList, nil, "Todavía no se cargó ninguna factura")
			m("invoices_held", "Facturas para revisar", "count", invList+"&review=true", nil, "Todavía no se cargó ninguna factura")
		} else {
			m("invoices_processed", "Facturas procesadas", "count", invList, measured(float64(invoices)), "Facturas cargadas "+in)
			m("invoices_held", "Facturas para revisar", "count", invList+"&review=true", measured(float64(held)),
				"Facturas "+of+" con algún dato obligatorio faltante, dudoso o en conflicto, o que no generaron partida")
		}
		return nil
	}()
	return out, err
}

var reasonLabel = map[PendingReason]string{
	ReasonAmbiguous:        "dos candidatos iguales",
	ReasonSharedCandidate:  "el candidato también le sirve a otro movimiento",
	ReasonComboAmbiguous:   "más de una combinación posible",
	ReasonRefAmountDiffers: "la referencia no cierra con el importe",
	ReasonClientOther:      "el cliente dice que es otra cosa",
	ReasonClientRejected:   "el cliente rechazó la propuesta",
}

func civilDay(t time.Time) time.Time {
	t = t.In(time.FixedZone("ART", -3*3600))
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// MatchRows is the matches export: one row per match with a movement dated
// in [from, to], with rule and version, tray, links, adjustments and the
// decision in effect (the client's in Exceptions, else the operator's in
// Suggested, or the client's link for a manual match). The first row is
// the header. Cells are plain text; the caller writes them formula safe.
func (s *PGStore) MatchRows(ctx context.Context, from, to time.Time) ([][]string, error) {
	out := [][]string{{"match_id", "rule", "rule_version", "tray", "movement_ids", "item_ids", "adjustments",
		"reference", "date_diff_days", "cross_period", "low_confidence",
		"decision_tray", "decision", "decided_by", "decided_role", "decided_at", "decision_revision", "after_close"}}
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		// Anyone but an operator sees results, not the Suggested tray:
		// automatic matches, links by hand, approved matches and what an
		// operator sent to Exceptions. A suggestion still undecided, or one
		// the operator rejected, is the operator's (spec section 1).
		operator, err := seesSuggested(ctx, tx)
		if err != nil {
			return err
		}
		rows, err := store.New(tx).MatchExportRows(ctx, store.MatchExportRowsParams{DateFrom: from, DateTo: to, Operator: operator})
		if err != nil {
			return err
		}
		for _, r := range rows {
			m := Match{}
			if err := unmarshalEvidence(r.Adjustments, r.Evidence, &m); err != nil {
				return err
			}
			var adj []string
			for _, a := range m.Adjustments {
				adj = append(adj, a.Kind+":"+a.Amount.String())
			}
			// The decision in effect, else the client's link.
			at, rev := r.DecisionAt, r.DecisionRevision
			if at == nil {
				at, rev = r.LinkAt, r.LinkRevision
			}
			atS, revS := "", ""
			if at != nil {
				atS = at.UTC().Format(time.RFC3339)
			}
			if rev != nil {
				revS = strconv.Itoa(int(*rev))
			}
			out = append(out, []string{r.ID, r.RuleID, strconv.Itoa(int(r.RuleVersion)), r.Tray, strings.Join(r.MovementIds, " "),
				strings.Join(r.ItemIds, " "), strings.Join(adj, " "), m.Reference, strconv.Itoa(m.DateDiffDays),
				strconv.FormatBool(m.CrossPeriod), strconv.FormatBool(m.LowConfidence), r.DecisionTray, r.Decision,
				r.DecidedBy, r.DecidedRole, atS, revS, strconv.FormatBool(m.AfterClose)})
		}
		return nil
	})
	return out, err
}

// unmarshalEvidence fills the adjustments and the evidence fields of m from
// their stored JSON.
func unmarshalEvidence(adj, ev []byte, m *Match) error {
	if err := json.Unmarshal(adj, &m.Adjustments); err != nil {
		return err
	}
	var e struct {
		Reference     string   `json:"reference"`
		DateDiffDays  int      `json:"date_diff_days"`
		CrossPeriod   bool     `json:"cross_period"`
		MovePeriods   []string `json:"movement_periods"`
		ItemPeriods   []string `json:"item_periods"`
		LowConfidence bool     `json:"low_confidence"`
		AfterClose    bool     `json:"after_close"`
	}
	if err := json.Unmarshal(ev, &e); err != nil {
		return err
	}
	m.Reference, m.DateDiffDays, m.CrossPeriod = e.Reference, e.DateDiffDays, e.CrossPeriod
	m.MovePeriods, m.ItemPeriods, m.LowConfidence = e.MovePeriods, e.ItemPeriods, e.LowConfidence
	m.AfterClose = e.AfterClose
	return nil
}
