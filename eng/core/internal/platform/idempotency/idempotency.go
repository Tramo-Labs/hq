// Package idempotency implements the Idempotency-Key contract of
// eng/ARQUITECTURA.md section 7 as HTTP middleware.
//
// For a POST carrying Idempotency-Key (a UUID):
//   - first time: the key is claimed (status 0), the handler runs, and a
//     response below 500 is stored for 24 hours;
//   - same key, same method, path and body: the stored response is replayed
//     with Idempotent-Replayed: true, the handler does not run again;
//   - same key, different request: 422 idempotency_key_reused;
//   - same key while the first request is still running: 409;
//   - a 5xx or a panic releases the key so the client can retry.
//
// The key is scoped to the tenant of the caller, or to anon:<path> before a
// tenant exists (registration). Whether a route requires the header is
// declared in api/openapi.yaml and enforced by the generated code; this
// middleware only gives the header its meaning.
//
// Responses are stored in the database: handlers behind a key must never
// return secrets (tokens, keys) in the body.
package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

// Header is the request header name.
const Header = "Idempotency-Key"

// TTL is how long a key is remembered.
const TTL = 24 * time.Hour

// MaxBody bounds the request body hashed and buffered (statement uploads
// included).
const MaxBody = 32 << 20

// Middleware returns the idempotency middleware backed by d.
func Middleware(d *db.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := r.Header.Get(Header)
			if r.Method != http.MethodPost || raw == "" {
				next.ServeHTTP(w, r)
				return
			}
			key, err := uuid.Parse(raw)
			if err != nil {
				httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "Idempotency-Key must be a UUID")
				return
			}
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBody))
			if err != nil {
				httpx.WriteProblem(w, r, http.StatusRequestEntityTooLarge, httpx.CodeTooLarge, "")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			hash, err := requestHash(r, body)
			if err != nil {
				httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "malformed multipart body")
				return
			}

			scope, tenant := "anon:"+r.URL.Path, (*uuid.UUID)(nil)
			if s, ok := db.ScopeFrom(r.Context()); ok {
				scope, tenant = s.TenantID.String(), &s.TenantID
			}
			st := store{d}
			claimed, err := st.claim(r.Context(), scope, key, tenant, hash)
			if err != nil {
				httpx.Logger(r.Context()).Error("idempotency claim", "err", err)
				httpx.WriteProblem(w, r, http.StatusInternalServerError, httpx.CodeInternal, "")
				return
			}
			if !claimed {
				st.replay(w, r, scope, key, hash)
				return
			}

			rec := &recorder{header: http.Header{}, status: http.StatusOK}
			released := false
			release := func() {
				if !released {
					released = true
					// Detached from the request: the client may be gone.
					_ = st.release(context.WithoutCancel(r.Context()), scope, key)
				}
			}
			defer func() {
				if v := recover(); v != nil {
					release()
					panic(v)
				}
			}()
			next.ServeHTTP(rec, r)

			if rec.status >= 500 {
				release()
			} else if err := st.save(context.WithoutCancel(r.Context()), scope, key, rec); err != nil {
				httpx.Logger(r.Context()).Error("idempotency save", "err", err)
				release()
			}
			rec.flush(w)
		})
	}
}

// requestHash identifies "the same request": method, path and body. For
// multipart/form-data the raw bytes include a boundary the client picks at
// random on every attempt, so a browser retrying the same upload would look
// different. There the hash covers each part instead: field name, file
// name, part content type and content, in order, length prefixed.
func requestHash(r *http.Request, body []byte) ([]byte, error) {
	h := sha256.New()
	h.Write([]byte(r.Method + "\n" + r.URL.Path + "\n"))
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" {
		h.Write(body)
		return h.Sum(nil), nil
	}
	if params["boundary"] == "" {
		return nil, errors.New("multipart without boundary")
	}
	field := func(b []byte) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(b)))
		h.Write(n[:])
		h.Write(b)
	}
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	parts := 0
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(p)
		if err != nil {
			return nil, err
		}
		field([]byte(p.FormName()))
		field([]byte(p.FileName()))
		field([]byte(p.Header.Get("Content-Type")))
		field(content)
		parts++
	}
	if parts == 0 {
		return nil, errors.New("multipart without parts")
	}
	return h.Sum(nil), nil
}

type store struct{ d *db.DB }

// claim inserts the key, or takes over an expired one. It reports whether
// this request owns the key.
func (s store) claim(ctx context.Context, scope string, key uuid.UUID, tenant *uuid.UUID, hash []byte) (bool, error) {
	var owned bool
	err := s.d.System(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			insert into idempotency_requests (scope, key, tenant_id, request_hash)
			values ($1, $2, $3, $4)
			on conflict (scope, key) do update
			  set tenant_id = excluded.tenant_id, request_hash = excluded.request_hash,
			      status = 0, content_type = null, response = null, created_at = now()
			  where idempotency_requests.created_at < now() - make_interval(secs => $5)
			returning true`,
			scope, key, tenant, hash, TTL.Seconds()).Scan(&owned)
		if errors.Is(err, pgx.ErrNoRows) {
			owned = false
			return nil
		}
		return err
	})
	return owned, err
}

func (s store) replay(w http.ResponseWriter, r *http.Request, scope string, key uuid.UUID, hash []byte) {
	var stored []byte
	var status int
	var contentType *string
	var body []byte
	err := s.d.System(r.Context(), func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(),
			`select request_hash, status, content_type, response from idempotency_requests where scope = $1 and key = $2`,
			scope, key).Scan(&stored, &status, &contentType, &body)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Released between our claim and this read: ask the client to retry.
		httpx.WriteProblem(w, r, http.StatusConflict, httpx.CodeIdempotencyInFlight, "retry the request")
	case err != nil:
		httpx.Logger(r.Context()).Error("idempotency replay", "err", err)
		httpx.WriteProblem(w, r, http.StatusInternalServerError, httpx.CodeInternal, "")
	case !bytes.Equal(stored, hash):
		httpx.WriteProblem(w, r, http.StatusUnprocessableEntity, httpx.CodeIdempotencyMismatch,
			"this Idempotency-Key was used with a different request")
	case status == 0:
		httpx.WriteProblem(w, r, http.StatusConflict, httpx.CodeIdempotencyInFlight,
			"a request with this Idempotency-Key is still in progress")
	default:
		if contentType != nil {
			w.Header().Set("Content-Type", *contentType)
		}
		w.Header().Set("Idempotent-Replayed", "true")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}
}

func (s store) save(ctx context.Context, scope string, key uuid.UUID, rec *recorder) error {
	return s.d.System(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`update idempotency_requests set status = $3, content_type = $4, response = $5 where scope = $1 and key = $2`,
			scope, key, rec.status, rec.header.Get("Content-Type"), rec.body.Bytes())
		return err
	})
}

func (s store) release(ctx context.Context, scope string, key uuid.UUID) error {
	return s.d.System(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `delete from idempotency_requests where scope = $1 and key = $2`, scope, key)
		return err
	})
}

// Purge deletes expired keys. The worker runs it periodically.
func Purge(ctx context.Context, d *db.DB) (int64, error) {
	var n int64
	err := d.System(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `delete from idempotency_requests where created_at < now() - make_interval(secs => $1)`, TTL.Seconds())
		n = tag.RowsAffected()
		return err
	})
	return n, err
}

// recorder buffers the handler response so it can be stored before it is
// sent.
type recorder struct {
	header      http.Header
	status      int
	wroteHeader bool
	body        bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status, r.wroteHeader = code, true
	}
}

func (r *recorder) Write(b []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	return r.body.Write(b)
}

func (r *recorder) flush(w http.ResponseWriter) {
	for k, v := range r.header {
		w.Header()[k] = v
	}
	w.WriteHeader(r.status)
	_, _ = w.Write(r.body.Bytes())
}
