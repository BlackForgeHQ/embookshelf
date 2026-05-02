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

// ErrNotFound is returned when a lookup by id/slug returns no rows.
var ErrNotFound = errors.New("not found")

// ErrLibraryNameTaken is returned by CreateLibrary when the derived slug
// collides with an existing library. Callers should surface this as a 409
// so the UI can prompt the user to pick a different name.
var ErrLibraryNameTaken = errors.New("library name already taken")

// ErrLibraryPathTaken is returned when the supplied filesystem root is
// already bound to another library. Two libraries sharing one path would
// race on scans and naming collisions.
var ErrLibraryPathTaken = errors.New("library path already in use")

type LibraryRepo struct {
	db *db.DB
}

func NewLibraryRepo(d *db.DB) *LibraryRepo {
	return &LibraryRepo{db: d}
}

// libCols is the shared SELECT list for library rows. Keep the scan
// order in scanLibrary() in sync if you add columns here.
const libCols = `
	l.id, l.name, l.slug, l.path,
	l.last_scanned_at, l.file_count, l.discovered_count,
	l.created_at,
	COALESCE(
		(SELECT COUNT(*) FROM books b
		 WHERE b.library_id = l.id AND b.deleted_at IS NULL),
		0
	) AS book_count,
	l.backend_id, l.root
`

// libColsReturning is the same projection for RETURNING clauses where
// no table alias is available.
const libColsReturning = `
	id, name, slug, path,
	last_scanned_at, file_count, discovered_count,
	created_at,
	COALESCE(
		(SELECT COUNT(*) FROM books b
		 WHERE b.library_id = libraries.id AND b.deleted_at IS NULL),
		0
	) AS book_count,
	backend_id, root
`

// CreateLibrary inserts a new library row and returns the persisted
// record. `slug` is UNIQUE (000001) and `path` is UNIQUE (000018, partial
// — excludes empty strings so multiple s3 libraries with empty path don't
// collide) — a collision on either surfaces as a typed sentinel
// (ErrLibraryNameTaken or ErrLibraryPathTaken) so the handler can map it
// to a 409.
//
// backendID == nil → INSERT with NULL backend_id (local libraries).
// backendID != nil → INSERT with that value; path should be "".
// root for s3 libraries is empty (the backend has the prefix encoded);
// root for local libraries equals path.
//
// UUID is generated app-side via db.NewID() so the same INSERT works on
// both Postgres (UUID column) and SQLite (TEXT column).
func (r *LibraryRepo) CreateLibrary(ctx context.Context, name, slug, path string, backendID *string) (model.Library, error) {
	id := db.NewID()
	// root mirrors path for local libraries; empty for s3 libraries (the
	// backend already encodes the prefix).
	root := path
	if backendID != nil {
		root = ""
	}
	const qPG = `
		INSERT INTO libraries (id, name, slug, path, backend_id, root)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING ` + libColsReturning

	const qSQLite = `
		INSERT INTO libraries (id, name, slug, path, backend_id, root)
		VALUES (?, ?, ?, ?, ?, ?)
		RETURNING ` + libColsReturning

	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite),
		id, name, slug, path, backendID, root)
	l, err := r.scanLibrary(row)
	if err != nil {
		if ok, constraint := dberr.IsUniqueViolation(err); ok {
			switch constraint {
			case "libraries_slug_key":
				return model.Library{}, ErrLibraryNameTaken
			case "libraries_path_key":
				return model.Library{}, ErrLibraryPathTaken
			}
		}
		return model.Library{}, err
	}
	return l, nil
}

func (r *LibraryRepo) List(ctx context.Context) ([]model.Library, error) {
	rows, err := r.db.SQL.QueryContext(ctx, `
		SELECT `+libCols+`
		FROM libraries l
		ORDER BY l.created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var libs []model.Library
	for rows.Next() {
		l, err := r.scanLibrary(rows)
		if err != nil {
			return nil, err
		}
		libs = append(libs, l)
	}
	return libs, rows.Err()
}

// GetByID returns a single library row. Used by scan flows that need
// the current path without a full listing.
func (r *LibraryRepo) GetByID(ctx context.Context, id string) (model.Library, error) {
	const qPG = `
		SELECT ` + libCols + `
		FROM libraries l
		WHERE l.id = $1
	`
	const qSQLite = `
		SELECT ` + libCols + `
		FROM libraries l
		WHERE l.id = ?
	`
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), id)
	l, err := r.scanLibrary(row)
	if err != nil {
		if dberr.IsNotFound(err) {
			return model.Library{}, ErrNotFound
		}
		return model.Library{}, err
	}
	return l, nil
}

// TouchScan stamps the last-scan aggregate on a library row after a
// filesystem walk completes. Used by the library-scan worker.
func (r *LibraryRepo) TouchScan(ctx context.Context, id string, fileCount, discovered int) error {
	const qPG = `
		UPDATE libraries
		SET last_scanned_at = now(),
		    file_count       = $2,
		    discovered_count = $3
		WHERE id = $1
	`
	const qSQLite = `
		UPDATE libraries
		SET last_scanned_at = CURRENT_TIMESTAMP,
		    file_count       = ?,
		    discovered_count = ?
		WHERE id = ?
	`
	var res sql.Result
	var err error
	if r.db.Dialect == db.DialectSQLite {
		res, err = r.db.SQL.ExecContext(ctx, qSQLite, fileCount, discovered, id)
	} else {
		res, err = r.db.SQL.ExecContext(ctx, qPG, id, fileCount, discovered)
	}
	if err != nil {
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

func (r *LibraryRepo) scanLibrary(s scanner) (model.Library, error) {
	var l model.Library
	var lastScannedAny, createdAny any
	var backendID, root sql.NullString
	err := s.Scan(
		&l.ID, &l.Name, &l.Slug, &l.Path,
		&lastScannedAny, &l.FileCount, &l.DiscoveredCount,
		&createdAny, &l.BookCount,
		&backendID, &root,
	)
	if err != nil {
		return l, err
	}
	if err := db.ScanNullTime(r.db.Dialect, lastScannedAny, &l.LastScannedAt); err != nil {
		return l, fmt.Errorf("scan last_scanned_at: %w", err)
	}
	if err := db.ScanTime(r.db.Dialect, createdAny, &l.CreatedAt); err != nil {
		return l, fmt.Errorf("scan created_at: %w", err)
	}
	if backendID.Valid {
		s := backendID.String
		l.BackendID = &s
	}
	if root.Valid {
		s := root.String
		l.Root = &s
	}
	return l, nil
}

// DeleteLibrary removes a library row and cascades the deletion through
// books, library_paths, shelf_books, annotations, reading_sessions, and
// per-user progress via the existing FK ON DELETE CASCADE chain. The
// returned []bookIDs lets the caller clean up cover-image files on disk
// — those aren't owned by the DB so the cascade can't reach them.
func (r *LibraryRepo) DeleteLibrary(ctx context.Context, id string) ([]string, error) {
	tx, err := r.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	const qBooksPG = `SELECT id FROM books WHERE library_id = $1`
	const qBooksSQLite = `SELECT id FROM books WHERE library_id = ?`
	rows, err := tx.QueryContext(ctx, db.SelectQ(r.db.Dialect, qBooksPG, qBooksSQLite), id)
	if err != nil {
		return nil, err
	}
	var bookIDs []string
	for rows.Next() {
		var bookID string
		if err := rows.Scan(&bookID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		bookIDs = append(bookIDs, bookID)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	const qDelPG = `DELETE FROM libraries WHERE id = $1`
	const qDelSQLite = `DELETE FROM libraries WHERE id = ?`
	res, err := tx.ExecContext(ctx, db.SelectQ(r.db.Dialect, qDelPG, qDelSQLite), id)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return bookIDs, nil
}

// SuggestLibrary is the slim shape for library autocomplete rows.
type SuggestLibrary struct {
	ID   string
	Name string
	Slug string
}

// SearchSuggest returns libraries whose name matches `q`. Today every
// authenticated user can see every library (mirrors GET /libraries), so
// there is no per-user filter here — adopt one if/when library
// visibility becomes user-scoped.
func (r *LibraryRepo) SearchSuggest(ctx context.Context, q string, limit int) ([]SuggestLibrary, error) {
	// ILIKE is Postgres-specific; SQLite's LIKE is case-insensitive for ASCII
	// by default (the project's SQLite pragma does not override that).
	const qPG = `
		SELECT l.id, l.name, l.slug
		FROM libraries l
		WHERE l.name ILIKE '%' || $1 || '%'
		ORDER BY l.name ASC
		LIMIT $2
	`
	const qSQLite = `
		SELECT l.id, l.name, l.slug
		FROM libraries l
		WHERE l.name LIKE '%' || ? || '%'
		ORDER BY l.name ASC
		LIMIT ?
	`
	rows, err := r.db.SQL.QueryContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), q, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SuggestLibrary
	for rows.Next() {
		var l SuggestLibrary
		if err := rows.Scan(&l.ID, &l.Name, &l.Slug); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// LibraryBackend returns the storage_backends row associated with the given
// library by joining through the backend_id FK. Returns ErrNotFound when the
// library either does not exist or has no backend_id set yet.
func (r *LibraryRepo) LibraryBackend(ctx context.Context, libraryID string) (model.StorageBackend, error) {
	const qPG = `
		SELECT sb.id, sb.kind, sb.config, sb.created_at
		FROM libraries l
		JOIN storage_backends sb ON sb.id = l.backend_id
		WHERE l.id = $1
	`
	const qSQLite = `
		SELECT sb.id, sb.kind, sb.config, sb.created_at
		FROM libraries l
		JOIN storage_backends sb ON sb.id = l.backend_id
		WHERE l.id = ?
	`
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), libraryID)

	// Re-use the same scan logic as StorageBackendRepo to avoid duplication.
	var b model.StorageBackend
	var configRaw, createdAny any
	if err := row.Scan(&b.ID, &b.Kind, &configRaw, &createdAny); err != nil {
		if dberr.IsNotFound(err) {
			return model.StorageBackend{}, ErrNotFound
		}
		return model.StorageBackend{}, err
	}

	var raw []byte
	switch v := configRaw.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return model.StorageBackend{}, fmt.Errorf("unexpected type for config column: %T", configRaw)
	}
	if err := json.Unmarshal(raw, &b.Config); err != nil {
		return model.StorageBackend{}, fmt.Errorf("decode config: %w", err)
	}
	if err := db.ScanTime(r.db.Dialect, createdAny, &b.CreatedAt); err != nil {
		return model.StorageBackend{}, fmt.Errorf("scan created_at: %w", err)
	}
	return b, nil
}

// SetBackendID wires a library to a storage backend by writing the
// backend_id FK column. Pass an empty string to clear the association.
// Used by StorageBackendRepo tests and the library-update handler.
func (r *LibraryRepo) SetBackendID(ctx context.Context, libraryID, backendID string) error {
	const qPG = `UPDATE libraries SET backend_id = $2 WHERE id = $1`
	const qSQLite = `UPDATE libraries SET backend_id = ? WHERE id = ?`
	var nilableBackend any
	if backendID != "" {
		nilableBackend = backendID
	}
	var res sql.Result
	var err error
	if r.db.Dialect == db.DialectSQLite {
		res, err = r.db.SQL.ExecContext(ctx, qSQLite, nilableBackend, libraryID)
	} else {
		res, err = r.db.SQL.ExecContext(ctx, qPG, libraryID, nilableBackend)
	}
	if err != nil {
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
