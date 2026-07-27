// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/blackforge/embookshelf/internal/crypto"
	"github.com/blackforge/embookshelf/internal/db"
)

// ProviderSetting is the per-provider row shape. Config is opaque
// JSON — providers own their own schema (apiKey, language, cookie,
// …) — and is always plaintext in this struct: the repo decrypts on
// the way out and encrypts on the way in. Priority orders the
// provider-chain walks; nil means "fall back to catalog order". The
// Last* fields are health telemetry written by the enrichment service
// on each Search call so admins can spot stale tokens / broken
// scrapers.
type ProviderSetting struct {
	ID            string
	Enabled       bool
	Config        json.RawMessage
	Priority      *int
	UpdatedAt     time.Time
	LastSuccessAt *time.Time
	LastErrorAt   *time.Time
	LastError     string
}

// SecretKeyFunc reports which keys of a provider's config blob hold
// secrets. Provider config differs from a typed Setting only in how its
// secret slots are discovered: a Setting declares them as pointers to
// struct fields, a metadata provider declares them at runtime from its
// ConfigSchema. Supplied at construction so the repo never imports the
// provider catalog.
type SecretKeyFunc func(id string) []string

// ProviderSettingsRepo stores per-provider admin state: enabled flag,
// config blob, ranking, health telemetry.
//
// It holds the Cipher and the slot-discovery function rather than taking
// them per call, for the same reason AppSettingsRepo does: encryption is
// a property of the row, so it belongs at the one seam every write
// crosses. Owning it a tier up in the service left SetConfig accepting
// whatever raw blob it was handed, which means a second caller — or a
// second accessor on this type — could store a secret in plaintext and
// nothing would catch it (ADR-0010 §4).
type ProviderSettingsRepo struct {
	db         *db.DB
	cipher     crypto.Cipher
	secretKeys SecretKeyFunc
}

// NewProviderSettingsRepo wires the storage seam. A nil cipher falls
// back to Noop, matching the dev-mode boot semantics. A nil secretKeys
// declares "no config key on this instance is a secret" — the same
// opt-out a Setting takes by omitting Secrets.
func NewProviderSettingsRepo(d *db.DB, cipher crypto.Cipher, secretKeys SecretKeyFunc) *ProviderSettingsRepo {
	if cipher == nil {
		cipher = crypto.Noop{}
	}
	if secretKeys == nil {
		secretKeys = func(string) []string { return nil }
	}
	return &ProviderSettingsRepo{db: d, cipher: cipher, secretKeys: secretKeys}
}

// transformConfig applies op to the config keys this provider declares
// secret, leaving every other key verbatim so the stored row is still
// legible in psql (ADR-0010 §1). Blobs with no secret slots are returned
// unchanged rather than re-marshalled, so a passthrough never reorders
// an operator's JSON.
//
// A decrypt failure is returned, not swallowed. The previous behaviour —
// hand the caller the raw blob so the admin panel still renders — put
// ciphertext into a password input, and the next Save re-encrypted it,
// destroying the last recoverable copy of the secret. Failing is also
// what Setting[T].Get does, and the whole point of this seam is that the
// two behave alike.
func (r *ProviderSettingsRepo) transformConfig(
	id string, cfg json.RawMessage, op func(string) (string, error),
) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(cfg)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("{}")) {
		return cfg, nil
	}
	keys := r.secretKeys(id)
	if len(keys) == 0 {
		return cfg, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(cfg, &obj); err != nil {
		return nil, err
	}
	present := make([]string, 0, len(keys))
	values := make([]string, 0, len(keys))
	for _, k := range keys {
		s, ok := obj[k].(string)
		if !ok {
			continue
		}
		present = append(present, k)
		values = append(values, s)
	}
	if len(present) == 0 {
		return cfg, nil
	}
	// The same slot transformer the typed settings rows use: all-or-
	// nothing, so a half-encrypted config never reaches the database.
	slots := make([]*string, len(values))
	for i := range values {
		slots[i] = &values[i]
	}
	if err := crypto.TransformSlots(op, slots); err != nil {
		return nil, err
	}
	for i, k := range present {
		obj[k] = values[i]
	}
	return json.Marshal(obj)
}

// List returns every row with config + priority + health, secrets
// already decrypted. Order is by priority ASC (NULLs last) then id —
// matches the chain-walk behavior callers actually want for ISBN lookup
// and future bookdrop auto-enrich.
func (r *ProviderSettingsRepo) List(ctx context.Context) ([]ProviderSetting, error) {
	rows, err := r.db.SQL.QueryContext(ctx, `
		SELECT id, enabled, config, priority, updated_at,
		       last_success_at, last_error_at, last_error
		FROM provider_settings
		ORDER BY priority ASC NULLS LAST, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]ProviderSetting, 0)
	for rows.Next() {
		s, err := r.scanProviderSetting(rows)
		if err != nil {
			return nil, err
		}
		// Decrypt here rather than at the call site: List is read by both
		// the admin surface and the ISBN chain walk, and the latter's
		// happening to touch only Enabled/Priority is what let the missing
		// decrypt go unnoticed.
		cfg, err := r.transformConfig(s.ID, s.Config, r.cipher.Decrypt)
		if err != nil {
			return nil, fmt.Errorf("decrypt %s config: %w", s.ID, err)
		}
		s.Config = cfg
		out = append(out, s)
	}
	return out, rows.Err()
}

// AllConfigs returns id → config for every known provider, secrets
// decrypted. Used on service boot to push stored configs into provider
// instances, which per ADR-0010 §4 must only ever see plaintext.
func (r *ProviderSettingsRepo) AllConfigs(ctx context.Context) (map[string]json.RawMessage, error) {
	rows, err := r.db.SQL.QueryContext(ctx, `SELECT id, config FROM provider_settings`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]json.RawMessage)
	for rows.Next() {
		var id string
		var cfg []byte
		if err := rows.Scan(&id, &cfg); err != nil {
			return nil, err
		}
		decoded, err := r.transformConfig(id, json.RawMessage(cfg), r.cipher.Decrypt)
		if err != nil {
			return nil, fmt.Errorf("decrypt %s config: %w", id, err)
		}
		out[id] = decoded
	}
	return out, rows.Err()
}

// EnabledIDs is the hot-path helper — returns id → enabled so the
// enrichment service can filter its provider fan-out in one query.
func (r *ProviderSettingsRepo) EnabledIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := r.db.SQL.QueryContext(ctx, `SELECT id, enabled FROM provider_settings`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]bool)
	for rows.Next() {
		var id string
		var enabled bool
		if err := rows.Scan(&id, &enabled); err != nil {
			return nil, err
		}
		out[id] = enabled
	}
	return out, rows.Err()
}

// SetEnabled upserts a single row. Used by the PATCH endpoint when an
// admin toggles a provider.
func (r *ProviderSettingsRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	const q = `
		INSERT INTO provider_settings (id, enabled, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (id) DO UPDATE
		SET enabled = EXCLUDED.enabled,
		    updated_at = now()
	`
	_, err := r.db.SQL.ExecContext(ctx, q, id, enabled)
	return err
}

// SetConfig stores JSON config for a provider, encrypting the keys the
// provider declares secret. Treats the row as the source of truth — no
// merge semantics, the caller sends the full replacement plaintext blob.
func (r *ProviderSettingsRepo) SetConfig(ctx context.Context, id string, config json.RawMessage) error {
	if len(config) == 0 {
		config = []byte("{}")
	}
	config, err := r.transformConfig(id, config, r.cipher.Encrypt)
	if err != nil {
		return fmt.Errorf("encrypt %s config: %w", id, err)
	}
	const q = `
		INSERT INTO provider_settings (id, enabled, config, updated_at)
		VALUES ($1, false, $2, now())
		ON CONFLICT (id) DO UPDATE
		SET config = EXCLUDED.config,
		    updated_at = now()
	`
	_, err = r.db.SQL.ExecContext(ctx, q, id, config)
	return err
}

// RecordSuccess stamps last_success_at = now() and clears the last
// error. Called by the enrichment service from within a goroutine
// after a provider returns — writes are best-effort; caller logs
// errors but never aborts on them.
func (r *ProviderSettingsRepo) RecordSuccess(ctx context.Context, id string) error {
	const q = `
		INSERT INTO provider_settings (id, enabled, last_success_at, last_error, updated_at)
		VALUES ($1, false, now(), '', now())
		ON CONFLICT (id) DO UPDATE
		SET last_success_at = now(),
		    last_error = ''
	`
	_, err := r.db.SQL.ExecContext(ctx, q, id)
	return err
}

// RecordError stamps last_error_at = now() and stores the error string
// (truncated to 500 chars to keep the row small).
func (r *ProviderSettingsRepo) RecordError(ctx context.Context, id, msg string) error {
	if len(msg) > 500 {
		msg = msg[:500]
	}
	const q = `
		INSERT INTO provider_settings (id, enabled, last_error_at, last_error, updated_at)
		VALUES ($1, false, now(), $2, now())
		ON CONFLICT (id) DO UPDATE
		SET last_error_at = now(),
		    last_error = EXCLUDED.last_error
	`
	_, err := r.db.SQL.ExecContext(ctx, q, id, msg)
	return err
}

// SetPriority stores the sort priority. nil clears the slot (reverts
// the provider to catalog-order fallback).
func (r *ProviderSettingsRepo) SetPriority(ctx context.Context, id string, priority *int) error {
	const q = `
		INSERT INTO provider_settings (id, enabled, priority, updated_at)
		VALUES ($1, false, $2, now())
		ON CONFLICT (id) DO UPDATE
		SET priority = EXCLUDED.priority,
		    updated_at = now()
	`
	_, err := r.db.SQL.ExecContext(ctx, q, id, priority)
	return err
}

// SeedIfAbsent inserts any missing rows using the supplied defaults
// (derived from the provider catalog). Existing rows are left untouched
// — after first boot the DB is authoritative and restarts don't clobber
// admin toggles.
func (r *ProviderSettingsRepo) SeedIfAbsent(ctx context.Context, defaults map[string]bool) error {
	const q = `
		INSERT INTO provider_settings (id, enabled)
		VALUES ($1, $2)
		ON CONFLICT (id) DO NOTHING
	`
	for id, enabled := range defaults {
		if _, err := r.db.SQL.ExecContext(ctx, q, id, enabled); err != nil {
			return err
		}
	}
	return nil
}

func (r *ProviderSettingsRepo) scanProviderSetting(s scanner) (ProviderSetting, error) {
	var (
		ps             ProviderSetting
		cfg            []byte
		updatedAny     any
		lastSuccessAny any
		lastErrorAtAny any
	)
	if err := s.Scan(
		&ps.ID, &ps.Enabled, &cfg, &ps.Priority, &updatedAny,
		&lastSuccessAny, &lastErrorAtAny, &ps.LastError,
	); err != nil {
		return ps, err
	}
	ps.Config = json.RawMessage(cfg)
	if err := db.ScanTime(updatedAny, &ps.UpdatedAt); err != nil {
		return ps, fmt.Errorf("scan updated_at: %w", err)
	}
	if err := db.ScanNullTime(lastSuccessAny, &ps.LastSuccessAt); err != nil {
		return ps, fmt.Errorf("scan last_success_at: %w", err)
	}
	if err := db.ScanNullTime(lastErrorAtAny, &ps.LastErrorAt); err != nil {
		return ps, fmt.Errorf("scan last_error_at: %w", err)
	}
	return ps, nil
}
