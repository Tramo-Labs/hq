package crypto_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/crypto"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

func key(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestSealOpen(t *testing.T) {
	k := key(t)
	blob, err := crypto.Seal(k, []byte("xubio-api-key"), []byte("ctx"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("xubio-api-key")) {
		t.Fatal("plaintext visible in blob")
	}
	pt, err := crypto.Open(k, blob, []byte("ctx"))
	if err != nil || string(pt) != "xubio-api-key" {
		t.Fatalf("open: %q, %v", pt, err)
	}
	for name, tc := range map[string]struct {
		key, aad []byte
		mutate   func([]byte)
	}{
		"other key":      {key(t), []byte("ctx"), nil},
		"other aad":      {k, []byte("other"), nil},
		"flipped byte":   {k, []byte("ctx"), func(b []byte) { b[len(b)-1] ^= 1 }},
		"flipped header": {k, []byte("ctx"), func(b []byte) { b[2] ^= 1 }},
	} {
		b := append([]byte{}, blob...)
		if tc.mutate != nil {
			tc.mutate(b)
		}
		if _, err := crypto.Open(tc.key, b, tc.aad); !errors.Is(err, crypto.ErrDecrypt) {
			t.Errorf("%s: got %v, want ErrDecrypt", name, err)
		}
	}
}

func TestNewKeyringRejectsShortKeys(t *testing.T) {
	if _, err := crypto.NewKeyring(make([]byte, 16)); err == nil {
		t.Fatal("accepted a 16 byte master key")
	}
}

func TestTenantKeysAreBoundToTenantAndPurpose(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	kr, err := crypto.NewKeyring(key(t))
	if err != nil {
		t.Fatal(err)
	}
	a, b := uuid.New(), uuid.New()
	err = d.System(ctx, func(tx pgx.Tx) error {
		for _, id := range []uuid.UUID{a, b} {
			if _, err := tx.Exec(ctx, `insert into tenants (id, slug, name) values ($1, $2, 'x')`, id, id.String()); err != nil {
				return err
			}
			if err := kr.EnsureTenantKey(ctx, tx, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Encrypt inside a tenant transaction, as a business module would.
	var blob []byte
	actx := db.WithScope(ctx, db.Scope{TenantID: a})
	err = d.InTenant(actx, func(tx pgx.Tx) error {
		blob, err = kr.Encrypt(actx, tx, a, []byte("oauth-token"), "mp:1")
		if err != nil {
			return err
		}
		pt, err := kr.Decrypt(actx, tx, a, blob, "mp:1")
		if err != nil || string(pt) != "oauth-token" {
			t.Errorf("round trip: %q, %v", pt, err)
		}
		if _, err := kr.Decrypt(actx, tx, a, blob, "mp:2"); !errors.Is(err, crypto.ErrDecrypt) {
			t.Errorf("other purpose: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// The same blob read by tenant b: its key is not visible and would not fit.
	bctx := db.WithScope(ctx, db.Scope{TenantID: b})
	err = d.InTenant(bctx, func(tx pgx.Tx) error {
		if _, err := kr.Decrypt(bctx, tx, a, blob, "mp:1"); !errors.Is(err, crypto.ErrDecrypt) {
			t.Errorf("tenant b opened tenant a blob via a's id: %v", err)
		}
		if _, err := kr.Decrypt(bctx, tx, b, blob, "mp:1"); !errors.Is(err, crypto.ErrDecrypt) {
			t.Errorf("tenant b opened tenant a blob with its own key: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// A different master key cannot unwrap the stored data keys.
	other, _ := crypto.NewKeyring(key(t))
	err = d.InTenant(actx, func(tx pgx.Tx) error {
		if _, err := other.Decrypt(actx, tx, a, blob, "mp:1"); !errors.Is(err, crypto.ErrDecrypt) {
			t.Errorf("other master key: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
