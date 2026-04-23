// Package crypto handles at-rest encryption for sensitive settings
// (API keys, cookies, OIDC client secrets). It exposes a single
// Cipher interface with two implementations:
//
//   - AESGCM: real encryption, keyed by a 32-byte blob from
//     EMBOOKSHELF_SECRET_KEY (base64-encoded).
//   - Noop:   pass-through. Used in dev when no KEK is configured so
//     the app still boots, with a startup warning.
//
// Ciphertexts carry a version tag (enc:v1:) so Decrypt can recognize
// pre-existing plaintext values from older rows and pass them through
// verbatim. This lets us roll out encryption without a migration — the
// next SetConfig write encrypts; reads keep working either way.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Cipher is the tiny interface used by callers. Encrypt/Decrypt
// operate on strings — binary payloads are up to the caller.
type Cipher interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(stored string) (string, error)
	// Enabled reports whether real encryption is in effect; admins can
	// surface a "secrets stored in plaintext" warning when false.
	Enabled() bool
}

// prefix is prepended to every ciphertext so Decrypt can distinguish
// pre-encryption plaintext from post-encryption blobs. Version is
// baked in so we can rotate algorithms later without losing rows.
const prefix = "enc:v1:"

// ErrBadKey is returned when EMBOOKSHELF_SECRET_KEY is set but
// unparseable. Callers should fail fast on this — silently falling
// back to Noop would let admins think secrets are encrypted when
// they aren't.
var ErrBadKey = errors.New("secret key must be base64-encoded 32 bytes (AES-256)")

// AESGCM is the default Cipher implementation.
type AESGCM struct {
	aead cipher.AEAD
}

// NewAESGCM constructs an AES-256-GCM cipher from a base64-encoded
// 32-byte key. Returns ErrBadKey for any length mismatch or decode
// failure.
func NewAESGCM(b64Key string) (*AESGCM, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64Key))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadKey, err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("%w: got %d bytes", ErrBadKey, len(raw))
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &AESGCM{aead: aead}, nil
}

func (c *AESGCM) Enabled() bool { return true }

func (c *AESGCM) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	// Fresh nonce per call — required for GCM safety. 12 bytes is the
	// standard nonce size; we prepend it to the ciphertext so Decrypt
	// can recover it without a separate slot.
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return prefix + base64.StdEncoding.EncodeToString(sealed), nil
}

func (c *AESGCM) Decrypt(stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	// Rows written before encryption was turned on don't carry the
	// prefix — treat them as plaintext passthrough. The next write will
	// upgrade them.
	if !strings.HasPrefix(stored, prefix) {
		return stored, nil
	}
	raw, err := base64.StdEncoding.DecodeString(stored[len(prefix):])
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	if len(raw) < c.aead.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce := raw[:c.aead.NonceSize()]
	body := raw[c.aead.NonceSize():]
	plain, err := c.aead.Open(nil, nonce, body, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plain), nil
}

// Noop is the fallback Cipher for dev / pre-upgrade instances. Values
// round-trip unchanged; callers should log a warning when constructing
// this so admins know secrets are plaintext.
type Noop struct{}

func (Noop) Enabled() bool                            { return false }
func (Noop) Encrypt(plaintext string) (string, error) { return plaintext, nil }
func (Noop) Decrypt(stored string) (string, error)    { return stored, nil }
