// Package mpfake is a fake of Mercado Pago's OAuth for tests and make dev
// (ADR 0010). It checks what the real one documents: client credentials,
// a single use authorization code valid 10 minutes, the redirect_uri, and
// the PKCE S256 challenge against the verifier. Refresh tokens rotate (the
// old one stops working). Revoke makes every token of a seller answer 400
// invalid_grant, as when the seller removes the app; FailNext answers the
// next requests with a status, for 429 and 5xx.
package mpfake

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

// Fake is a running fake.
type Fake struct {
	*httptest.Server
	ClientID, ClientSecret, RedirectURI string
	// ExpiresIn is what tokens last (Mercado Pago: 180 days).
	ExpiresIn time.Duration
	// NoRefresh issues tokens without a refresh token (an app without
	// offline_access).
	NoRefresh bool
	// Now is the fake's clock, for code expiry.
	Now func() time.Time

	mu       sync.Mutex
	codes    map[string]grant
	refresh  map[string]int64 // refresh token -> seller
	revoked  map[int64]bool
	fail     []int
	requests int
}

type grant struct {
	challenge, method, redirect string
	seller                      int64
	issued                      time.Time
	used                        bool
}

// New starts a fake for one app.
func New() *Fake {
	f := &Fake{ClientID: "app-123", ClientSecret: "secret-test", RedirectURI: "https://app.example.test/app/mercadopago/vuelta",
		ExpiresIn: 180 * 24 * time.Hour, Now: time.Now,
		codes: map[string]grant{}, refresh: map[string]int64{}, revoked: map[int64]bool{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

func token() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Grant stands in for the seller authorizing in the browser: it reads the
// authorization URL's parameters and returns the code Mercado Pago would
// put in the return URL. It refuses what the real one would.
func (f *Fake) Grant(clientID, redirect, challenge, method string, seller int64) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if clientID != f.ClientID || redirect != f.RedirectURI || challenge == "" || method != "S256" {
		return "", false
	}
	code := "TG-" + token()
	f.codes[code] = grant{challenge: challenge, method: method, redirect: redirect, seller: seller, issued: f.Now()}
	return code, true
}

// Revoke makes every token of seller invalid, as removing the app does.
func (f *Fake) Revoke(seller int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked[seller] = true
}

// FailNext answers the next len(statuses) token requests with them.
func (f *Fake) FailNext(statuses ...int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = append(f.fail, statuses...)
}

// Requests is how many token requests arrived.
func (f *Fake) Requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthError(w http.ResponseWriter, code, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": code, "message": msg, "status": 400})
}

func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/oauth/token" {
		http.NotFound(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	if len(f.fail) > 0 {
		status := f.fail[0]
		f.fail = f.fail[1:]
		writeJSON(w, status, map[string]any{"error": "fake_failure"})
		return
	}
	var in map[string]string
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		oauthError(w, "invalid_request", "body is not JSON")
		return
	}
	if in["client_id"] != f.ClientID || in["client_secret"] != f.ClientSecret {
		oauthError(w, "invalid_client", "bad client")
		return
	}
	var seller int64
	switch in["grant_type"] {
	case "authorization_code":
		g, ok := f.codes[in["code"]]
		switch {
		case !ok, g.used, f.Now().Sub(g.issued) > 10*time.Minute, in["redirect_uri"] != g.redirect,
			s256(in["code_verifier"]) != g.challenge, f.revoked[g.seller]:
			oauthError(w, "invalid_grant", "invalid code")
			return
		}
		g.used = true
		f.codes[in["code"]] = g
		seller = g.seller
	case "refresh_token":
		s, ok := f.refresh[in["refresh_token"]]
		if !ok || f.revoked[s] {
			oauthError(w, "invalid_grant", "invalid refresh token")
			return
		}
		delete(f.refresh, in["refresh_token"]) // rotated: the old one is spent
		seller = s
	default:
		oauthError(w, "unsupported_grant_type", "no")
		return
	}
	out := map[string]any{"access_token": "APP_USR-" + token(), "token_type": "bearer",
		"expires_in": int64(f.ExpiresIn / time.Second), "scope": "read write offline_access",
		"user_id": seller, "public_key": "APP_USR-pub", "live_mode": true}
	if !f.NoRefresh {
		rt := "TG-" + token()
		f.refresh[rt] = seller
		out["refresh_token"] = rt
	} else {
		out["scope"] = "read write"
	}
	writeJSON(w, http.StatusOK, out)
}
