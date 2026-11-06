package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	// AccessTTL is the life of an access token (eng/ARQUITECTURA.md section 5).
	AccessTTL = 10 * time.Minute
	// RefreshTTL is the life of a refresh token.
	RefreshTTL = 30 * 24 * time.Hour

	issuer   = "tramo-core"
	audience = "tramo-api"
)

// Principal is the authenticated caller of a request.
type Principal struct {
	UserID   uuid.UUID
	TenantID uuid.UUID
	Role     string
}

type claims struct {
	jwt.RegisteredClaims
	TenantID string `json:"tid"`
	Role     string `json:"role"`
}

// Signer issues and verifies access tokens. The private key comes from
// JWT_SIGNING_KEY (an ed25519 seed); it is never stored in the database.
type Signer struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
	kid  string
	now  func() time.Time
}

// NewSigner builds a signer from a 32 byte seed.
func NewSigner(seed []byte, kid string) (*Signer, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("identity: jwt seed must be %d bytes", ed25519.SeedSize)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return &Signer{priv: priv, pub: priv.Public().(ed25519.PublicKey), kid: kid, now: time.Now}, nil
}

// Issue signs an access token for user acting on tenant with role.
func (s *Signer) Issue(p Principal) (string, error) {
	now := s.now()
	c := claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audience},
			Subject:   p.UserID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTTL)),
			ID:        uuid.NewString(),
		},
		TenantID: p.TenantID.String(),
		Role:     p.Role,
	}
	t := jwt.NewWithClaims(jwt.SigningMethodEdDSA, c)
	t.Header["kid"] = s.kid
	return t.SignedString(s.priv)
}

var errToken = errors.New("identity: invalid access token")

// Verify checks signature, algorithm, issuer, audience and expiry.
func (s *Signer) Verify(token string) (Principal, error) {
	var c claims
	_, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
		if kid, _ := t.Header["kid"].(string); kid != s.kid {
			return nil, errToken
		}
		return s.pub, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(30*time.Second),
		jwt.WithTimeFunc(s.now),
	)
	if err != nil {
		return Principal{}, errToken
	}
	user, err1 := uuid.Parse(c.Subject)
	tenant, err2 := uuid.Parse(c.TenantID)
	if err1 != nil || err2 != nil || c.Role == "" {
		return Principal{}, errToken
	}
	return Principal{UserID: user, TenantID: tenant, Role: c.Role}, nil
}

// newRefreshToken returns an opaque 256 bit token and the SHA-256 that is
// the only thing stored.
func newRefreshToken() (string, []byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	return tok, hashRefresh(tok), nil
}

func hashRefresh(tok string) []byte {
	h := sha256.Sum256([]byte(tok))
	return h[:]
}
