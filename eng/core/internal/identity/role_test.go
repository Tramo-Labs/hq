package identity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
)

// The role modules see comes only from the verified access token. Nothing a
// client puts in the request can change it.
func TestRequestCannotOverrideRole(t *testing.T) {
	seed := make([]byte, 32)
	_, _ = rand.Read(seed)
	s, err := NewSigner(seed, "k1")
	if err != nil {
		t.Fatal(err)
	}
	viewer := Principal{UserID: uuid.New(), TenantID: uuid.New(), Role: RoleViewer}
	tok := mustIssue(t, s, viewer)

	var seen db.Scope
	var hadScope bool
	h := Authenticate(s)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen, hadScope = db.ScopeFrom(r.Context())
	}))

	// Everything a client controls says "approver" (or another tenant).
	r := httptest.NewRequest(http.MethodPost, "/v1/matches/x/decision?role=approver&tenantId="+uuid.NewString(),
		strings.NewReader(`{"outcome":"approved","role":"approver","tenantId":"`+uuid.NewString()+`"}`))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("X-Role", "approver")
	r.Header.Set("X-Tenant-Id", uuid.NewString())
	// Even a scope already in the context (a buggy earlier middleware) is replaced.
	r = r.WithContext(db.WithScope(r.Context(), db.Scope{TenantID: uuid.New(), UserID: uuid.New(), Role: RoleApprover}))
	h.ServeHTTP(httptest.NewRecorder(), r)

	if !hadScope || seen.Role != RoleViewer || seen.TenantID != viewer.TenantID || seen.UserID != viewer.UserID {
		t.Fatalf("scope = %+v, want the viewer from the token", seen)
	}
}

// A token whose role claim was edited does not verify, and a token signed
// by us with a role outside the enum is refused too.
func TestTamperedOrUnknownRoleIsRejected(t *testing.T) {
	seed := make([]byte, 32)
	_, _ = rand.Read(seed)
	s, _ := NewSigner(seed, "k1")
	if _, err := s.Verify(mustIssue(t, s, Principal{UserID: uuid.New(), TenantID: uuid.New(), Role: "superadmin"})); err == nil {
		t.Fatal("accepted a role outside the enum")
	}

	tok := mustIssue(t, s, Principal{UserID: uuid.New(), TenantID: uuid.New(), Role: RoleViewer})
	parts := strings.Split(tok, ".")
	payload := strings.Replace(decodeSeg(t, parts[1]), `"role":"viewer"`, `"role":"approver"`, 1)
	forged := parts[0] + "." + encodeSeg(payload) + "." + parts[2]

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	r.Header.Set("Authorization", "Bearer "+forged)
	reached := false
	Authenticate(s)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })).ServeHTTP(rec, r)
	if reached || rec.Code != http.StatusUnauthorized {
		t.Fatalf("forged role: reached=%v code=%d", reached, rec.Code)
	}
}

func decodeSeg(t *testing.T, s string) string {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func encodeSeg(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// Without a token there is no scope at all: an anonymous request cannot
// put one in.
func TestNoTokenNoScope(t *testing.T) {
	seed := make([]byte, 32)
	_, _ = rand.Read(seed)
	s, _ := NewSigner(seed, "k1")
	var has bool
	h := Authenticate(s)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { _, has = db.ScopeFrom(r.Context()) }))
	r := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	r.Header.Set("X-Role", "operator")
	h.ServeHTTP(httptest.NewRecorder(), r.WithContext(context.Background()))
	if has {
		t.Fatal("anonymous request got a scope")
	}
}
