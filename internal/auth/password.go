package auth

import (
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// HashPassword returns a bcrypt hash compatible with pgcrypto's crypt(... 'bf').
func HashPassword(raw string) (string, error) {
	if len(raw) < 8 {
		return "", ErrWeakPassword
	}
	h, err := bcrypt.GenerateFromPassword([]byte(raw), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// VerifyPassword returns nil when the raw password matches the stored hash.
// It accepts both $2a$ and $2b$ hashes, including hashes produced by
// Postgres' pgcrypto (which we use to seed the dev admin).
func VerifyPassword(hash, raw string) error {
	// pgcrypto sometimes returns the hash with a trailing newline when used
	// inside larger SELECTs; normalize defensively.
	hash = strings.TrimSpace(hash)
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(raw))
}

// ErrWeakPassword is returned when a submitted password fails the local policy.
var ErrWeakPassword = errors.New("password must be at least 8 characters")
