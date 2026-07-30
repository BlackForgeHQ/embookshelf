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
	row, err := s.Prepare(r, v)
	if err != nil {
		return err
	}
	return r.SetRaw(ctx, row.Name, row.Value)
}

// Prepare does everything Set does except the write: normalize,
// validate, encrypt, marshal.
//
// Split out so several settings can be written in one transaction. The
// OIDC settings submission is five rows, and applying four of them
// before a fifth is refused leaves an instance configured half the way
// an admin asked for (#195).
func (s Setting[T]) Prepare(r *AppSettingsRepo, v T) (SettingRow, error) {
	if s.Normalize != nil {
		v = s.Normalize(v)
	}
	if s.Validate != nil {
		if err := s.Validate(v); err != nil {
			return SettingRow{}, err
		}
	}
	if s.Secrets != nil {
		if err := crypto.TransformSlots(r.cipher.Encrypt, s.Secrets(&v)); err != nil {
			return SettingRow{}, fmt.Errorf("encrypt %s secrets: %w", s.Key, err)
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return SettingRow{}, err
	}
	return SettingRow{Name: s.Key, Value: b}, nil
}

// SettingRow is one prepared app_settings row: the key, and the JSON
// that is ready to land in it.
type SettingRow struct {
	Name  string
	Value json.RawMessage
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

// settingSeeder is one row's boot-time seed, with Setting[T]'s type
// parameter erased so rows of different value types share one list.
type settingSeeder struct {
	key  string
	seed func(context.Context) error
}

// seedRow builds a registry entry from a declaration. The key travels
// with the seed step, so an entry cannot name one row and seed another.
func seedRow[T any](r *AppSettingsRepo, s Setting[T]) settingSeeder {
	return settingSeeder{
		key:  s.Key,
		seed: func(ctx context.Context) error { return s.SeedIfAbsent(ctx, r) },
	}
}
