package mercadopago

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Config is the Mercado Pago app, all from the environment (ADR 0010):
// MP_CLIENT_ID, MP_CLIENT_SECRET, MP_REDIRECT_URI, and MP_AUTH_URL and
// MP_API_URL, which default to Mercado Pago's and point at the fake in
// tests and make dev.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	AuthURL      string
	APIURL       string
}

// Defaults from Mercado Pago's OAuth pages (ADR 0010).
const (
	DefaultAuthURL = "https://auth.mercadopago.com/authorization"
	DefaultAPIURL  = "https://api.mercadopago.com"
)

// Configured reports whether an app is set: without it nothing connects.
func (c Config) Configured() bool {
	return c.ClientID != "" && c.ClientSecret != "" && c.RedirectURI != ""
}

func (c Config) authURL() string {
	if c.AuthURL != "" {
		return c.AuthURL
	}
	return DefaultAuthURL
}

func (c Config) apiURL() string {
	if c.APIURL != "" {
		return c.APIURL
	}
	return DefaultAPIURL
}

// Tokens is what the token endpoint answers.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    time.Duration
	Scope        string
	UserID       string
}

var (
	// ErrInvalidGrant: Mercado Pago answered 400 invalid_grant: the code or
	// refresh token is invalid, expired or revoked (or for another app).
	ErrInvalidGrant = errors.New("mercadopago: invalid_grant")
	// ErrRejected: another 4xx; for a code, the same outcome as invalid_grant.
	ErrRejected = errors.New("mercadopago: request rejected")
	// ErrUnavailable: no proper answer (network, 5xx, 429, malformed body).
	ErrUnavailable = errors.New("mercadopago: unavailable")
)

// client talks to Mercado Pago's token endpoint.
type client struct {
	cfg  Config
	http *http.Client
}

func newClient(cfg Config, hc *http.Client) *client {
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	return &client{cfg: cfg, http: hc}
}

// challenge is the PKCE S256 challenge of a verifier (RFC 7636).
func challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// authorizeURL is where the owner's browser goes. Parameter names as in
// Mercado Pago's authorization URL example with PKCE.
func (c *client) authorizeURL(state, verifier string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", c.cfg.ClientID)
	q.Set("redirect_uri", c.cfg.RedirectURI)
	q.Set("platform_id", "mp")
	q.Set("state", state)
	q.Set("code_challenge", challenge(verifier))
	q.Set("code_challenge_method", "S256")
	return c.cfg.authURL() + "?" + q.Encode()
}

func (c *client) exchange(ctx context.Context, code, verifier string) (Tokens, error) {
	return c.token(ctx, map[string]string{"grant_type": "authorization_code", "code": code,
		"redirect_uri": c.cfg.RedirectURI, "code_verifier": verifier})
}

func (c *client) refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	return c.token(ctx, map[string]string{"grant_type": "refresh_token", "refresh_token": refreshToken})
}

// maxBody bounds what is read of any answer.
const maxBody = 64 << 10

func (c *client) token(ctx context.Context, body map[string]string) (Tokens, error) {
	body["client_id"], body["client_secret"] = c.cfg.ClientID, c.cfg.ClientSecret
	raw, err := json.Marshal(body)
	if err != nil {
		return Tokens{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.apiURL()+"/oauth/token", bytes.NewReader(raw))
	if err != nil {
		return Tokens{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return Tokens{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		return Tokens{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	switch {
	case res.StatusCode == http.StatusOK || res.StatusCode == http.StatusCreated:
	case res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500:
		return Tokens{}, fmt.Errorf("%w: status %d", ErrUnavailable, res.StatusCode)
	default:
		// Mercado Pago's text is never passed on: only the code matters.
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "invalid_grant" {
			return Tokens{}, ErrInvalidGrant
		}
		return Tokens{}, fmt.Errorf("%w: status %d", ErrRejected, res.StatusCode)
	}
	var t struct {
		AccessToken  string          `json:"access_token"`
		RefreshToken string          `json:"refresh_token"`
		ExpiresIn    int64           `json:"expires_in"`
		Scope        string          `json:"scope"`
		UserID       json.RawMessage `json:"user_id"`
	}
	if err := json.Unmarshal(data, &t); err != nil || t.AccessToken == "" || t.ExpiresIn <= 0 {
		return Tokens{}, fmt.Errorf("%w: malformed token answer", ErrUnavailable)
	}
	return Tokens{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken,
		ExpiresIn: time.Duration(t.ExpiresIn) * time.Second, Scope: t.Scope, UserID: userID(t.UserID)}, nil
}

// userID accepts the number Mercado Pago sends, or a string.
func userID(raw json.RawMessage) string {
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return strconv.FormatInt(n, 10)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}
