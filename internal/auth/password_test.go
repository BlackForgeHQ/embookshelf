package auth

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashPasswordRejectsShort(t *testing.T) {
	cases := []string{"", "1", "1234567"}
	for _, raw := range cases {
		if _, err := HashPassword(raw); !errors.Is(err, ErrWeakPassword) {
			t.Errorf("HashPassword(%q) err = %v, want ErrWeakPassword", raw, err)
		}
	}
}

func TestHashPasswordReturnsBcryptHash(t *testing.T) {
	hash, err := HashPassword("hunter2!!")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$2a$") && !strings.HasPrefix(hash, "$2b$") {
		t.Fatalf("hash prefix unexpected: %q", hash)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("hunter2!!")); err != nil {
		t.Fatalf("hash does not verify: %v", err)
	}
}

func TestVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	if err := VerifyPassword(hash, "correct-horse-battery"); err != nil {
		t.Errorf("VerifyPassword valid: %v", err)
	}
	if err := VerifyPassword(hash, "wrong"); err == nil {
		t.Error("VerifyPassword wrong: want error, got nil")
	}
}

func TestVerifyPasswordTrimsWhitespace(t *testing.T) {
	hash, err := HashPassword("password123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	padded := "  " + hash + "\n"
	if err := VerifyPassword(padded, "password123"); err != nil {
		t.Fatalf("VerifyPassword with surrounding whitespace: %v", err)
	}
}
