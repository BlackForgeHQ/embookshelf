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

type BookDropRepo struct {
	db *db.DB
}

func NewBookDropRepo(d *db.DB) *BookDropRepo {
	return &BookDropRepo{db: d}
}

const bdCols = `id, path, file_size, format, state, progress, error_msg,
                title, author, description, language, isbn, has_cover, cover_mime, book_id,
                discovered_at, updated_at, content_hash,
                duration_seconds, narrator, chapters`

// Insert records a newly-discovered file. Returns the inserted row; if a row
// already exists for that path, returns (existing, ErrAlreadyExists).
var ErrAlreadyExists = errors.New("already exists")

func (r *BookDropRepo) Insert(ctx context.Context, path, format string, size int64) (model.BookDropItem, error) {
	id := db.NewID()
	const q = `
		INSERT INTO bookdrop_items (id, path, file_size, format)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (path) DO NOTHING
		RETURNING ` + bdCols
	row := r.db.SQL.QueryRowContext(ctx, q, id, path, size, format)
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
	const q = `SELECT ` + bdCols + ` FROM bookdrop_items WHERE id = $1`
	row := r.db.SQL.QueryRowContext(ctx, q, id)
	return r.scanBookDrop(row)
}

func (r *BookDropRepo) GetByPath(ctx context.Context, path string) (model.BookDropItem, error) {
	const q = `SELECT ` + bdCols + ` FROM bookdrop_items WHERE path = $1`
	row := r.db.SQL.QueryRowContext(ctx, q, path)
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
	return collect(rows, nil, r.scanBookDrop)
}

func (r *BookDropRepo) SetState(ctx context.Context, id string, state model.BookDropState, progress int, errorMsg string) error {
	const q = `
		UPDATE bookdrop_items
		SET state = $2, progress = $3, error_msg = $4, updated_at = now()
		WHERE id = $1
	`
	_, err := r.db.SQL.ExecContext(ctx, q, id, string(state), progress, errorMsg)
	return err
}

// SetMetadata records metadata extracted by the fileproc worker and flips the
// item into 'ready' state. cover_mime is empty when no cover was extracted.
func (r *BookDropRepo) SetMetadata(ctx context.Context, id, title, author, description, language, isbn string, hasCover bool, coverMime string) error {
	const q = `
		UPDATE bookdrop_items
		SET title = $2, author = $3, description = $4, language = $5,
		    isbn = $6, has_cover = $7, cover_mime = $8,
		    state = 'ready', progress = 100, error_msg = '',
		    updated_at = now()
		WHERE id = $1
	`
	_, err := r.db.SQL.ExecContext(ctx, q, id, title, author, description, language, isbn, hasCover, coverMime)
	return err
}

// SetCoverPresence flips has_cover + cover_mime for a row without
// otherwise touching state/progress. Used by the user-driven cover
// upload path (BookDropPutCover); ingest's SetMetadata already covers
// the worker-side path.
func (r *BookDropRepo) SetCoverPresence(ctx context.Context, id string, hasCover bool, coverMime string) error {
	const q = `
		UPDATE bookdrop_items
		SET has_cover = $2, cover_mime = $3, updated_at = now()
		WHERE id = $1
	`
	_, err := r.db.SQL.ExecContext(ctx, q, id, hasCover, coverMime)
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
	return collect(rows, nil, func(s scanner) (string, error) {
		var id string
		err := s.Scan(&id)
		return id, err
	})
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
	return collect(rows, nil, func(s scanner) (string, error) {
		var p string
		err := s.Scan(&p)
		return p, err
	})
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
	return collect(rows, nil, func(s scanner) (struct {
		ID   string
		Path string
	}, error) {
		var row struct {
			ID   string
			Path string
		}
		err := s.Scan(&row.ID, &row.Path)
		return row, err
	})
}

// DeleteByID removes a single bookdrop row by id. Used by Wipe's orphan
// sweep — the service layer decides who to delete after stat'ing paths.
func (r *BookDropRepo) DeleteByID(ctx context.Context, id string) error {
	const q = `DELETE FROM bookdrop_items WHERE id = $1`
	_, err := r.db.SQL.ExecContext(ctx, q, id)
	return err
}

// MarkImported links the bookdrop item to the newly-created book row.
func (r *BookDropRepo) MarkImported(ctx context.Context, id, bookID string) error {
	const q = `
		UPDATE bookdrop_items
		SET state = 'imported', progress = 100, book_id = $2, updated_at = now()
		WHERE id = $1
	`
	_, err := r.db.SQL.ExecContext(ctx, q, id, bookID)
	return err
}

func (r *BookDropRepo) scanBookDrop(s scanner) (model.BookDropItem, error) {
	var (
		item        model.BookDropItem
		state       string
		durationAny any
		chaptersAny any
	)
	err := s.Scan(
		&item.ID, &item.Path, &item.FileSize, &item.Format, &state, &item.Progress, &item.ErrorMsg,
		&item.Title, &item.Author, &item.Description, &item.Language, &item.ISBN, &item.HasCover, &item.CoverMime, &item.BookID,
		&item.DiscoveredAt, &item.UpdatedAt, &item.ContentHash,
		&durationAny, &item.Narrator, &chaptersAny,
	)
	if err != nil {
		if dberr.IsNotFound(err) {
			return item, ErrNotFound
		}
		return item, err
	}
	item.State = model.BookDropState(state)
	if v, ok := durationAny.(int64); ok {
		n := int(v)
		item.DurationSeconds = &n
	}
	// chapters: JSONB. NULL → nil slice.
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
	const q = `
		UPDATE bookdrop_items
		SET duration_seconds = $2, narrator = $3, chapters = $4,
		    updated_at = now()
		WHERE id = $1
	`
	return execOne(ctx, r.db.SQL, q, itemID, durationSeconds, narrator, chaptersVal)
}

// SetContentHash records the sha256 computed during ingest.
// []byte binds natively to BYTEA.
func (r *BookDropRepo) SetContentHash(ctx context.Context, itemID string, hash []byte) error {
	const q = `
		UPDATE bookdrop_items
		SET content_hash = $2, updated_at = now()
		WHERE id = $1
	`
	return execOne(ctx, r.db.SQL, q, itemID, hash)
}
