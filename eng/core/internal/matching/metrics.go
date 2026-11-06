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

// pendingReasons are the reasons counted one by one in the Suggested tray.
var pendingReasons = []PendingReason{ReasonAmbiguous, ReasonSharedCandidate, ReasonComboAmbiguous, ReasonRefAmountDiffers, ReasonClientOther}

// Metrics computes the results panel of the tenant in the context for one
// month, in one tenant transaction. now dates the age of open exceptions.
func (s *PGStore) Metrics(ctx context.Context, period string, now time.Time) ([]Metric, error) {
	from, to, err := monthRange(period)
	if err != nil {
		return nil, err
	}
	exportList := "/v1/exports/matches?from=" + from.Format("2006-01-02") + "&to=" + to.Format("2006-01-02")
	var out []Metric
	err = s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		var movements, autoMoves, approved, rejected, openMoves, openMatches, invoices, held int
		var invoicesEver bool
		var hasRun bool
		// Metrics about the Suggested tray are the operator's only.
		operator, err := seesSuggested(ctx, tx)
		if err != nil {
			return err
		}
		var oldest *time.Time
		pending := map[PendingReason]int{}

		if err := tx.QueryRow(ctx, `
			select (select count(*) from ledger.movements where date between $1 and $2),
			       exists (select 1 from matching.runs where period = $3),
			       (select count(distinct l.id) from matching.matches m
			          join ledger.movements l on l.id = any(m.movement_ids)
			         where m.tray = 'auto' and l.date between $1 and $2)`,
			from, to, period).Scan(&movements, &hasRun, &autoMoves); err != nil {
			return err
		}

		// Suggested matches of the month decided by the operator, by the
		// decision in effect (a reverted one does not count).
		if err := tx.QueryRow(ctx, `
			select count(*) filter (where d.action = 'approve'), count(*) filter (where d.action = 'reject')
			  from matching.matches m
			  join matching.decision_state d on d.tenant_id = m.tenant_id and d.match_id = m.id and d.tray = 'suggested'
			 where m.tray = 'suggested'
			   and exists (select 1 from ledger.movements l where l.id = any(m.movement_ids) and l.date between $1 and $2)`,
			from, to).Scan(&approved, &rejected); err != nil {
			return err
		}

		// Open exceptions: movements the latest run of the month left in
		// Exceptions with no client decision in effect, and escalated
		// matches of the month the client has not decided.
		if err := tx.QueryRow(ctx, `
			with latest as (select id from matching.runs where period = $1 order by started_at desc, id limit 1),
			     open_moves as (
			       select p.movement_id from matching.pending p
			        where p.run_id in (select id from latest) and p.tray = 'exception'
			          and not exists (select 1 from matching.movement_decision_state s
			                           where s.tenant_id = p.tenant_id and s.movement_id = p.movement_id
			                            and s.action is not null and s.action <> 'return'))
			select (select count(*) from open_moves),
			       (select min(l.date) from ledger.movements l where l.id in (select movement_id from open_moves))`,
			period).Scan(&openMoves, &oldest); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			select count(*) from matching.matches m
			 where exists (select 1 from matching.decision_state d where d.tenant_id = m.tenant_id and d.match_id = m.id
			                and d.tray = 'suggested' and d.action = 'escalate')
			   and not exists (select 1 from matching.decision_state d where d.tenant_id = m.tenant_id and d.match_id = m.id
			                and d.tray = 'exception' and d.action is not null)
			   and exists (select 1 from ledger.movements l where l.id = any(m.movement_ids) and l.date between $1 and $2)`,
			from, to).Scan(&openMatches); err != nil {
			return err
		}

		// Pending by reason in Suggested, from the latest run of the month.
		rows, err := tx.Query(ctx, `
			with latest as (select id from matching.runs where period = $1 order by started_at desc, id limit 1)
			select case when p.tray = 'exception' and s.action = 'other' then 'client_other' else p.reason end, count(*)
			  from matching.pending p
			  left join matching.movement_decision_state s on s.tenant_id = p.tenant_id and s.movement_id = p.movement_id
			 where p.run_id in (select id from latest)
			   and (p.tray = 'suggested' or (p.tray = 'exception' and s.action = 'other'))
			 group by 1`, period)
		if err != nil {
			return err
		}
		for rows.Next() {
			var reason string
			var n int
			if err := rows.Scan(&reason, &n); err != nil {
				rows.Close()
				return err
			}
			pending[PendingReason(reason)] = n
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		// Invoices stored in the month (Argentina time), and how many need review.
		if err := tx.QueryRow(ctx, `
			select exists (select 1 from documents.invoices),
			       count(*) filter (where to_char(created_at at time zone 'America/Argentina/Buenos_Aires', 'YYYY-MM') = $1),
			       count(*) filter (where to_char(created_at at time zone 'America/Argentina/Buenos_Aires', 'YYYY-MM') = $1
			                          and (not auto_eligible or item_reason is not null))
			  from documents.invoices`, period).Scan(&invoicesEver, &invoices, &held); err != nil {
			return err
		}

		noRun := "El mes todavía no se corrió"
		m := func(key, label, unit, list string, v *float64, source string) {
			out = append(out, Metric{Key: key, Label: label, Unit: unit, Period: period, Value: v, Source: source, List: list})
		}

		switch {
		case movements == 0:
			m("automation_rate", "Conciliación automática", "percent", "/v1/matching/matches?tray=auto&period="+period, nil,
				"Sin movimientos cargados en el mes")
		case !hasRun:
			m("automation_rate", "Conciliación automática", "percent", "/v1/matching/matches?tray=auto&period="+period, nil, noRun)
		default:
			m("automation_rate", "Conciliación automática", "percent", "/v1/matching/matches?tray=auto&period="+period,
				percent(autoMoves, movements),
				fmt.Sprintf("Movimientos del mes registrados por una regla automática (%d) sobre todos los movimientos del mes (%d)",
					autoMoves, movements))
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
				"Ninguna sugerencia del mes tiene todavía una decisión del operador")
		} else {
			m("suggested_acceptance_rate", "Sugerencias aceptadas", "percent", exportList, percent(approved, approved+rejected),
				fmt.Sprintf("Sugerencias del mes aprobadas (%d) sobre las decididas (%d), según la decisión vigente",
					approved, approved+rejected))
		}

		if !hasRun {
			m("open_exception_movements", "Movimientos sin explicar", "count", "/v1/matching/pending?tray=exception&period="+period, nil, noRun)
			m("open_exception_matches", "Sugerencias esperando al cliente", "count", "/v1/matching/matches?tray=exception&period="+period, nil, noRun)
			m("oldest_open_exception_days", "Antigüedad de la excepción más vieja", "days", "/v1/matching/pending?tray=exception&period="+period, nil, noRun)
			for _, r := range pendingReasons {
				m("pending_"+string(r), "Pendientes: "+reasonLabel[r], "count", "/v1/matching/pending?tray=suggested&period="+period, nil, noRun)
			}
		} else {
			m("open_exception_movements", "Movimientos sin explicar", "count", "/v1/matching/pending?tray=exception&period="+period,
				measured(float64(openMoves)), "Movimientos del mes en Excepciones sin candidato, sin decisión del cliente o devueltos por el operador, según la última corrida")
			m("open_exception_matches", "Sugerencias esperando al cliente", "count", "/v1/matching/matches?tray=exception&period="+period,
				measured(float64(openMatches)), "Sugerencias del mes que el operador mandó a Excepciones y el cliente no decidió")
			if oldest == nil {
				m("oldest_open_exception_days", "Antigüedad de la excepción más vieja", "days", "/v1/matching/pending?tray=exception&period="+period,
					nil, "No hay movimientos del mes esperando en Excepciones")
			} else {
				days := math.Floor(civilDay(now).Sub(*oldest).Hours() / 24)
				m("oldest_open_exception_days", "Antigüedad de la excepción más vieja", "days", "/v1/matching/pending?tray=exception&period="+period,
					measured(days), "Días desde la fecha del movimiento más viejo que espera en Excepciones")
			}
			for _, r := range pendingReasons {
				m("pending_"+string(r), "Pendientes: "+reasonLabel[r], "count", "/v1/matching/pending?tray=suggested&period="+period,
					measured(float64(pending[r])), "Movimientos del mes en Sugerido por este motivo, según la última corrida")
			}
		}

		invList := "/v1/documents/invoices?period=" + period
		if !invoicesEver {
			m("invoices_processed", "Facturas procesadas", "count", invList, nil, "Todavía no se cargó ninguna factura")
			m("invoices_held", "Facturas para revisar", "count", invList+"&review=true", nil, "Todavía no se cargó ninguna factura")
		} else {
			m("invoices_processed", "Facturas procesadas", "count", invList, measured(float64(invoices)), "Facturas cargadas en el mes")
			m("invoices_held", "Facturas para revisar", "count", invList+"&review=true", measured(float64(held)),
				"Facturas del mes con algún dato obligatorio faltante, dudoso o en conflicto, o que no generaron partida")
		}
		return nil
	})
	return out, err
}

var reasonLabel = map[PendingReason]string{
	ReasonAmbiguous:        "dos candidatos iguales",
	ReasonSharedCandidate:  "el candidato también le sirve a otro movimiento",
	ReasonComboAmbiguous:   "más de una combinación posible",
	ReasonRefAmountDiffers: "la referencia no cierra con el importe",
	ReasonClientOther:      "el cliente dice que es otra cosa",
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
		"decision_tray", "decision", "decided_by", "decided_role", "decided_at", "decision_revision"}}
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		// Anyone but an operator sees results, not the Suggested tray:
		// automatic matches, links by hand, approved matches and what an
		// operator sent to Exceptions. A suggestion still undecided, or one
		// the operator rejected, is the operator's (spec section 1).
		operator, err := seesSuggested(ctx, tx)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			select m.id, m.rule_id, m.rule_version, m.tray, m.movement_ids::text[], m.item_ids::text[], m.adjustments, m.evidence,
			       coalesce(dx.tray, ds.tray, ''), coalesce(dx.action, ds.action, ms.action, ''),
			       coalesce(dd.user_id::text, md.user_id::text, ''), coalesce(dd.role, md.role, ''),
			       coalesce(dd.at, md.at), coalesce(dd.revision, md.revision)
			  from matching.matches m
			  left join matching.decision_state dx on dx.tenant_id = m.tenant_id and dx.match_id = m.id
			        and dx.tray = 'exception' and dx.action is not null
			  left join matching.decision_state ds on ds.tenant_id = m.tenant_id and ds.match_id = m.id
			        and ds.tray = 'suggested' and ds.action is not null
			  left join matching.decisions dd on dd.id = coalesce(dx.id, ds.id)
			  left join matching.movement_decision_state ms on ms.tenant_id = m.tenant_id and ms.match_id = m.id and ms.action = 'link'
			  left join matching.movement_decisions md on md.id = ms.id
			 where exists (select 1 from ledger.movements l where l.id = any(m.movement_ids) and l.date between $1 and $2)
			   and ($3 or m.tray = 'auto' or ms.action = 'link' or ds.action in ('approve', 'escalate') or dx.action is not null)
			 order by m.created_at, m.id`, from, to, operator)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, rule, tray, dTray, action, by, role string
			var version int
			var mv, it []string
			var adjJSON, evJSON []byte
			var at *time.Time
			var rev *int
			if err := rows.Scan(&id, &rule, &version, &tray, &mv, &it, &adjJSON, &evJSON, &dTray, &action, &by, &role, &at, &rev); err != nil {
				return err
			}
			m := Match{}
			if err := unmarshalEvidence(adjJSON, evJSON, &m); err != nil {
				return err
			}
			var adj []string
			for _, a := range m.Adjustments {
				adj = append(adj, a.Kind+":"+a.Amount.String())
			}
			atS, revS := "", ""
			if at != nil {
				atS = at.UTC().Format(time.RFC3339)
			}
			if rev != nil {
				revS = strconv.Itoa(*rev)
			}
			out = append(out, []string{id, rule, strconv.Itoa(version), tray, strings.Join(mv, " "), strings.Join(it, " "),
				strings.Join(adj, " "), m.Reference, strconv.Itoa(m.DateDiffDays), strconv.FormatBool(m.CrossPeriod),
				strconv.FormatBool(m.LowConfidence), dTray, action, by, role, atS, revS})
		}
		return rows.Err()
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
	}
	if err := json.Unmarshal(ev, &e); err != nil {
		return err
	}
	m.Reference, m.DateDiffDays, m.CrossPeriod = e.Reference, e.DateDiffDays, e.CrossPeriod
	m.MovePeriods, m.ItemPeriods, m.LowConfidence = e.MovePeriods, e.ItemPeriods, e.LowConfidence
	return nil
}
