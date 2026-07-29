// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/db/dberr"
	"github.com/blackforge/embookshelf/internal/model"
)

// ErrFileLocationTaken is returned by Insert when (library_id, location)
// already exists in the files table.
var ErrFileLocationTaken = errors.New("file location already taken in this library")

// fileProjection is the files row, declared once. Its eleven columns
// used to be retyped inline at six query sites plus scanFile's
// destinations; every SELECT and the INSERT's RETURNING render from here.
//
// files queries never alias the table, so the bare form serves both the
// SELECT lists and the RETURNING clause.
var fileProjection = projection[model.File]{
	{name: "id", dest: func(f *model.File) any { return &f.ID }},
	{name: "library_id", dest: func(f *model.File) any { return &f.LibraryID }},
	{name: "book_id", dest: func(f *model.File) any { return nullText{Dst: &f.BookID} }},
	{name: "location", dest: func(f *model.File) any { return &f.Location }},
	{name: "size", dest: func(f *model.File) any { return &f.Size }},
	{name: "mtime", dest: func(f *model.File) any { return &f.Mtime }},
	{name: "etag", dest: func(f *model.File) any { return nullText{Dst: &f.ETag} }},
	{name: "content_hash", dest: func(f *model.File) any { return &f.ContentHash }},
	{name: "format", dest: func(f *model.File) any { return &f.Format }},
	{name: "last_scanned", dest: func(f *model.File) any { return &f.LastScanned }},
	{name: "missing_since", dest: func(f *model.File) any { return &f.MissingSince }},
}

// fileCols is the projection rendered for the unaliased files queries.
var fileCols = fileProjection.returningList("files")

// FileRepo provides access to the files table.
type FileRepo struct {
	db *db.DB
}

// NewFileRepo constructs a FileRepo backed by d.
func NewFileRepo(d *db.DB) *FileRepo {
	return &FileRepo{db: d}
}

// Insert creates a new files row. Returns ErrFileLocationTaken on
// (library_id, location) collision. The id on f is ignored; a fresh
// id is generated app-side and returned on the result.
func (r *FileRepo) Insert(ctx context.Context, f model.File) (model.File, error) {
	id := db.NewID()

	// book_id is nullable — pass nil when empty.
	var bookID any
	if f.BookID != "" {
		bookID = f.BookID
	}
	// etag is nullable — pass nil when empty.
	var etag any
	if f.ETag != "" {
		etag = f.ETag
	}
	// content_hash is nullable — pass nil when not set.
	var contentHash any
	if len(f.ContentHash) > 0 {
		contentHash = f.ContentHash
	}

	q := `
		INSERT INTO files (id, library_id, book_id, location, size, mtime, etag, content_hash, format, last_scanned)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING ` + fileCols + `
	`

	// mtime and last_scanned: passed as time.Time (pgx handles TIMESTAMPTZ).
	row := r.db.SQL.QueryRowContext(ctx,
		q,
		id, f.LibraryID, bookID, f.Location, f.Size,
		f.Mtime, etag, contentHash, f.Format, f.LastScanned,
	)
	result, err := r.scanFile(row)
	if err != nil {
		if ok, _ := dberr.IsUniqueViolation(err); ok {
			return model.File{}, ErrFileLocationTaken
		}
		return model.File{}, err
	}
	return result, nil
}

// GetByLocation returns the file at (library_id, location). Returns
// ErrNotFound when missing.
func (r *FileRepo) GetByLocation(ctx context.Context, libraryID, location string) (model.File, error) {
	q := `
		SELECT ` + fileCols + `
		FROM files
		WHERE library_id = $1 AND location = $2
	`
	row := r.db.SQL.QueryRowContext(ctx, q, libraryID, location)
	f, err := r.scanFile(row)
	if err != nil {
		if dberr.IsNotFound(err) {
			return model.File{}, ErrNotFound
		}
		return model.File{}, err
	}
	return f, nil
}

// GetByContentHash returns every files row with the given content_hash.
// Used for duplicate detection at scan time. An empty slice (not nil) is
// returned when no rows match.
func (r *FileRepo) GetByContentHash(ctx context.Context, hash []byte) ([]model.File, error) {
	q := `
		SELECT ` + fileCols + `
		FROM files
		WHERE content_hash = $1
	`
	rows, err := r.db.SQL.QueryContext(ctx, q, hash)
	if err != nil {
		return nil, err
	}
	return collect(rows, []model.File{}, r.scanFile)
}

// ExistsByLocation answers whether there is already a file at this
// (library_id, location) key without fetching the full row.
func (r *FileRepo) ExistsByLocation(ctx context.Context, libraryID, location string) (bool, error) {
	const q = `SELECT count(*) FROM files WHERE library_id = $1 AND location = $2`
	var n int
	err := r.db.SQL.QueryRowContext(ctx,
		q,
		libraryID, location,
	).Scan(&n)
	return n > 0, err
}

// SetContentHash records the hash, size, and mtime once a file has been
// scanned. Used by the boot-time backfill worker.
func (r *FileRepo) SetContentHash(ctx context.Context, fileID string, hash []byte, size int64, mtime time.Time) error {
	const q = `
		UPDATE files
		SET content_hash = $2,
		    size         = $3,
		    mtime        = $4
		WHERE id = $1
	`

	return execOne(ctx, r.db.SQL, q, fileID, hash, size, mtime)
}

// ListPendingHash returns up to batchSize files whose content_hash is
// still NULL. Used by the backfill worker to drain the queue.
func (r *FileRepo) ListPendingHash(ctx context.Context, batchSize int) ([]model.File, error) {
	q := `
		SELECT ` + fileCols + `
		FROM files
		WHERE content_hash IS NULL
		ORDER BY id
		LIMIT $1
	`
	rows, err := r.db.SQL.QueryContext(ctx, q, batchSize)
	if err != nil {
		return nil, err
	}
	return collect(rows, nil, r.scanFile)
}

// MarkScanned bumps last_scanned to now without changing the hash. Used
// by the scan worker to record that a file was inspected.
func (r *FileRepo) MarkScanned(ctx context.Context, fileID string) error {
	const q = `UPDATE files SET last_scanned = now() WHERE id = $1`
	return execOne(ctx, r.db.SQL, q, fileID)
}

// MarkMissing records that the file is no longer present in storage.
// Idempotent: re-marking with a later 'when' is allowed (overwrite);
// re-marking with the same 'when' is a no-op.
func (r *FileRepo) MarkMissing(ctx context.Context, fileID string, when time.Time) error {
	const q = `UPDATE files SET missing_since = $2 WHERE id = $1`

	return execOne(ctx, r.db.SQL, q, fileID, when)
}

// ClearMissing flips missing_since back to NULL when a previously
// missing file reappears.
func (r *FileRepo) ClearMissing(ctx context.Context, fileID string) error {
	const q = `UPDATE files SET missing_since = NULL WHERE id = $1`

	return execOne(ctx, r.db.SQL, q, fileID)
}

// DeleteMissingOlderThan purges rows whose missing_since is more
// than ttl ago. Returns the count deleted. Rows where missing_since
// Delete removes one files row. Used when the artifact it names is
// deliberately discarded — today only a narration, whose bytes go with
// it (#208).
func (r *FileRepo) Delete(ctx context.Context, fileID string) error {
	_, err := r.db.SQL.ExecContext(ctx, `DELETE FROM files WHERE id = $1`, fileID)
	return err
}

// IS NULL are not affected.
func (r *FileRepo) DeleteMissingOlderThan(ctx context.Context, ttl time.Duration) (int64, error) {
	cutoff := time.Now().Add(-ttl)

	const q = `DELETE FROM files WHERE missing_since IS NOT NULL AND missing_since < $1`

	res, err := r.db.SQL.ExecContext(ctx, q, cutoff)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}

// ListByLibrary returns every files row for libraryID (including
// missing ones). Used by the scan worker to diff against the live walk.
func (r *FileRepo) ListByLibrary(ctx context.Context, libraryID string) ([]model.File, error) {
	q := `
		SELECT ` + fileCols + `
		FROM files
		WHERE library_id = $1
		ORDER BY location
	`
	rows, err := r.db.SQL.QueryContext(ctx, q, libraryID)
	if err != nil {
		return nil, err
	}
	return collect(rows, nil, r.scanFile)
}

// ListByBook returns all files rows for bookID ordered by id.
// Returns an empty slice (not nil) when no rows match.
func (r *FileRepo) ListByBook(ctx context.Context, bookID string) ([]model.File, error) {
	q := `
		SELECT ` + fileCols + `
		FROM files
		WHERE book_id = $1
		ORDER BY id
	`
	rows, err := r.db.SQL.QueryContext(ctx, q, bookID)
	if err != nil {
		return nil, err
	}
	return collect(rows, []model.File{}, r.scanFile)
}

// UpdateLocation moves a row to a new location within the same
// library. Used by hash-based reattach when a file is renamed.
// Returns ErrFileLocationTaken on (library_id, location) conflict.
func (r *FileRepo) UpdateLocation(ctx context.Context, fileID, newLocation string) error {
	const q = `UPDATE files SET location = $2 WHERE id = $1`

	if err := execOne(ctx, r.db.SQL, q, fileID, newLocation); err != nil {
		if ok, _ := dberr.IsUniqueViolation(err); ok {
			return ErrFileLocationTaken
		}
		return err
	}
	return nil
}

// scanFile scans a files row into a model.File. s may be a *sql.Row or
// *sql.Rows — both implement the scanner interface.
func (r *FileRepo) scanFile(s scanner) (model.File, error) {
	var f model.File
	if err := fileProjection.scan(s, &f); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return f, sql.ErrNoRows
		}
		return f, err
	}
	return f, nil
}
