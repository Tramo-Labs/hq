// Package app assembles the core HTTP API: the generated router from
// api/openapi.yaml, request validation against the same spec, the platform
// middleware chain and the handlers of every module.
//
// Middleware order, outermost first:
//
//	RequestMeta -> Recover -> CORS -> refresh Origin check -> Authenticate -> spec validation (+ auth required?) -> Idempotency -> handler
//
// Authenticate reads a bearer token when present and puts the principal and
// the tenant scope in the context. Whether an operation needs one is
// declared in the spec: everything requires the bearer scheme unless the
// operation says `security: []`. Validation runs before idempotency so a
// malformed or anonymous request never claims a key, and keys are scoped to
// the caller's tenant.
package app

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	middleware "github.com/oapi-codegen/nethttp-middleware"

	apigen "github.com/Tramo-Labs/hq/eng/core/gen/api"
	"github.com/Tramo-Labs/hq/eng/core/internal/documents"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/ledger"
	"github.com/Tramo-Labs/hq/eng/core/internal/matching"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/audit"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/idempotency"
	"github.com/Tramo-Labs/hq/eng/core/internal/portal"
)

// Deps are the shared dependencies of the API.
type Deps struct {
	DB       *db.DB
	Identity *identity.Service
	// TrustedProxies whose X-Forwarded-For is believed (config.TrustedProxies).
	TrustedProxies []netip.Prefix
	// CORSOrigins may call the api from a browser (config.CORSOrigins, D-18).
	CORSOrigins httpx.Origins
	// DocsDir serves the Swagger UI and the spec when set.
	DocsDir string
	// DenialWindow coalesces repeated 403s of one user on one operation
	// into the first one's audit row (DefaultDenialWindow when zero).
	DenialWindow time.Duration
}

// DefaultDenialWindow is how long repeats of a denial count on its row.
const DefaultDenialWindow = time.Minute

// Handlers implements the generated strict interface by embedding one
// handler set per module.
type Handlers struct {
	platformHandlers
	pendingHandlers
	*identity.Handlers
	*ledgerAPI
	*documentsAPI
	*matchingAPI
	*portalAPI
}

// Aliases give each embedded module handler set its own field name.
type (
	ledgerAPI    = ledger.Handlers
	documentsAPI = documents.Handlers
	matchingAPI  = matching.Handlers
	portalAPI    = portal.Handlers
)

// NewHandlers builds the module handlers.
func NewHandlers(d Deps) *Handlers {
	return &Handlers{
		platformHandlers: platformHandlers{ready: d.DB.Ping},
		Handlers:         identity.NewHandlers(d.Identity),
		// Data modules: every query through db.InTenant.
		ledgerAPI: ledger.NewHandlers(&ledger.PGStore{DB: d.DB}),
		// An invoice generates its open item in the same transaction (F2).
		documentsAPI: documents.NewHandlers(&documents.PGStore{DB: d.DB, OnSaved: (&matching.PGStore{DB: d.DB}).InvoiceItemTx}),
		matchingAPI:  matching.NewHandlers(&matching.PGStore{DB: d.DB}),
		// Read views over the modules above, under the caller's scope.
		portalAPI: &portal.Handlers{DB: d.DB, Identity: d.Identity,
			Ledger: &ledger.PGStore{DB: d.DB}, Matching: &matching.PGStore{DB: d.DB}},
	}
}

var errAuthRequired = errors.New("authentication required")

// New returns the full HTTP handler.
func New(d Deps, h apigen.StrictServerInterface) (http.Handler, error) {
	spec, err := apigen.GetSwagger()
	if err != nil {
		return nil, err
	}
	spec.Servers = nil // validate paths regardless of the host serving them

	r := chi.NewRouter()
	r.Use(httpx.RequestMeta(d.TrustedProxies), httpx.Recover, httpx.CORS(d.CORSOrigins), identity.RequireAllowedOrigin(d.CORSOrigins), identity.Authenticate(d.Identity.Signer))
	if d.DocsDir != "" {
		r.Handle("/docs/*", http.StripPrefix("/docs/", http.FileServer(http.Dir(d.DocsDir))))
	}
	r.Group(func(r chi.Router) {
		r.Use(middleware.OapiRequestValidatorWithOptions(spec, &middleware.Options{
			Options: openapi3filter.Options{
				// Called only for operations that declare the bearer
				// scheme; the token itself was verified by Authenticate.
				AuthenticationFunc: func(ctx context.Context, in *openapi3filter.AuthenticationInput) error {
					if _, ok := identity.PrincipalFrom(in.RequestValidationInput.Request.Context()); !ok {
						return errAuthRequired
					}
					return nil
				},
			},
			ErrorHandlerWithOpts: validationError,
		}))
		r.Use(idempotency.Middleware(d.DB))
		strict := apigen.NewStrictHandlerWithOptions(h, []apigen.StrictMiddlewareFunc{auditDenials(d.DB, d.DenialWindow)}, apigen.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
				httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "malformed request body")
			},
			ResponseErrorHandlerFunc: writeHandlerError,
		})
		apigen.HandlerWithOptions(strict, apigen.ChiServerOptions{
			BaseRouter: r,
			ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
				httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeBadRequest, badRequestDetail(err))
			},
		})
	})
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteProblem(w, r, http.StatusNotFound, httpx.CodeNotFound, "")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteProblem(w, r, http.StatusMethodNotAllowed, httpx.CodeBadRequest, "")
	})
	return r, nil
}

func validationError(_ context.Context, err error, w http.ResponseWriter, r *http.Request, opts middleware.ErrorHandlerOpts) {
	switch {
	case opts.StatusCode == http.StatusNotFound:
		httpx.WriteProblem(w, r, http.StatusNotFound, httpx.CodeNotFound, "")
	case opts.StatusCode == http.StatusMethodNotAllowed:
		httpx.WriteProblem(w, r, http.StatusMethodNotAllowed, httpx.CodeBadRequest, "")
	case opts.StatusCode == http.StatusUnauthorized:
		w.Header().Set("WWW-Authenticate", `Bearer realm="tramo"`)
		httpx.WriteProblem(w, r, http.StatusUnauthorized, httpx.CodeUnauthorized, "a valid bearer token is required")
	case strings.Contains(err.Error(), idempotency.Header) && r.Header.Get(idempotency.Header) == "":
		httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeIdempotencyRequired,
			"this operation requires an Idempotency-Key header (UUID)")
	default:
		// kin-openapi's message prints the schema and the submitted value
		// (passwords included): only the parameter or field goes out.
		httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeBadRequest, badRequestDetail(err))
	}
}

// auditDenials writes an `authz.denied` audit row for every operation that
// answers 403 (spec §1: every action of the roles table is audited with
// who, when and from where). The row is written in its own tenant
// transaction, since the denied action's transaction rolled back, and
// carries the caller (user_id), the resolved client IP and request id
// (httpx.MetaFrom, through audit.Record), the operationId as subject and the
// role, method and path as detail. Never the body or the query string. A
// failure to write it is logged and the caller still gets the 403.
func auditDenials(d *db.DB, window time.Duration) apigen.StrictMiddlewareFunc {
	if window <= 0 {
		window = DefaultDenialWindow
	}
	return func(next apigen.StrictHandlerFunc, operationID string) apigen.StrictHandlerFunc {
		// The generator passes the Go name; the spec's operationId is the
		// same with a lower case first letter.
		if operationID != "" {
			operationID = strings.ToLower(operationID[:1]) + operationID[1:]
		}
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
			resp, err := next(ctx, w, r, req)
			var e *httpx.Error
			if !errors.As(err, &e) || e.Status != http.StatusForbidden {
				return resp, err
			}
			p, ok := identity.PrincipalFrom(ctx)
			if !ok {
				return resp, err
			}
			subject := "operation:" + operationID
			if aerr := d.InTenant(ctx, func(tx pgx.Tx) error {
				// Within the window, a repeat only bumps the counter of the
				// first denial's row (audit_log is append only). Two first
				// denials racing may both write a row; that is accepted.
				var first int64
				err := tx.QueryRow(ctx, `
					select id from audit_log
					where action = 'authz.denied' and tenant_id = app_current_tenant()
					  and user_id = $1 and subject = $2 and at > now() - make_interval(secs => $3)
					order by at desc limit 1`, p.UserID, subject, window.Seconds()).Scan(&first)
				if err == nil {
					_, err = tx.Exec(ctx, `
						insert into audit_denial_repeats (audit_id, tenant_id, count, last_at)
						values ($1, app_current_tenant(), 1, now())
						on conflict (audit_id) do update
						  set count = audit_denial_repeats.count + 1, last_at = excluded.last_at`, first)
					return err
				}
				if !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
				return audit.Record(ctx, tx, audit.Entry{Action: "authz.denied", Subject: subject,
					Detail: map[string]any{"role": p.Role, "method": r.Method, "path": r.URL.Path}})
			}); aerr != nil {
				httpx.Logger(ctx).Error("audit denial", "err", aerr, "operation", operationID)
			}
			return resp, err
		}
	}
}

func writeHandlerError(w http.ResponseWriter, r *http.Request, err error) {
	var e *httpx.Error
	if errors.As(err, &e) {
		httpx.WriteError(w, r, e)
		return
	}
	httpx.Logger(r.Context()).Error("handler", "err", err)
	httpx.WriteProblem(w, r, http.StatusInternalServerError, httpx.CodeInternal, "")
}

// platformHandlers answers the platform operations of the spec.
type platformHandlers struct {
	ready func(context.Context) error
}

func (p platformHandlers) Healthz(context.Context, apigen.HealthzRequestObject) (apigen.HealthzResponseObject, error) {
	return apigen.Healthz204Response{}, nil
}

func (p platformHandlers) Readyz(ctx context.Context, _ apigen.ReadyzRequestObject) (apigen.ReadyzResponseObject, error) {
	if p.ready != nil {
		if err := p.ready(ctx); err != nil {
			httpx.Logger(ctx).Warn("not ready", "err", err)
			return apigen.Readyz503ApplicationProblemPlusJSONResponse{
				Type: "about:blank", Title: "Service Unavailable", Status: 503, Code: "not_ready",
			}, nil
		}
	}
	return apigen.Readyz204Response{}, nil
}
