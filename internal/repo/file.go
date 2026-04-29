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

	const qPG = `
		INSERT INTO files (id, library_id, book_id, location, size, mtime, etag, content_hash, format, last_scanned)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, library_id, book_id, location, size, mtime, etag, content_hash, format, last_scanned
	`
	const qSQLite = `
		INSERT INTO files (id, library_id, book_id, location, size, mtime, etag, content_hash, format, last_scanned)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id, library_id, book_id, location, size, mtime, etag, content_hash, format, last_scanned
	`

	// mtime and last_scanned: pass as time.Time on PG (pgx handles TIMESTAMPTZ),
	// pass as ISO-8601 string on SQLite.
	var mtimeVal, lastScannedVal any
	if r.db.Dialect == db.DialectSQLite {
		mtimeVal = f.Mtime.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
		lastScannedVal = f.LastScanned.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	} else {
		mtimeVal = f.Mtime
		lastScannedVal = f.LastScanned
	}

	row := r.db.SQL.QueryRowContext(ctx,
		db.SelectQ(r.db.Dialect, qPG, qSQLite),
		id, f.LibraryID, bookID, f.Location, f.Size,
		mtimeVal, etag, contentHash, f.Format, lastScannedVal,
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
	const qPG = `
		SELECT id, library_id, book_id, location, size, mtime, etag, content_hash, format, last_scanned
		FROM files
		WHERE library_id = $1 AND location = $2
	`
	const qSQLite = `
		SELECT id, library_id, book_id, location, size, mtime, etag, content_hash, format, last_scanned
		FROM files
		WHERE library_id = ? AND location = ?
	`
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), libraryID, location)
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
	const qPG = `
		SELECT id, library_id, book_id, location, size, mtime, etag, content_hash, format, last_scanned
		FROM files
		WHERE content_hash = $1
	`
	const qSQLite = `
		SELECT id, library_id, book_id, location, size, mtime, etag, content_hash, format, last_scanned
		FROM files
		WHERE content_hash = ?
	`
	rows, err := r.db.SQL.QueryContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), hash)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []model.File{}
	for rows.Next() {
		f, err := r.scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ExistsByLocation answers whether there is already a file at this
// (library_id, location) key without fetching the full row.
func (r *FileRepo) ExistsByLocation(ctx context.Context, libraryID, location string) (bool, error) {
	const qPG = `SELECT count(*) FROM files WHERE library_id = $1 AND location = $2`
	const qSQLite = `SELECT count(*) FROM files WHERE library_id = ? AND location = ?`
	var n int
	err := r.db.SQL.QueryRowContext(ctx,
		db.SelectQ(r.db.Dialect, qPG, qSQLite),
		libraryID, location,
	).Scan(&n)
	return n > 0, err
}

// SetContentHash records the hash, size, and mtime once a file has been
// scanned. Used by the boot-time backfill worker.
func (r *FileRepo) SetContentHash(ctx context.Context, fileID string, hash []byte, size int64, mtime time.Time) error {
	const qPG = `
		UPDATE files
		SET content_hash = $2,
		    size         = $3,
		    mtime        = $4
		WHERE id = $1
	`
	const qSQLite = `
		UPDATE files
		SET content_hash = ?,
		    size         = ?,
		    mtime        = ?
		WHERE id = ?
	`

	var mtimeVal any
	if r.db.Dialect == db.DialectSQLite {
		mtimeVal = mtime.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	} else {
		mtimeVal = mtime
	}

	var res sql.Result
	var err error
	if r.db.Dialect == db.DialectSQLite {
		res, err = r.db.SQL.ExecContext(ctx, qSQLite, hash, size, mtimeVal, fileID)
	} else {
		res, err = r.db.SQL.ExecContext(ctx, qPG, fileID, hash, size, mtimeVal)
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

// ListPendingHash returns up to batchSize files whose content_hash is
// still NULL. Used by the backfill worker to drain the queue.
func (r *FileRepo) ListPendingHash(ctx context.Context, batchSize int) ([]model.File, error) {
	const qPG = `
		SELECT id, library_id, book_id, location, size, mtime, etag, content_hash, format, last_scanned
		FROM files
		WHERE content_hash IS NULL
		ORDER BY id
		LIMIT $1
	`
	const qSQLite = `
		SELECT id, library_id, book_id, location, size, mtime, etag, content_hash, format, last_scanned
		FROM files
		WHERE content_hash IS NULL
		ORDER BY id
		LIMIT ?
	`
	rows, err := r.db.SQL.QueryContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), batchSize)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.File
	for rows.Next() {
		f, err := r.scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// MarkScanned bumps last_scanned to now without changing the hash. Used
// by the scan worker to record that a file was inspected.
func (r *FileRepo) MarkScanned(ctx context.Context, fileID string) error {
	const qPG = `UPDATE files SET last_scanned = now() WHERE id = $1`
	const qSQLite = `UPDATE files SET last_scanned = (strftime('%Y-%m-%dT%H:%M:%fZ','now')) WHERE id = ?`
	res, err := r.db.SQL.ExecContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), fileID)
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

// scanFile scans a files row into a model.File. s may be a *sql.Row or
// *sql.Rows — both implement the scanner interface.
func (r *FileRepo) scanFile(s scanner) (model.File, error) {
	var f model.File
	var bookIDAny any
	var etagAny any
	var contentHashAny any
	var mtimeAny any
	var lastScannedAny any

	err := s.Scan(
		&f.ID, &f.LibraryID, &bookIDAny, &f.Location,
		&f.Size, &mtimeAny, &etagAny, &contentHashAny,
		&f.Format, &lastScannedAny,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return f, sql.ErrNoRows
		}
		return f, err
	}

	// Nullable book_id
	if bookIDAny != nil {
		switch v := bookIDAny.(type) {
		case string:
			f.BookID = v
		case []byte:
			f.BookID = string(v)
		}
	}

	// Nullable etag
	if etagAny != nil {
		switch v := etagAny.(type) {
		case string:
			f.ETag = v
		case []byte:
			f.ETag = string(v)
		}
	}

	// Nullable content_hash — BYTEA on PG, BLOB on SQLite, both map to []byte.
	if contentHashAny != nil {
		switch v := contentHashAny.(type) {
		case []byte:
			f.ContentHash = v
		case string:
			f.ContentHash = []byte(v)
		}
	}

	// mtime: non-nullable TIMESTAMPTZ (PG) / TEXT ISO-8601 (SQLite)
	if err := db.ScanTime(r.db.Dialect, mtimeAny, &f.Mtime); err != nil {
		return f, fmt.Errorf("scan mtime: %w", err)
	}

	// last_scanned: non-nullable
	if err := db.ScanTime(r.db.Dialect, lastScannedAny, &f.LastScanned); err != nil {
		return f, fmt.Errorf("scan last_scanned: %w", err)
	}

	return f, nil
}
