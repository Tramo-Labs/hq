// Package portal serves the read views of the client portal and the
// operator console: onboarding status, the console's tenant list and the
// client's downloads. It owns no table: it reads through the stores of
// identity, ledger and matching, always under the tenant scope of the
// access token (or, for the console, of each of the caller's own
// memberships). See README.md.
package portal

import (
	"bytes"
	"context"
	"encoding/csv"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	apigen "github.com/Tramo-Labs/hq/eng/core/gen/api"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/ledger"
	"github.com/Tramo-Labs/hq/eng/core/internal/matching"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

// OpenCap bounds each count of open exceptions (matches and movements).
const OpenCap = 1000

// Handlers implements the portal operations of the strict interface.
type Handlers struct {
	DB       *db.DB
	Identity *identity.Service
	Ledger   *ledger.PGStore
	Matching *matching.PGStore
}

var errNotImplemented = &httpx.Error{Status: http.StatusNotImplemented, Code: "not_implemented",
	Detail: "not available yet; see the API description"}

// GetMyTenant returns the onboarding status of the active tenant.
func (h *Handlers) GetMyTenant(ctx context.Context, _ apigen.GetMyTenantRequestObject) (apigen.GetMyTenantResponseObject, error) {
	p, _ := identity.PrincipalFrom(ctx)
	st, err := h.status(ctx, p.Role)
	if err != nil {
		return nil, err
	}
	return apigen.GetMyTenant200JSONResponse(st), nil
}

// ListTenants returns the status of every tenant where the caller is
// operator or accountant, each read under its own scope.
func (h *Handlers) ListTenants(ctx context.Context, _ apigen.ListTenantsRequestObject) (apigen.ListTenantsResponseObject, error) {
	p, _ := identity.PrincipalFrom(ctx)
	principals, err := h.Identity.ConsolePrincipals(ctx, p)
	if err != nil {
		return nil, err
	}
	if len(principals) == 0 {
		return nil, &httpx.Error{Status: http.StatusForbidden, Code: httpx.CodeForbidden,
			Detail: "operator or accountant role required"}
	}
	out := apigen.ListTenants200JSONResponse{Items: make([]apigen.TenantStatus, 0, len(principals))}
	for _, pr := range principals {
		st, err := h.status(identity.WithPrincipal(ctx, pr), pr.Role)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, st)
	}
	return out, nil
}

func (h *Handlers) status(ctx context.Context, role string) (apigen.TenantStatus, error) {
	t, err := h.Identity.Tenant(ctx)
	if err != nil {
		return apigen.TenantStatus{}, err
	}
	st := apigen.TenantStatus{Id: t.ID, Name: t.Name, Role: apigen.Role(role)}
	if t.CUIT != nil {
		c := apigen.Cuit(strconv.FormatInt(*t.CUIT, 10))
		st.Cuit = &c
	}
	arca := apigen.OnboardingStep{Kind: apigen.StepArca, Status: apigen.StepPending,
		Detail: "CUIT not on file yet"}
	if t.CUIT != nil {
		arca.Detail = "CUIT on file; ARCA verification is not connected yet"
	}
	bank, err := h.bankStep(ctx, t.ID)
	if err != nil {
		return apigen.TenantStatus{}, err
	}
	erp := apigen.OnboardingStep{Kind: apigen.StepErp, Status: apigen.StepPending,
		Detail: "no ERP connector available yet"}
	st.Steps = []apigen.OnboardingStep{arca, bank, erp}

	matches, err := h.Matching.Undecided(ctx, matching.Exception, OpenCap)
	if err != nil {
		return apigen.TenantStatus{}, err
	}
	movements, err := h.Matching.LatestPending(ctx, matching.Exception, OpenCap)
	if err != nil {
		return apigen.TenantStatus{}, err
	}
	st.OpenExceptions = len(matches) + len(movements)
	st.OpenExceptionsCapped = len(matches) >= OpenCap || len(movements) >= OpenCap
	return st, nil
}

// bankStep is verified once any account has an accepted statement.
func (h *Handlers) bankStep(ctx context.Context, tenant uuid.UUID) (apigen.OnboardingStep, error) {
	step := apigen.OnboardingStep{Kind: apigen.StepBank, Status: apigen.StepPending, Detail: "no bank account yet"}
	accounts, err := h.Ledger.Accounts(ctx)
	if err != nil {
		return step, err
	}
	if len(accounts) > 0 {
		step.Detail = "account created; no statement accepted yet"
	}
	for _, a := range accounts {
		sum, err := h.Ledger.LastStatement(ctx, tenant.String(), a.ID.String())
		if err != nil {
			return step, err
		}
		if sum != nil {
			step.Status, step.Detail = apigen.StepVerified, "statement accepted for "+a.Name
			return step, nil
		}
	}
	return step, nil
}

// ListMetrics returns the results panel of the active tenant for a month,
// computed by matching from the tenant's stored data. A metric with nothing
// to measure comes back no_data with a null value, never zero.
func (h *Handlers) ListMetrics(ctx context.Context, req apigen.ListMetricsRequestObject) (apigen.ListMetricsResponseObject, error) {
	ms, err := h.Matching.Metrics(ctx, req.Params.Period, time.Now())
	if err != nil {
		return nil, err
	}
	out := apigen.ListMetrics200JSONResponse{Items: make([]apigen.Metric, 0, len(ms))}
	for _, m := range ms {
		item := apigen.Metric{Key: m.Key, Label: m.Label, Unit: apigen.MetricUnit(m.Unit), Period: m.Period,
			Source: m.Source, List: m.List, Status: apigen.MetricMeasured, Value: m.Value}
		if m.Value == nil {
			item.Status = apigen.MetricNoData
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

var (
	allFrom = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	allTo   = time.Date(2100, 12, 31, 0, 0, 0, 0, time.UTC)
)

// ListExports reports what the client can download and how many rows.
func (h *Handlers) ListExports(ctx context.Context, _ apigen.ListExportsRequestObject) (apigen.ListExportsResponseObject, error) {
	movements, err := h.Ledger.MovementsBetween(ctx, allFrom, allTo)
	if err != nil {
		return nil, err
	}
	audits, err := h.auditCount(ctx)
	if err != nil {
		return nil, err
	}
	matches, err := h.Matching.MatchRows(ctx, allFrom, allTo)
	if err != nil {
		return nil, err
	}
	nm, nx, na := len(movements), len(matches)-1, audits // MatchRows starts with its header
	return apigen.ListExports200JSONResponse{Items: []apigen.ExportInfo{
		{Kind: apigen.ExportMovements, Format: apigen.ExportCSV, Available: true, Rows: &nm},
		{Kind: apigen.ExportMatches, Format: apigen.ExportCSV, Available: true, Rows: &nx},
		{Kind: apigen.ExportAudit, Format: apigen.ExportCSV, Available: true, Rows: &na},
	}}, nil
}

// GetExport streams one export as CSV.
func (h *Handlers) GetExport(ctx context.Context, req apigen.GetExportRequestObject) (apigen.GetExportResponseObject, error) {
	from, to := allFrom, allTo
	if req.Params.From != nil {
		from = req.Params.From.Time
	}
	if req.Params.To != nil {
		to = req.Params.To.Time
	}
	if to.Before(from) {
		return nil, &httpx.Error{Status: http.StatusBadRequest, Code: httpx.CodeBadRequest, Detail: "to is before from"}
	}
	var rows [][]string
	var err error
	switch req.Kind {
	case apigen.KindMovements:
		rows, err = h.movementRows(ctx, from, to)
	case apigen.KindAudit:
		rows, err = h.auditRows(ctx, from, to)
	case apigen.KindMatches:
		rows, err = h.Matching.MatchRows(ctx, from, to)
	default:
		return nil, errNotImplemented
	}
	if err != nil {
		return nil, err
	}
	body, err := writeCSV(rows)
	if err != nil {
		return nil, err
	}
	disposition := `attachment; filename="` + string(req.Kind) + `.csv"`
	return apigen.GetExport200TextcsvResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body)),
		Headers: apigen.GetExport200ResponseHeaders{ContentDisposition: &disposition}}, nil
}

func (h *Handlers) movementRows(ctx context.Context, from, to time.Time) ([][]string, error) {
	ms, err := h.Ledger.MovementsBetween(ctx, from, to)
	if err != nil {
		return nil, err
	}
	rows := [][]string{{"date", "account_id", "amount", "balance", "description", "reference", "external_ref"}}
	for _, m := range ms {
		balance := ""
		if m.Balance != nil {
			balance = m.Balance.String()
		}
		rows = append(rows, []string{m.Date.Format("2006-01-02"), m.AccountID.String(), m.Amount.String(), balance,
			m.Description, m.Reference, m.ExternalRef})
	}
	return rows, nil
}

func (h *Handlers) auditCount(ctx context.Context) (int, error) {
	var n int
	err := h.DB.InTenant(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select count(*) from audit_log`).Scan(&n)
	})
	return n, err
}

// auditRows lists the tenant's own trail (RLS keeps it to the tenant).
func (h *Handlers) auditRows(ctx context.Context, from, to time.Time) ([][]string, error) {
	rows := [][]string{{"at", "action", "subject", "user_id", "request_id", "ip"}}
	err := h.DB.InTenant(ctx, func(tx pgx.Tx) error {
		rs, err := tx.Query(ctx, `
			select at, action, subject, coalesce(user_id::text, ''), coalesce(request_id, ''), coalesce(host(ip), '')
			from audit_log where at >= $1::timestamptz and at < $2::timestamptz + interval '1 day'
			order by at, id`, from, to)
		if err != nil {
			return err
		}
		defer rs.Close()
		for rs.Next() {
			var at time.Time
			var action, subject, user, req, ip string
			if err := rs.Scan(&at, &action, &subject, &user, &req, &ip); err != nil {
				return err
			}
			rows = append(rows, []string{at.UTC().Format(time.RFC3339), action, subject, user, req, ip})
		}
		return rs.Err()
	})
	return rows, err
}

// writeCSV renders rows, neutralizing cells a spreadsheet would run as a
// formula (=, +, -, @, tab, carriage return): bank descriptions are written
// by third parties.
func writeCSV(rows [][]string) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	for i, r := range rows {
		safe := make([]string, len(r))
		for j, cell := range r {
			safe[j] = cell
			if i > 0 && isFormula(cell) {
				safe[j] = "'" + cell
			}
		}
		if err := w.Write(safe); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// isFormula reports a cell that starts like a spreadsheet formula. A plain
// negative amount ("-1234.56") is a number, not a formula, and is kept.
func isFormula(s string) bool {
	if s == "" {
		return false
	}
	switch s[0] {
	case '=', '+', '@', '\t', '\r':
		return true
	case '-':
		_, err := strconv.ParseFloat(s, 64)
		return err != nil
	}
	return false
}
