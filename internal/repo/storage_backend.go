package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/db/dberr"
	"github.com/blackforge/embookshelf/internal/model"
)

// ErrStorageBackendInUse is returned by Delete when at least one library
// still references the backend via its backend_id FK. The caller should
// surface this as HTTP 409 with a message prompting the user to
// re-home or delete those libraries first.
var ErrStorageBackendInUse = errors.New("storage backend is still in use by one or more libraries")

// StorageBackendRepo provides CRUD access to the storage_backends table.
type StorageBackendRepo struct {
	db *db.DB
}

// NewStorageBackendRepo constructs a StorageBackendRepo backed by d.
func NewStorageBackendRepo(d *db.DB) *StorageBackendRepo {
	return &StorageBackendRepo{db: d}
}

// Create inserts a new storage backend row and returns the persisted record.
// The id is generated app-side so the same INSERT works on both PG (UUID col)
// and SQLite (TEXT col).
func (r *StorageBackendRepo) Create(ctx context.Context, kind string, config map[string]any) (model.StorageBackend, error) {
	id := db.NewID()

	cfgJSON, err := json.Marshal(config)
	if err != nil {
		return model.StorageBackend{}, fmt.Errorf("encode config: %w", err)
	}

	// On Postgres the config column is JSONB; we cast the parameter explicitly
	// so the driver does not have to guess the target OID.
	const qPG = `
		INSERT INTO storage_backends (id, kind, config)
		VALUES ($1, $2, $3::jsonb)
		RETURNING id, kind, config, created_at
	`
	const qSQLite = `
		INSERT INTO storage_backends (id, kind, config)
		VALUES (?, ?, ?)
		RETURNING id, kind, config, created_at
	`

	row := r.db.SQL.QueryRowContext(ctx,
		db.SelectQ(r.db.Dialect, qPG, qSQLite),
		id, kind, string(cfgJSON),
	)
	return r.scanBackend(row)
}

// Get returns the backend with the given id. Returns ErrNotFound when missing.
func (r *StorageBackendRepo) Get(ctx context.Context, id string) (model.StorageBackend, error) {
	const qPG = `
		SELECT id, kind, config, created_at
		FROM storage_backends
		WHERE id = $1
	`
	const qSQLite = `
		SELECT id, kind, config, created_at
		FROM storage_backends
		WHERE id = ?
	`
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), id)
	b, err := r.scanBackend(row)
	if err != nil {
		if dberr.IsNotFound(err) {
			return model.StorageBackend{}, ErrNotFound
		}
		return model.StorageBackend{}, err
	}
	return b, nil
}

// List returns all backends ordered by created_at ASC.
func (r *StorageBackendRepo) List(ctx context.Context) ([]model.StorageBackend, error) {
	rows, err := r.db.SQL.QueryContext(ctx, `
		SELECT id, kind, config, created_at
		FROM storage_backends
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.StorageBackend
	for rows.Next() {
		b, err := r.scanBackend(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Delete removes the backend. Returns ErrStorageBackendInUse when any
// library still references it (FK ON DELETE RESTRICT).
func (r *StorageBackendRepo) Delete(ctx context.Context, id string) error {
	const qPG = `DELETE FROM storage_backends WHERE id = $1`
	const qSQLite = `DELETE FROM storage_backends WHERE id = ?`
	res, err := r.db.SQL.ExecContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), id)
	if err != nil {
		if dberr.IsForeignKeyViolation(err) {
			return ErrStorageBackendInUse
		}
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// scanner is shared with library.go to avoid re-declaring it; it is
// already declared in that file within the same package.

func (r *StorageBackendRepo) scanBackend(s scanner) (model.StorageBackend, error) {
	var b model.StorageBackend
	var configRaw any
	var createdAny any

	err := s.Scan(&b.ID, &b.Kind, &configRaw, &createdAny)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return b, sql.ErrNoRows
		}
		return b, err
	}

	// Decode JSON config. Postgres JSONB arrives as []byte, SQLite TEXT arrives
	// as string. Both are valid JSON.
	var raw []byte
	switch v := configRaw.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return b, fmt.Errorf("unexpected type for config column: %T", configRaw)
	}
	if err := json.Unmarshal(raw, &b.Config); err != nil {
		return b, fmt.Errorf("decode config: %w", err)
	}

	if err := db.ScanTime(r.db.Dialect, createdAny, &b.CreatedAt); err != nil {
		return b, fmt.Errorf("scan created_at: %w", err)
	}
	return b, nil
}
