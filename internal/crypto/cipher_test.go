package crypto

import (
	"encoding/base64"
	"strings"
	"testing"
)

// testKey returns a deterministic 32-byte key for tests. Real deploys
// use a random key from EMBOOKSHELF_SECRET_KEY.
func testKey() string {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestAESGCMRoundTrip(t *testing.T) {
	c, err := NewAESGCM(testKey())
	if err != nil {
		t.Fatal(err)
	}
	for _, plain := range []string{"", "hc_abcdef", "AIzaSyA-very-long-google-api-key-xxxx", "🚀 unicode"} {
		ct, err := c.Encrypt(plain)
		if err != nil {
			t.Fatalf("Encrypt(%q): %v", plain, err)
		}
		if plain == "" {
			if ct != "" {
				t.Errorf("empty plaintext should produce empty ciphertext, got %q", ct)
			}
		} else if !strings.HasPrefix(ct, prefix) {
			t.Errorf("ciphertext missing prefix %q: %q", prefix, ct)
		}
		got, err := c.Decrypt(ct)
		if err != nil {
			t.Fatalf("Decrypt(%q): %v", ct, err)
		}
		if got != plain {
			t.Errorf("round-trip: want %q got %q", plain, got)
		}
	}
}

func TestAESGCMPlaintextPassthrough(t *testing.T) {
	// Pre-encryption plaintext rows should survive Decrypt unchanged so
	// the prefix-based rollout doesn't lose data.
	c, err := NewAESGCM(testKey())
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"AIzaLegacyPlainValue", "cookie=abc; token=xyz"} {
		got, err := c.Decrypt(v)
		if err != nil {
			t.Fatalf("Decrypt(%q): %v", v, err)
		}
		if got != v {
			t.Errorf("plaintext passthrough: want %q got %q", v, got)
		}
	}
}

func TestAESGCMFreshNonce(t *testing.T) {
	// GCM security collapses if two messages share (key, nonce). Each
	// Encrypt call must produce a distinct ciphertext for the same input.
	c, err := NewAESGCM(testKey())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := c.Encrypt("same-plaintext")
	b, _ := c.Encrypt("same-plaintext")
	if a == b {
		t.Fatal("repeated Encrypt produced identical ciphertexts — nonce reuse")
	}
}

func TestAESGCMBadKey(t *testing.T) {
	for _, bad := range []string{"", "not-base64!!", "dG9vLXNob3J0"} {
		if _, err := NewAESGCM(bad); err == nil {
			t.Errorf("expected error for key %q", bad)
		}
	}
}

func TestNoopPassthrough(t *testing.T) {
	var c Cipher = Noop{}
	ct, err := c.Encrypt("secret")
	if err != nil || ct != "secret" {
		t.Fatalf("noop encrypt: got %q err=%v", ct, err)
	}
	pt, err := c.Decrypt("secret")
	if err != nil || pt != "secret" {
		t.Fatalf("noop decrypt: got %q err=%v", pt, err)
	}
	if c.Enabled() {
		t.Fatal("noop should report disabled")
	}
}
