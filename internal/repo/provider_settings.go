package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/blackforge/embookshelf/internal/db"
)

// ProviderSetting is the per-provider row shape. Config is opaque
// JSON — providers own their own schema (apiKey, language, cookie,
// …). Priority orders the provider-chain walks; nil means
// "fall back to catalog order". The Last* fields are health
// telemetry written by the enrichment service on each Search call so
// admins can spot stale tokens / broken scrapers.
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

type ProviderSettingsRepo struct {
	db *db.DB
}

func NewProviderSettingsRepo(d *db.DB) *ProviderSettingsRepo {
	return &ProviderSettingsRepo{db: d}
}

// List returns every row with config + priority + health. Order is by
// priority ASC (NULLs last) then id — matches the chain-walk behavior
// callers actually want for ISBN lookup and future bookdrop auto-enrich.
func (r *ProviderSettingsRepo) List(ctx context.Context) ([]ProviderSetting, error) {
	// NULLS LAST is supported by SQLite as well.
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
		out = append(out, s)
	}
	return out, rows.Err()
}

// AllConfigs returns id → config for every known provider. Used on
// service boot to push stored configs into provider instances.
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
		out[id] = json.RawMessage(cfg)
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
	const qPG = `
		INSERT INTO provider_settings (id, enabled, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (id) DO UPDATE
		SET enabled = EXCLUDED.enabled,
		    updated_at = now()
	`
	const qSQLite = `
		INSERT INTO provider_settings (id, enabled, updated_at)
		VALUES (?, ?, (strftime('%Y-%m-%dT%H:%M:%fZ','now')))
		ON CONFLICT (id) DO UPDATE
		SET enabled = EXCLUDED.enabled,
		    updated_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	`
	_, err := r.db.SQL.ExecContext(ctx,
		db.SelectQ(r.db.Dialect, qPG, qSQLite),
		id, enabled)
	return err
}

// SetConfig stores opaque JSON config for a provider. Treats the row
// as the source of truth — no merge semantics, the caller sends the
// full replacement blob.
func (r *ProviderSettingsRepo) SetConfig(ctx context.Context, id string, config json.RawMessage) error {
	if len(config) == 0 {
		config = []byte("{}")
	}
	const qPG = `
		INSERT INTO provider_settings (id, enabled, config, updated_at)
		VALUES ($1, false, $2, now())
		ON CONFLICT (id) DO UPDATE
		SET config = EXCLUDED.config,
		    updated_at = now()
	`
	const qSQLite = `
		INSERT INTO provider_settings (id, enabled, config, updated_at)
		VALUES (?, 0, ?, (strftime('%Y-%m-%dT%H:%M:%fZ','now')))
		ON CONFLICT (id) DO UPDATE
		SET config = EXCLUDED.config,
		    updated_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	`
	_, err := r.db.SQL.ExecContext(ctx,
		db.SelectQ(r.db.Dialect, qPG, qSQLite),
		id, config)
	return err
}

// RecordSuccess stamps last_success_at = now() and clears the last
// error. Called by the enrichment service from within a goroutine
// after a provider returns — writes are best-effort; caller logs
// errors but never aborts on them.
func (r *ProviderSettingsRepo) RecordSuccess(ctx context.Context, id string) error {
	const qPG = `
		INSERT INTO provider_settings (id, enabled, last_success_at, last_error, updated_at)
		VALUES ($1, false, now(), '', now())
		ON CONFLICT (id) DO UPDATE
		SET last_success_at = now(),
		    last_error = ''
	`
	const qSQLite = `
		INSERT INTO provider_settings (id, enabled, last_success_at, last_error, updated_at)
		VALUES (?, 0, (strftime('%Y-%m-%dT%H:%M:%fZ','now')), '', (strftime('%Y-%m-%dT%H:%M:%fZ','now')))
		ON CONFLICT (id) DO UPDATE
		SET last_success_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		    last_error = ''
	`
	_, err := r.db.SQL.ExecContext(ctx,
		db.SelectQ(r.db.Dialect, qPG, qSQLite),
		id)
	return err
}

// RecordError stamps last_error_at = now() and stores the error string
// (truncated to 500 chars to keep the row small).
func (r *ProviderSettingsRepo) RecordError(ctx context.Context, id, msg string) error {
	if len(msg) > 500 {
		msg = msg[:500]
	}
	const qPG = `
		INSERT INTO provider_settings (id, enabled, last_error_at, last_error, updated_at)
		VALUES ($1, false, now(), $2, now())
		ON CONFLICT (id) DO UPDATE
		SET last_error_at = now(),
		    last_error = EXCLUDED.last_error
	`
	const qSQLite = `
		INSERT INTO provider_settings (id, enabled, last_error_at, last_error, updated_at)
		VALUES (?, 0, (strftime('%Y-%m-%dT%H:%M:%fZ','now')), ?, (strftime('%Y-%m-%dT%H:%M:%fZ','now')))
		ON CONFLICT (id) DO UPDATE
		SET last_error_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		    last_error = EXCLUDED.last_error
	`
	_, err := r.db.SQL.ExecContext(ctx,
		db.SelectQ(r.db.Dialect, qPG, qSQLite),
		id, msg)
	return err
}

// SetPriority stores the sort priority. nil clears the slot (reverts
// the provider to catalog-order fallback).
func (r *ProviderSettingsRepo) SetPriority(ctx context.Context, id string, priority *int) error {
	const qPG = `
		INSERT INTO provider_settings (id, enabled, priority, updated_at)
		VALUES ($1, false, $2, now())
		ON CONFLICT (id) DO UPDATE
		SET priority = EXCLUDED.priority,
		    updated_at = now()
	`
	const qSQLite = `
		INSERT INTO provider_settings (id, enabled, priority, updated_at)
		VALUES (?, 0, ?, (strftime('%Y-%m-%dT%H:%M:%fZ','now')))
		ON CONFLICT (id) DO UPDATE
		SET priority = EXCLUDED.priority,
		    updated_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	`
	_, err := r.db.SQL.ExecContext(ctx,
		db.SelectQ(r.db.Dialect, qPG, qSQLite),
		id, priority)
	return err
}

// SeedIfAbsent inserts any missing rows using the supplied defaults
// (derived from the provider catalog). Existing rows are left untouched
// — after first boot the DB is authoritative and restarts don't clobber
// admin toggles.
func (r *ProviderSettingsRepo) SeedIfAbsent(ctx context.Context, defaults map[string]bool) error {
	const qPG = `
		INSERT INTO provider_settings (id, enabled)
		VALUES ($1, $2)
		ON CONFLICT (id) DO NOTHING
	`
	const qSQLite = `
		INSERT INTO provider_settings (id, enabled)
		VALUES (?, ?)
		ON CONFLICT (id) DO NOTHING
	`
	for id, enabled := range defaults {
		if _, err := r.db.SQL.ExecContext(ctx,
			db.SelectQ(r.db.Dialect, qPG, qSQLite),
			id, enabled); err != nil {
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
	if err := db.ScanTime(r.db.Dialect, updatedAny, &ps.UpdatedAt); err != nil {
		return ps, fmt.Errorf("scan updated_at: %w", err)
	}
	if err := db.ScanNullTime(r.db.Dialect, lastSuccessAny, &ps.LastSuccessAt); err != nil {
		return ps, fmt.Errorf("scan last_success_at: %w", err)
	}
	if err := db.ScanNullTime(r.db.Dialect, lastErrorAtAny, &ps.LastErrorAt); err != nil {
		return ps, fmt.Errorf("scan last_error_at: %w", err)
	}
	return ps, nil
}
