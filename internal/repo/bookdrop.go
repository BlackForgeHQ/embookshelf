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

type BookDropRepo struct {
	db *db.DB
}

func NewBookDropRepo(d *db.DB) *BookDropRepo {
	return &BookDropRepo{db: d}
}

const bdCols = `id, path, file_size, format, state, progress, error_msg,
                title, author, description, language, has_cover, cover_mime, book_id,
                discovered_at, updated_at, content_hash,
                duration_seconds, narrator, chapters`

// Insert records a newly-discovered file. Returns the inserted row; if a row
// already exists for that path, returns (existing, ErrAlreadyExists).
var ErrAlreadyExists = errors.New("already exists")

func (r *BookDropRepo) Insert(ctx context.Context, path, format string, size int64) (model.BookDropItem, error) {
	id := db.NewID()
	const qPG = `
		INSERT INTO bookdrop_items (id, path, file_size, format)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (path) DO NOTHING
		RETURNING ` + bdCols
	const qSQLite = `
		INSERT INTO bookdrop_items (id, path, file_size, format)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (path) DO NOTHING
		RETURNING ` + bdCols
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), id, path, size, format)
	item, err := r.scanBookDrop(row)
	if errors.Is(err, ErrNotFound) {
		// ON CONFLICT DO NOTHING returned no rows — row already existed, fetch it.
		existing, gerr := r.GetByPath(ctx, path)
		if gerr != nil {
			return existing, gerr
		}
		return existing, ErrAlreadyExists
	}
	return item, err
}

func (r *BookDropRepo) GetByID(ctx context.Context, id string) (model.BookDropItem, error) {
	const qPG = `SELECT ` + bdCols + ` FROM bookdrop_items WHERE id = $1`
	const qSQLite = `SELECT ` + bdCols + ` FROM bookdrop_items WHERE id = ?`
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), id)
	return r.scanBookDrop(row)
}

func (r *BookDropRepo) GetByPath(ctx context.Context, path string) (model.BookDropItem, error) {
	const qPG = `SELECT ` + bdCols + ` FROM bookdrop_items WHERE path = $1`
	const qSQLite = `SELECT ` + bdCols + ` FROM bookdrop_items WHERE path = ?`
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), path)
	return r.scanBookDrop(row)
}

func (r *BookDropRepo) List(ctx context.Context) ([]model.BookDropItem, error) {
	rows, err := r.db.SQL.QueryContext(ctx, `
		SELECT `+bdCols+`
		FROM bookdrop_items
		ORDER BY discovered_at DESC
		LIMIT 500
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return r.collectBookDrop(rows)
}

func (r *BookDropRepo) SetState(ctx context.Context, id string, state model.BookDropState, progress int, errorMsg string) error {
	const qPG = `
		UPDATE bookdrop_items
		SET state = $2, progress = $3, error_msg = $4, updated_at = now()
		WHERE id = $1
	`
	const qSQLite = `
		UPDATE bookdrop_items
		SET state = ?, progress = ?, error_msg = ?, updated_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		WHERE id = ?
	`
	if r.db.Dialect == db.DialectSQLite {
		_, err := r.db.SQL.ExecContext(ctx, qSQLite, string(state), progress, errorMsg, id)
		return err
	}
	_, err := r.db.SQL.ExecContext(ctx, qPG, id, string(state), progress, errorMsg)
	return err
}

// SetMetadata records metadata extracted by the fileproc worker and flips the
// item into 'ready' state. cover_mime is empty when no cover was extracted.
func (r *BookDropRepo) SetMetadata(ctx context.Context, id, title, author, description, language string, hasCover bool, coverMime string) error {
	const qPG = `
		UPDATE bookdrop_items
		SET title = $2, author = $3, description = $4, language = $5,
		    has_cover = $6, cover_mime = $7,
		    state = 'ready', progress = 100, error_msg = '',
		    updated_at = now()
		WHERE id = $1
	`
	const qSQLite = `
		UPDATE bookdrop_items
		SET title = ?, author = ?, description = ?, language = ?,
		    has_cover = ?, cover_mime = ?,
		    state = 'ready', progress = 100, error_msg = '',
		    updated_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		WHERE id = ?
	`
	if r.db.Dialect == db.DialectSQLite {
		_, err := r.db.SQL.ExecContext(ctx, qSQLite, title, author, description, language, hasCover, coverMime, id)
		return err
	}
	_, err := r.db.SQL.ExecContext(ctx, qPG, id, title, author, description, language, hasCover, coverMime)
	return err
}

// DeleteProcessed removes every bookdrop row in a terminal state
// ('imported' or 'rejected'). Returns the ids of the deleted rows so the
// caller can clean up any lingering cover files off-DB. Active-state rows
// (discovered/processing/ready/failed) are untouched — clearing those
// would drop in-flight work.
func (r *BookDropRepo) DeleteProcessed(ctx context.Context) ([]string, error) {
	rows, err := r.db.SQL.QueryContext(ctx, `
		DELETE FROM bookdrop_items
		WHERE state IN ('imported','rejected')
		RETURNING id
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ProcessingPaths returns the file paths of every row currently in 'processing'
// state. The wipe op needs this set to skip files an extractor is actively
// reading — deleting them mid-extract leaves a row stuck in 'processing'
// pointing at vanished bytes.
func (r *BookDropRepo) ProcessingPaths(ctx context.Context) ([]string, error) {
	rows, err := r.db.SQL.QueryContext(ctx, `
		SELECT path FROM bookdrop_items WHERE state = 'processing'
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListNonProcessing returns id+path for every bookdrop row not in
// 'processing' state. Used by Wipe so the service layer can stat each
// path and decide which rows are now orphaned.
func (r *BookDropRepo) ListNonProcessing(ctx context.Context) ([]struct {
	ID   string
	Path string
}, error) {
	rows, err := r.db.SQL.QueryContext(ctx, `
		SELECT id, path FROM bookdrop_items WHERE state <> 'processing'
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []struct {
		ID   string
		Path string
	}
	for rows.Next() {
		var r struct {
			ID   string
			Path string
		}
		if err := rows.Scan(&r.ID, &r.Path); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteByID removes a single bookdrop row by id. Used by Wipe's orphan
// sweep — the service layer decides who to delete after stat'ing paths.
func (r *BookDropRepo) DeleteByID(ctx context.Context, id string) error {
	const qPG = `DELETE FROM bookdrop_items WHERE id = $1`
	const qSQLite = `DELETE FROM bookdrop_items WHERE id = ?`
	_, err := r.db.SQL.ExecContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), id)
	return err
}

// MarkImported links the bookdrop item to the newly-created book row.
func (r *BookDropRepo) MarkImported(ctx context.Context, id, bookID string) error {
	const qPG = `
		UPDATE bookdrop_items
		SET state = 'imported', progress = 100, book_id = $2, updated_at = now()
		WHERE id = $1
	`
	const qSQLite = `
		UPDATE bookdrop_items
		SET state = 'imported', progress = 100, book_id = ?, updated_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		WHERE id = ?
	`
	if r.db.Dialect == db.DialectSQLite {
		_, err := r.db.SQL.ExecContext(ctx, qSQLite, bookID, id)
		return err
	}
	_, err := r.db.SQL.ExecContext(ctx, qPG, id, bookID)
	return err
}

func (r *BookDropRepo) scanBookDrop(s scanner) (model.BookDropItem, error) {
	var (
		item          model.BookDropItem
		state         string
		discoveredAny any
		updatedAny    any
		durationAny   any
		chaptersAny   any
	)
	err := s.Scan(
		&item.ID, &item.Path, &item.FileSize, &item.Format, &state, &item.Progress, &item.ErrorMsg,
		&item.Title, &item.Author, &item.Description, &item.Language, &item.HasCover, &item.CoverMime, &item.BookID,
		&discoveredAny, &updatedAny, &item.ContentHash,
		&durationAny, &item.Narrator, &chaptersAny,
	)
	if err != nil {
		if dberr.IsNotFound(err) {
			return item, ErrNotFound
		}
		return item, err
	}
	item.State = model.BookDropState(state)
	if err := db.ScanTime(r.db.Dialect, discoveredAny, &item.DiscoveredAt); err != nil {
		return item, fmt.Errorf("scan discovered_at: %w", err)
	}
	if err := db.ScanTime(r.db.Dialect, updatedAny, &item.UpdatedAt); err != nil {
		return item, fmt.Errorf("scan updated_at: %w", err)
	}
	if v, ok := durationAny.(int64); ok {
		n := int(v)
		item.DurationSeconds = &n
	}
	// chapters: TEXT JSON on SQLite, JSONB on PG. NULL → nil slice.
	if chaptersAny != nil {
		var raw []byte
		switch v := chaptersAny.(type) {
		case []byte:
			raw = v
		case string:
			raw = []byte(v)
		}
		if len(raw) > 0 {
			var ch []model.Chapter
			if err := json.Unmarshal(raw, &ch); err == nil && len(ch) > 0 {
				item.Chapters = ch
			}
		}
	}
	return item, nil
}

// SetAudio writes the audiobook fields extracted at ingest. nil
// chapters writes SQL NULL so the UI can distinguish "no chapter
// metadata" from "empty chapter list". Approve carries these straight
// to the books row, removing the need for a re-extract pass post-Place.
func (r *BookDropRepo) SetAudio(
	ctx context.Context,
	itemID string,
	durationSeconds *int,
	narrator string,
	chapters []model.Chapter,
) error {
	var chaptersVal any
	if chapters != nil {
		b, err := json.Marshal(chapters)
		if err != nil {
			return fmt.Errorf("encode chapters: %w", err)
		}
		chaptersVal = string(b)
	}
	const qPG = `
		UPDATE bookdrop_items
		SET duration_seconds = $2, narrator = $3, chapters = $4,
		    updated_at = now()
		WHERE id = $1
	`
	const qSQLite = `
		UPDATE bookdrop_items
		SET duration_seconds = ?, narrator = ?, chapters = ?,
		    updated_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		WHERE id = ?
	`
	var res sql.Result
	var err error
	if r.db.Dialect == db.DialectSQLite {
		res, err = r.db.SQL.ExecContext(ctx, qSQLite, durationSeconds, narrator, chaptersVal, itemID)
	} else {
		res, err = r.db.SQL.ExecContext(ctx, qPG, itemID, durationSeconds, narrator, chaptersVal)
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

// SetContentHash records the sha256 computed during ingest.
// PG and SQLite both bind []byte natively to BYTEA / BLOB.
func (r *BookDropRepo) SetContentHash(ctx context.Context, itemID string, hash []byte) error {
	const qPG = `
		UPDATE bookdrop_items
		SET content_hash = $2, updated_at = now()
		WHERE id = $1
	`
	const qSQLite = `
		UPDATE bookdrop_items
		SET content_hash = ?, updated_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		WHERE id = ?
	`
	var res sql.Result
	var err error
	if r.db.Dialect == db.DialectSQLite {
		res, err = r.db.SQL.ExecContext(ctx, qSQLite, hash, itemID)
	} else {
		res, err = r.db.SQL.ExecContext(ctx, qPG, itemID, hash)
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

func (r *BookDropRepo) collectBookDrop(rows *sql.Rows) ([]model.BookDropItem, error) {
	var out []model.BookDropItem
	for rows.Next() {
		item, err := r.scanBookDrop(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
