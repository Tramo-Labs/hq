// Package app assembles the core HTTP API: the generated router from
// api/openapi.yaml, request validation against the same spec, the platform
// middleware chain and the handlers of every module.
//
// Middleware order, outermost first:
//
//	RequestMeta -> Recover -> Authenticate -> spec validation -> Idempotency -> handler
//
// Validation runs before idempotency so a malformed request never claims a
// key. Authentication runs before idempotency so keys are scoped to the
// caller's tenant.
package app

import (
	"context"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/go-chi/chi/v5"
	middleware "github.com/oapi-codegen/nethttp-middleware"

	apigen "github.com/Tramo-Labs/hq/eng/core/gen/api"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/idempotency"
)

// Deps are the shared dependencies of the API.
type Deps struct {
	DB         *db.DB
	TrustProxy bool
	// Authenticate puts the caller's scope in the context when a valid
	// bearer token is present. Nil means no authentication (tests of the
	// platform only).
	Authenticate func(http.Handler) http.Handler
	// DocsDir serves the Swagger UI and the spec when set.
	DocsDir string
}

// Handlers implements the generated strict interface by embedding one
// handler set per module.
type Handlers struct {
	platformHandlers
}

// New returns the full HTTP handler.
func New(d Deps, h apigen.StrictServerInterface) (http.Handler, error) {
	spec, err := apigen.GetSwagger()
	if err != nil {
		return nil, err
	}
	spec.Servers = nil // validate paths regardless of the host serving them

	r := chi.NewRouter()
	r.Use(httpx.RequestMeta(d.TrustProxy), httpx.Recover)
	if d.Authenticate != nil {
		r.Use(d.Authenticate)
	}
	if d.DocsDir != "" {
		r.Handle("/docs/*", http.StripPrefix("/docs/", http.FileServer(http.Dir(d.DocsDir))))
	}
	r.Group(func(r chi.Router) {
		r.Use(middleware.OapiRequestValidatorWithOptions(spec, &middleware.Options{
			Options: openapi3filter.Options{
				// Bearer tokens are checked by Authenticate; the
				// validator only checks shapes.
				AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
			},
			ErrorHandlerWithOpts: validationError,
		}))
		r.Use(idempotency.Middleware(d.DB))
		strict := apigen.NewStrictHandlerWithOptions(h, nil, apigen.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
				httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "malformed request body")
			},
			ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
				writeHandlerError(w, r, err)
			},
		})
		apigen.HandlerWithOptions(strict, apigen.ChiServerOptions{
			BaseRouter: r,
			ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
				httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
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
	case strings.Contains(err.Error(), idempotency.Header) && r.Header.Get(idempotency.Header) == "":
		httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeIdempotencyRequired,
			"this operation requires an Idempotency-Key header (UUID)")
	default:
		// The first line of a kin-openapi error names the parameter or
		// field and the rule; it never echoes the submitted value.
		httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
	}
}

// Error lets handlers return a problem from the strict interface.
type Error struct {
	Status int
	Code   string
	Detail string
}

func (e *Error) Error() string { return e.Code + ": " + e.Detail }

func writeHandlerError(w http.ResponseWriter, r *http.Request, err error) {
	if e, ok := err.(*Error); ok {
		httpx.WriteProblem(w, r, e.Status, e.Code, e.Detail)
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

// NewHandlers builds the module handlers.
func NewHandlers(d Deps) *Handlers {
	return &Handlers{platformHandlers: platformHandlers{ready: d.DB.Ping}}
}
