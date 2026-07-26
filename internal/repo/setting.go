// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/blackforge/embookshelf/internal/crypto"
)

// Setting declares one typed app_settings row. Get / Set / SeedIfAbsent
// are implemented once here; a domain supplies only what actually
// differs between rows — its key, its defaults, and optionally how to
// normalize it, validate it, and where its secrets live.
//
// Declaring Secrets is how a row opts into at-rest encryption. It
// returns pointers to the secret string fields of the value; the
// implementation runs them through the Cipher on every write and
// reverses it on every read, so callers only ever see plaintext and no
// domain can forget to encrypt. Rows written before encryption was
// wired carry no version prefix and pass through unchanged — the next
// Set upgrades them (ADR-0010).
type Setting[T any] struct {
	// Key names the app_settings row.
	Key string
	// Default supplies the value used when the row is absent, and the
	// base a stored row is unmarshaled onto so partial JSON keeps its
	// defaults.
	Default func() T
	// Normalize trims and coerces before validation and storage. Optional.
	Normalize func(T) T
	// Validate refuses a bad value at save time. Optional.
	Validate func(T) error
	// Secrets enumerates the fields encrypted at rest. Optional — a row
	// without secrets omits it.
	Secrets func(*T) []*string
}

// Get loads the row, applying defaults for a missing row and decrypting
// any declared secrets. A missing row is not an error — callers treat
// "never configured" and "configured with defaults" identically.
func (s Setting[T]) Get(ctx context.Context, r *AppSettingsRepo) (T, error) {
	var zero T
	raw, err := r.GetRaw(ctx, s.Key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return s.Default(), nil
		}
		return zero, err
	}
	v := s.Default()
	if err := json.Unmarshal(raw, &v); err != nil {
		return zero, fmt.Errorf("unmarshal %s setting: %w", s.Key, err)
	}
	if s.Secrets != nil {
		if err := crypto.TransformSlots(r.cipher.Decrypt, s.Secrets(&v)); err != nil {
			return zero, fmt.Errorf("decrypt %s secrets: %w", s.Key, err)
		}
	}
	return v, nil
}

// Set normalizes, validates, encrypts declared secrets, and upserts the
// row. The caller's value is untouched — encryption happens on a copy.
func (s Setting[T]) Set(ctx context.Context, r *AppSettingsRepo, v T) error {
	if s.Normalize != nil {
		v = s.Normalize(v)
	}
	if s.Validate != nil {
		if err := s.Validate(v); err != nil {
			return err
		}
	}
	if s.Secrets != nil {
		if err := crypto.TransformSlots(r.cipher.Encrypt, s.Secrets(&v)); err != nil {
			return fmt.Errorf("encrypt %s secrets: %w", s.Key, err)
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return r.SetRaw(ctx, s.Key, b)
}

// SeedIfAbsent writes the default row when none exists, so the admin
// settings UI has something to render on first boot. Existing rows —
// including admin-edited ones — are left alone.
func (s Setting[T]) SeedIfAbsent(ctx context.Context, r *AppSettingsRepo) error {
	if _, err := r.GetRaw(ctx, s.Key); err == nil {
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	return s.Set(ctx, r, s.Default())
}
