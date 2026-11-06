package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var fastArgon = Argon2Params{Memory: 8 * 1024, Time: 1, Threads: 1}

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword(fastArgon, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=8192,t=1,p=1$") || strings.Contains(h, "horse") {
		t.Fatalf("hash format: %s", h)
	}
	if ok, err := VerifyPassword(h, "correct horse battery"); !ok || err != nil {
		t.Fatalf("right password: %v %v", ok, err)
	}
	if ok, _ := VerifyPassword(h, "correct horse batterY"); ok {
		t.Fatal("wrong password accepted")
	}
	h2, _ := HashPassword(fastArgon, "correct horse battery")
	if h == h2 {
		t.Fatal("same salt twice")
	}
	// A tampered row must not make login allocate gigabytes.
	bomb := strings.Replace(h, "m=8192", "m=4194304", 1)
	if _, err := VerifyPassword(bomb, "x"); err == nil {
		t.Fatal("accepted a 4 GiB memory parameter")
	}
	for _, bad := range []string{"", "plain", "$argon2i$v=19$m=8,t=1,p=1$c2FsdA$a2V5", "$argon2id$v=18$m=8192,t=1,p=1$c2FsdA$a2V5"} {
		if _, err := VerifyPassword(bad, "x"); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestSignerRejectsForgedTokens(t *testing.T) {
	seed := make([]byte, 32)
	_, _ = rand.Read(seed)
	s, err := NewSigner(seed, "k1")
	if err != nil {
		t.Fatal(err)
	}
	p := Principal{UserID: uuid.New(), TenantID: uuid.New(), Role: RoleOwner}
	tok, err := s.Issue(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Verify(tok)
	if err != nil || got != p {
		t.Fatalf("verify own token: %+v %v", got, err)
	}

	// Ten minutes: expired after 10m30s (30s leeway), valid before.
	s.now = func() time.Time { return time.Now().Add(10*time.Minute + 31*time.Second) }
	if _, err := s.Verify(tok); err == nil {
		t.Fatal("expired token accepted")
	}
	s.now = func() time.Time { return time.Now().Add(9 * time.Minute) }
	if _, err := s.Verify(tok); err != nil {
		t.Fatalf("token rejected before expiry: %v", err)
	}
	s.now = time.Now

	// Another key, same kid.
	other := make([]byte, 32)
	_, _ = rand.Read(other)
	o, _ := NewSigner(other, "k1")
	if _, err := s.Verify(mustIssue(t, o, p)); err == nil {
		t.Fatal("token from another key accepted")
	}

	// Algorithm confusion: HS256 signed with the public key bytes, and none.
	pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	c := jwt.MapClaims{"iss": issuer, "aud": audience, "sub": p.UserID.String(), "tid": p.TenantID.String(),
		"role": "owner", "exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix()}
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	hs.Header["kid"] = "k1"
	hsTok, _ := hs.SignedString([]byte(pub))
	none := jwt.NewWithClaims(jwt.SigningMethodNone, c)
	none.Header["kid"] = "k1"
	noneTok, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	for name, tk := range map[string]string{"hs256": hsTok, "none": noneTok, "garbage": "a.b.c"} {
		if _, err := s.Verify(tk); err == nil {
			t.Errorf("%s token accepted", name)
		}
	}

	// Same key, another kid.
	k2, _ := NewSigner(seed, "k2")
	if _, err := s.Verify(mustIssue(t, k2, p)); err == nil {
		t.Fatal("token with another kid accepted")
	}
}

func mustIssue(t *testing.T, s *Signer, p Principal) string {
	t.Helper()
	tok, err := s.Issue(p)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestRefreshTokensAreOpaqueAndHashed(t *testing.T) {
	tok, hash, err := newRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 43 || len(hash) != 32 || string(hash) == tok {
		t.Fatalf("token %d chars, hash %d bytes", len(tok), len(hash))
	}
	if string(hashRefresh(tok)) != string(hash) {
		t.Fatal("hash not stable")
	}
}
