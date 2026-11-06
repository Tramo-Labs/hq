// Package crypto implements envelope encryption for data at rest.
//
// Every tenant has a data key (AES-256-GCM) stored in tenant_keys wrapped by
// the master key. The master key never touches the database: it comes from
// the MASTER_KEY environment variable (dev) or a KMS or sealed file (prod).
// Ciphertexts are bound to their tenant and to a caller supplied context
// (for example "erp_credentials:<id>") through the AEAD additional data, so
// a blob copied to another row or tenant fails to decrypt.
//
// Blob format: 1 byte format (1) | 4 bytes key version | 12 bytes nonce | sealed.
package crypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	keySize    = 32
	formatV1   = 1
	headerSize = 1 + 4
)

// ErrDecrypt is returned for any blob that does not authenticate.
var ErrDecrypt = errors.New("crypto: cannot decrypt")

// Keyring holds the master key and wraps tenant data keys with it.
type Keyring struct {
	master cipher.AEAD
}

// NewKeyring builds a keyring from a 32 byte master key.
func NewKeyring(masterKey []byte) (*Keyring, error) {
	if len(masterKey) != keySize {
		return nil, fmt.Errorf("crypto: master key must be %d bytes, got %d", keySize, len(masterKey))
	}
	aead, err := newAEAD(masterKey)
	if err != nil {
		return nil, err
	}
	return &Keyring{master: aead}, nil
}

// Seal encrypts plaintext with key; aad is authenticated but not encrypted.
func Seal(key, plaintext, aad []byte) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	return seal(aead, 0, plaintext, aad)
}

// Open reverses Seal.
func Open(key, blob, aad []byte) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	_, pt, err := open(aead, blob, aad)
	return pt, err
}

// EnsureTenantKey creates version 1 of the tenant data key if missing. It
// writes tenant_keys, so it runs in a system transaction (tenant creation).
func (k *Keyring) EnsureTenantKey(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) error {
	dek := make([]byte, keySize)
	if _, err := rand.Read(dek); err != nil {
		return err
	}
	wrapped, err := seal(k.master, 1, dek, tenant[:])
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`insert into tenant_keys (tenant_id, version, wrapped_key) values ($1, 1, $2) on conflict do nothing`,
		tenant, wrapped)
	return err
}

// Encrypt seals plaintext with the newest data key of the tenant. tx may be
// a tenant transaction: tramo_app can read its own tenant keys.
func (k *Keyring) Encrypt(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, plaintext []byte, purpose string) ([]byte, error) {
	var version uint32
	var wrapped []byte
	err := tx.QueryRow(ctx,
		`select version, wrapped_key from tenant_keys where tenant_id = $1 order by version desc limit 1`,
		tenant).Scan(&version, &wrapped)
	if err != nil {
		return nil, fmt.Errorf("crypto: tenant key: %w", err)
	}
	aead, err := k.unwrap(tenant, wrapped)
	if err != nil {
		return nil, err
	}
	return seal(aead, version, plaintext, aad(tenant, purpose))
}

// Decrypt opens a blob produced by Encrypt for the same tenant and purpose.
func (k *Keyring) Decrypt(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, blob []byte, purpose string) ([]byte, error) {
	if len(blob) < headerSize || blob[0] != formatV1 {
		return nil, ErrDecrypt
	}
	version := binary.BigEndian.Uint32(blob[1:headerSize])
	var wrapped []byte
	err := tx.QueryRow(ctx,
		`select wrapped_key from tenant_keys where tenant_id = $1 and version = $2`,
		tenant, int64(version)).Scan(&wrapped)
	if err != nil {
		return nil, ErrDecrypt
	}
	aead, err := k.unwrap(tenant, wrapped)
	if err != nil {
		return nil, err
	}
	_, pt, err := open(aead, blob, aad(tenant, purpose))
	return pt, err
}

func (k *Keyring) unwrap(tenant uuid.UUID, wrapped []byte) (cipher.AEAD, error) {
	_, dek, err := open(k.master, wrapped, tenant[:])
	if err != nil {
		return nil, err
	}
	return newAEAD(dek)
}

func aad(tenant uuid.UUID, purpose string) []byte {
	return append(tenant[:], []byte(purpose)...)
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("crypto: key must be %d bytes", keySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// seal writes header | nonce | ciphertext. The header is authenticated too,
// so the key version cannot be swapped.
func seal(aead cipher.AEAD, version uint32, plaintext, aad []byte) ([]byte, error) {
	// The nonce is drawn into its own slice, not into the header buffer, so
	// it never shares backing memory with the fixed header bytes.
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, headerSize, headerSize+len(nonce)+len(plaintext)+aead.Overhead())
	out[0] = formatV1
	binary.BigEndian.PutUint32(out[1:headerSize], version)
	full := append(append([]byte{}, out...), aad...)
	out = append(out, nonce...)
	return aead.Seal(out, nonce, plaintext, full), nil
}

func open(aead cipher.AEAD, blob, aad []byte) (uint32, []byte, error) {
	ns := aead.NonceSize()
	if len(blob) < headerSize+ns+aead.Overhead() || blob[0] != formatV1 {
		return 0, nil, ErrDecrypt
	}
	version := binary.BigEndian.Uint32(blob[1:headerSize])
	nonce := blob[headerSize : headerSize+ns]
	full := append(append([]byte{}, blob[:headerSize]...), aad...)
	pt, err := aead.Open(nil, nonce, blob[headerSize+ns:], full)
	if err != nil {
		return 0, nil, ErrDecrypt
	}
	return version, pt, nil
}
