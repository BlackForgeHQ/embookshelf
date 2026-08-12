// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"errors"

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

// libProjection is the libraries row, declared once. book_count is a
// correlated subquery rather than a stored column, so it carries an expr
// naming the table it counts against; aliasToken is what lets the SELECT
// form and the RETURNING form come from this one declaration instead of
// two constants that had to agree by hand.
var libProjection = projection[model.Library]{
	{name: "id", dest: func(l *model.Library) any { return &l.ID }},
	{name: "name", dest: func(l *model.Library) any { return &l.Name }},
	{name: "slug", dest: func(l *model.Library) any { return &l.Slug }},
	{name: "path", dest: func(l *model.Library) any { return &l.Path }},
	{name: "last_scanned_at", dest: func(l *model.Library) any { return &l.LastScannedAt }},
	{name: "file_count", dest: func(l *model.Library) any { return &l.FileCount }},
	{name: "discovered_count", dest: func(l *model.Library) any { return &l.DiscoveredCount }},
	{name: "created_at", dest: func(l *model.Library) any { return &l.CreatedAt }},
	{
		name: "book_count",
		expr: `COALESCE((SELECT COUNT(*) FROM books b WHERE b.library_id = ` + aliasToken + `.id AND b.deleted_at IS NULL), 0) AS book_count`,
		dest: func(l *model.Library) any { return &l.BookCount },
	},
	{name: "backend_id", dest: func(l *model.Library) any { return &l.BackendID }},
	{name: "root", dest: func(l *model.Library) any { return &l.Root }},
}

// libCols is the projection rendered for queries aliasing libraries as l.
var libCols = libProjection.selectList("l")

// libColsReturning is the same projection for RETURNING clauses, which
// have no alias in scope.
var libColsReturning = libProjection.returningList("libraries")

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
// UUID is generated app-side via db.NewID() rather than by a column
// default, so the INSERT can RETURNING the row it just wrote.
func (r *LibraryRepo) CreateLibrary(ctx context.Context, name, slug, path string, backendID *string) (model.Library, error) {
	id := db.NewID()
	// root mirrors path for local libraries; empty for s3 libraries (the
	// backend already encodes the prefix).
	root := path
	if backendID != nil {
		root = ""
	}
	q := `
		INSERT INTO libraries (id, name, slug, path, backend_id, root)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING ` + libColsReturning

	row := r.db.SQL.QueryRowContext(ctx, q,
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
	return collect(rows, nil, r.scanLibrary)
}

// GetByID returns a single library row. Used by scan flows that need
// the current path without a full listing.
func (r *LibraryRepo) GetByID(ctx context.Context, id string) (model.Library, error) {
	q := `
		SELECT ` + libCols + `
		FROM libraries l
		WHERE l.id = $1
	`
	row := r.db.SQL.QueryRowContext(ctx, q, id)
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
	const q = `
		UPDATE libraries
		SET last_scanned_at = now(),
		    file_count       = $2,
		    discovered_count = $3
		WHERE id = $1
	`
	return execOne(ctx, r.db.SQL, q, id, fileCount, discovered)
}

func (r *LibraryRepo) scanLibrary(s scanner) (model.Library, error) {
	var l model.Library
	if err := libProjection.scan(s, &l); err != nil {
		return l, err
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

	const qBooks = `SELECT id FROM books WHERE library_id = $1`
	rows, err := tx.QueryContext(ctx, qBooks, id)
	if err != nil {
		return nil, err
	}
	bookIDs, err := collect(rows, nil, func(s scanner) (string, error) {
		var bookID string
		err := s.Scan(&bookID)
		return bookID, err
	})
	if err != nil {
		return nil, err
	}

	const qDel = `DELETE FROM libraries WHERE id = $1`
	if err := execOne(ctx, tx, qDel, id); err != nil {
		return nil, err
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
	// ILIKE gives the case-insensitive match the autocomplete needs.
	const qPG = `
		SELECT l.id, l.name, l.slug
		FROM libraries l
		WHERE l.name ILIKE '%' || $1 || '%'
		ORDER BY l.name ASC
		LIMIT $2
	`
	rows, err := r.db.SQL.QueryContext(ctx, qPG, q, limit)
	if err != nil {
		return nil, err
	}
	return collect(rows, nil, func(s scanner) (SuggestLibrary, error) {
		var l SuggestLibrary
		err := s.Scan(&l.ID, &l.Name, &l.Slug)
		return l, err
	})
}

// LibraryBackend returns the storage_backends row associated with the given
// library by joining through the backend_id FK. Returns ErrNotFound when the
// library either does not exist or has no backend_id set yet.
func (r *LibraryRepo) LibraryBackend(ctx context.Context, libraryID string) (model.StorageBackend, error) {
	q := `
		SELECT ` + storageBackendProjection.selectList("sb") + `
		FROM libraries l
		JOIN storage_backends sb ON sb.id = l.backend_id
		WHERE l.id = $1
	`
	row := r.db.SQL.QueryRowContext(ctx, q, libraryID)

	var b model.StorageBackend
	if err := storageBackendProjection.scan(row, &b); err != nil {
		if dberr.IsNotFound(err) {
			return model.StorageBackend{}, ErrNotFound
		}
		return model.StorageBackend{}, err
	}
	return b, nil
}

// SetBackendID wires a library to a storage backend by writing the
// backend_id FK column. Pass an empty string to clear the association.
// Used by StorageBackendRepo tests and the library-update handler.
func (r *LibraryRepo) SetBackendID(ctx context.Context, libraryID, backendID string) error {
	const q = `UPDATE libraries SET backend_id = $2 WHERE id = $1`
	var nilableBackend any
	if backendID != "" {
		nilableBackend = backendID
	}
	return execOne(ctx, r.db.SQL, q, libraryID, nilableBackend)
}
