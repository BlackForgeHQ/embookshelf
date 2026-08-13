// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
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

// storageBackendProjection is the storage_backends row, declared once.
// Three call sites used to retype "id, kind, config, created_at" and
// re-decode the config JSONB by hand — this file's own scanBackend, plus
// a second copy LibraryRepo.LibraryBackend carried under a comment
// promising it stayed in sync ("re-use the same scan logic ... to avoid
// duplication", which was aspirational, not enforced). A crossed id/kind
// pair — both TEXT, adjacent in every one of those lists — would compile
// and run, and a library's backend would report another backend's kind
// under its own id.
var storageBackendProjection = projection[model.StorageBackend]{
	{name: "id", dest: func(b *model.StorageBackend) any { return &b.ID }},
	{name: "kind", dest: func(b *model.StorageBackend) any { return &b.Kind }},
	{name: "config", dest: func(b *model.StorageBackend) any { return storageBackendConfigJSON{Dst: &b.Config} }},
	{name: "created_at", dest: func(b *model.StorageBackend) any { return &b.CreatedAt }},
}

// storageBackendCols is the projection rendered for the unaliased
// storage_backends queries below. LibraryBackend's join renders the same
// projection aliased as "sb" instead.
var storageBackendCols = storageBackendProjection.returningList("storage_backends")

// Create inserts a new storage backend row and returns the persisted record.
// The id is generated app-side rather than relying on a column default so the
// value is known before the round-trip completes.
func (r *StorageBackendRepo) Create(ctx context.Context, kind string, config map[string]any) (model.StorageBackend, error) {
	id := db.NewID()

	cfgJSON, err := json.Marshal(config)
	if err != nil {
		return model.StorageBackend{}, fmt.Errorf("encode config: %w", err)
	}

	// The config column is JSONB; we cast the parameter explicitly so the
	// driver does not have to guess the target OID.
	q := `
		INSERT INTO storage_backends (id, kind, config)
		VALUES ($1, $2, $3::jsonb)
		RETURNING ` + storageBackendCols + `
	`

	row := r.db.SQL.QueryRowContext(ctx, q, id, kind, string(cfgJSON))
	return r.scanBackend(row)
}

// Get returns the backend with the given id. Returns ErrNotFound when missing.
func (r *StorageBackendRepo) Get(ctx context.Context, id string) (model.StorageBackend, error) {
	q := `
		SELECT ` + storageBackendCols + `
		FROM storage_backends
		WHERE id = $1
	`
	row := r.db.SQL.QueryRowContext(ctx, q, id)
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
		SELECT `+storageBackendCols+`
		FROM storage_backends
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	return collect(rows, nil, r.scanBackend)
}

// UpdateConfig overwrites the config JSONB for a backend row. Used by
// the boot-time shared-S3 reconciler to push current env-derived values
// into pre-existing rows whose config drifted (e.g. endpoint changed).
func (r *StorageBackendRepo) UpdateConfig(ctx context.Context, id string, config map[string]any) error {
	cfgJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	const q = `UPDATE storage_backends SET config = $2::jsonb WHERE id = $1`
	return execOne(ctx, r.db.SQL, q, id, string(cfgJSON))
}

// Delete removes the backend. Returns ErrStorageBackendInUse when any
// library still references it (FK ON DELETE RESTRICT).
func (r *StorageBackendRepo) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM storage_backends WHERE id = $1`
	if err := execOne(ctx, r.db.SQL, q, id); err != nil {
		if dberr.IsForeignKeyViolation(err) {
			return ErrStorageBackendInUse
		}
		return err
	}
	return nil
}

// scanner is shared with library.go to avoid re-declaring it; it is
// already declared in that file within the same package.

func (r *StorageBackendRepo) scanBackend(s scanner) (model.StorageBackend, error) {
	var b model.StorageBackend
	if err := storageBackendProjection.scan(s, &b); err != nil {
		return model.StorageBackend{}, err
	}
	return b, nil
}
