package httpx

import (
	"encoding/json"
	"net/http"
)

// Problem is the uniform error body (RFC 9457, application/problem+json).
// It mirrors components.schemas.Problem in api/openapi.yaml. Code is a
// stable machine readable identifier; Detail is for humans and never
// carries personal data, amounts or secrets.
type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Detail    string `json:"detail,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

// Stable error codes shared by every module.
const (
	CodeBadRequest          = "bad_request"
	CodeUnauthorized        = "unauthorized"
	CodeForbidden           = "forbidden"
	CodeNotFound            = "not_found"
	CodeConflict            = "conflict"
	CodeIdempotencyMismatch = "idempotency_key_reused"
	CodeIdempotencyInFlight = "idempotency_key_in_flight"
	CodeIdempotencyRequired = "idempotency_key_required"
	CodeTooLarge            = "payload_too_large"
	CodeRateLimited         = "rate_limited"
	CodeInternal            = "internal"
)

// Error is returned by strict handlers to answer with a specific problem.
// Any other error becomes a 500 without detail.
type Error struct {
	Status int
	Code   string
	Detail string
}

func (e *Error) Error() string { return e.Code + ": " + e.Detail }

// WriteProblem writes a problem response. Title defaults to the status text.
func WriteProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	p := Problem{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Code:   code,
		Detail: detail,
	}
	if m, ok := MetaFrom(r.Context()); ok {
		p.RequestID = m.RequestID
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

// Recover turns a panic into a 500 problem and logs it with the request id.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				Logger(r.Context()).Error("panic", "value", v)
				WriteProblem(w, r, http.StatusInternalServerError, CodeInternal, "")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
