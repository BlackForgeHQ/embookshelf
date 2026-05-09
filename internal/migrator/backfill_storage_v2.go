// SPDX-License-Identifier: AGPL-3.0-or-later

package migrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/blackforge/embookshelf/internal/db"
)

// BackfillStorageV2 populates the columns added by migration 000025.
// Idempotent: re-running on an already-backfilled DB is a no-op.
//
// Strategy: short-circuit on the app_settings sentinel; otherwise run
// the four steps below in sequence. Each step is itself guarded by
// WHERE NOT EXISTS so a partial run that crashed mid-way still
// converges to a valid state on the next call.
func BackfillStorageV2(ctx context.Context, d *db.DB) error {
	done, err := storageV2AlreadyBackfilled(ctx, d)
	if err != nil {
		return fmt.Errorf("storage_v2 backfill: check sentinel: %w", err)
	}
	if done {
		return nil
	}
	start := time.Now()

	backends, err := seedStorageBackends(ctx, d)
	if err != nil {
		return fmt.Errorf("seed backends: %w", err)
	}
	libs, err := wireLibraries(ctx, d)
	if err != nil {
		return fmt.Errorf("wire libraries: %w", err)
	}
	uuidsAssigned, err := assignBookUUIDs(ctx, d)
	if err != nil {
		return fmt.Errorf("assign book uuids: %w", err)
	}
	files, err := seedFilesFromBooks(ctx, d)
	if err != nil {
		return fmt.Errorf("seed files: %w", err)
	}

	if err := setStorageV2Sentinel(ctx, d); err != nil {
		return fmt.Errorf("write sentinel: %w", err)
	}
	slog.Info("storage_v2 backfill complete",
		"backends", backends,
		"libraries_wired", libs,
		"uuids_assigned", uuidsAssigned,
		"files_seeded", files,
		"duration", time.Since(start),
	)
	return nil
}

func storageV2AlreadyBackfilled(ctx context.Context, d *db.DB) (bool, error) {
	var v string
	err := d.SQL.QueryRowContext(ctx,
		`SELECT value FROM app_settings WHERE name = 'storage_v2_backfilled'`,
	).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return v != "" && v != "false", nil
}

func setStorageV2Sentinel(ctx context.Context, d *db.DB) error {
	val := `"true"` // JSON-encoded scalar string
	_, err := dialectExec(ctx, d,
		`INSERT INTO app_settings (name, value)
			VALUES ('storage_v2_backfilled', $1::jsonb)
			ON CONFLICT (name) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		`INSERT INTO app_settings (name, value, updated_at)
			VALUES ('storage_v2_backfilled', $1, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
			ON CONFLICT (name) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		val,
	)
	return err
}

// seedStorageBackends inserts one storage_backends row per distinct non-empty
// libraries.path. Returns the number of rows inserted.
func seedStorageBackends(ctx context.Context, d *db.DB) (int, error) {
	rows, err := d.SQL.QueryContext(ctx,
		`SELECT DISTINCT path FROM libraries WHERE path <> ''`,
	)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return 0, err
		}
		paths = append(paths, p)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	inserted := 0
	for _, p := range paths {
		cfg, err := json.Marshal(map[string]string{"root": p})
		if err != nil {
			return inserted, fmt.Errorf("marshal backend config: %w", err)
		}

		var existsCount int
		if err := dialectQueryRow(ctx, d,
			`SELECT count(*) FROM storage_backends WHERE kind = 'local' AND config->>'root' = $1`,
			`SELECT count(*) FROM storage_backends WHERE kind = 'local' AND json_extract(config, '$.root') = $1`,
			p,
		).Scan(&existsCount); err != nil {
			return inserted, err
		}
		if existsCount > 0 {
			continue
		}

		id := uuid.NewString()
		if _, err := dialectExec(ctx, d,
			`INSERT INTO storage_backends (id, kind, config) VALUES ($1, 'local', $2::jsonb)`,
			`INSERT INTO storage_backends (id, kind, config, created_at) VALUES ($1, 'local', $2, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
			id, string(cfg),
		); err != nil {
			return inserted, err
		}
		inserted++
	}
	return inserted, nil
}

// wireLibraries updates each library with a non-empty path to reference its
// storage_backend and copies path → root. Returns the number of rows updated.
func wireLibraries(ctx context.Context, d *db.DB) (int, error) {
	res, err := dialectExec(ctx, d,
		`UPDATE libraries
			SET backend_id = sb.id,
			    root       = libraries.path
			FROM storage_backends sb
			WHERE sb.kind = 'local'
			  AND sb.config->>'root' = libraries.path
			  AND libraries.path <> ''
			  AND libraries.backend_id IS NULL`,
		// SQLite UPDATE doesn't support FROM; use a subquery.
		`UPDATE libraries
			SET backend_id = (
				SELECT id FROM storage_backends
				WHERE kind = 'local' AND json_extract(config, '$.root') = libraries.path
			),
			root = libraries.path
			WHERE libraries.path <> '' AND libraries.backend_id IS NULL`,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// assignBookUUIDs assigns a fresh UUID to every book where uuid IS NULL.
// Returns the number of books updated.
func assignBookUUIDs(ctx context.Context, d *db.DB) (int, error) {
	rows, err := d.SQL.QueryContext(ctx, `SELECT id FROM books WHERE uuid IS NULL`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, id := range ids {
		if _, err := d.SQL.ExecContext(ctx,
			`UPDATE books SET uuid = $1 WHERE id = $2 AND uuid IS NULL`,
			uuid.NewString(), id,
		); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

type bookForFile struct {
	bookID    string
	libraryID string
	path      string
	libRoot   sql.NullString
	format    string
	updatedAt time.Time
}

// seedFilesFromBooks inserts one files row per book that has a non-empty path
// and no existing files row. The location is the book path relative to the
// library root; if it doesn't fall under the root the absolute path is stored
// verbatim. Returns the number of rows inserted.
func seedFilesFromBooks(ctx context.Context, d *db.DB) (int, error) {
	rows, err := d.SQL.QueryContext(ctx, `
		SELECT b.id, b.library_id, b.path, l.root, b.format, b.updated_at
		FROM books b
		JOIN libraries l ON l.id = b.library_id
		WHERE b.path <> ''
		  AND b.deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM files WHERE files.book_id = b.id)
	`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()

	var batch []bookForFile
	for rows.Next() {
		var bf bookForFile
		var rawUpdatedAt any
		if err := rows.Scan(
			&bf.bookID, &bf.libraryID, &bf.path, &bf.libRoot, &bf.format, &rawUpdatedAt,
		); err != nil {
			return 0, err
		}
		if err := db.ScanTime(d.Dialect, rawUpdatedAt, &bf.updatedAt); err != nil {
			return 0, fmt.Errorf("scan updated_at for book %s: %w", bf.bookID, err)
		}
		batch = append(batch, bf)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, bf := range batch {
		loc := bf.path
		if bf.libRoot.Valid && bf.libRoot.String != "" {
			prefix := bf.libRoot.String
			if !strings.HasSuffix(prefix, "/") {
				prefix += "/"
			}
			if strings.HasPrefix(bf.path, prefix) {
				loc = bf.path[len(prefix):]
			}
		}

		if _, err := dialectExec(ctx, d,
			`INSERT INTO files (id, library_id, book_id, location, size, mtime, format, last_scanned)
					VALUES ($1, $2, $3, $4, 0, $5, $6, $5)
					ON CONFLICT (library_id, location) DO NOTHING`,
			`INSERT INTO files (id, library_id, book_id, location, size, mtime, format, last_scanned)
					VALUES ($1, $2, $3, $4, 0, $5, $6, $5)
					ON CONFLICT(library_id, location) DO NOTHING`,
			uuid.NewString(), bf.libraryID, bf.bookID, loc, bf.updatedAt, bf.format,
		); err != nil {
			return 0, err
		}
	}
	return len(batch), nil
}

// dialectExec executes pgSQL on Postgres or sqliteSQL on SQLite.
func dialectExec(ctx context.Context, d *db.DB, pgSQL, sqliteSQL string, args ...any) (sql.Result, error) {
	switch d.Dialect {
	case db.DialectPostgres:
		return d.SQL.ExecContext(ctx, pgSQL, args...)
	case db.DialectSQLite:
		return d.SQL.ExecContext(ctx, sqliteSQL, args...)
	}
	return nil, fmt.Errorf("unsupported dialect %q", d.Dialect)
}

// dialectQueryRow executes pgSQL on Postgres or sqliteSQL on SQLite,
// returning a single row for scanning. Panics on an unknown dialect — this
// represents a programmer error, not a runtime condition.
func dialectQueryRow(ctx context.Context, d *db.DB, pgSQL, sqliteSQL string, args ...any) *sql.Row {
	switch d.Dialect {
	case db.DialectPostgres:
		return d.SQL.QueryRowContext(ctx, pgSQL, args...)
	case db.DialectSQLite:
		return d.SQL.QueryRowContext(ctx, sqliteSQL, args...)
	}
	panic(fmt.Sprintf("unsupported dialect %q", d.Dialect))
}
