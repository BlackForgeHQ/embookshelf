package repo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blackforge/embookshelf/internal/model"
)

type BookDropRepo struct {
	pool *pgxpool.Pool
}

func NewBookDropRepo(pool *pgxpool.Pool) *BookDropRepo {
	return &BookDropRepo{pool: pool}
}

const bdCols = `id, path, file_size, format, state, progress, error_msg,
                title, author, description, language, has_cover, cover_mime, book_id,
                discovered_at, updated_at`

// Insert records a newly-discovered file. Returns the inserted row; if a row
// already exists for that path, returns (existing, ErrAlreadyExists).
var ErrAlreadyExists = errors.New("already exists")

func (r *BookDropRepo) Insert(ctx context.Context, path, format string, size int64) (model.BookDropItem, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO bookdrop_items (path, file_size, format)
		VALUES ($1, $2, $3)
		ON CONFLICT (path) DO NOTHING
		RETURNING `+bdCols, path, size, format)
	item, err := scanBookDrop(row)
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
	row := r.pool.QueryRow(ctx, `SELECT `+bdCols+` FROM bookdrop_items WHERE id = $1`, id)
	return scanBookDrop(row)
}

func (r *BookDropRepo) GetByPath(ctx context.Context, path string) (model.BookDropItem, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+bdCols+` FROM bookdrop_items WHERE path = $1`, path)
	return scanBookDrop(row)
}

func (r *BookDropRepo) List(ctx context.Context) ([]model.BookDropItem, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+bdCols+`
		FROM bookdrop_items
		ORDER BY discovered_at DESC
		LIMIT 500
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectBookDrop(rows)
}

func (r *BookDropRepo) SetState(ctx context.Context, id string, state model.BookDropState, progress int, errorMsg string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE bookdrop_items
		SET state = $2, progress = $3, error_msg = $4, updated_at = now()
		WHERE id = $1
	`, id, string(state), progress, errorMsg)
	return err
}

// SetMetadata records metadata extracted by the fileproc worker and flips the
// item into 'ready' state. cover_mime is empty when no cover was extracted.
func (r *BookDropRepo) SetMetadata(ctx context.Context, id, title, author, description, language string, hasCover bool, coverMime string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE bookdrop_items
		SET title = $2, author = $3, description = $4, language = $5,
		    has_cover = $6, cover_mime = $7,
		    state = 'ready', progress = 100, error_msg = '',
		    updated_at = now()
		WHERE id = $1
	`, id, title, author, description, language, hasCover, coverMime)
	return err
}

// MarkImported links the bookdrop item to the newly-created book row.
func (r *BookDropRepo) MarkImported(ctx context.Context, id, bookID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE bookdrop_items
		SET state = 'imported', progress = 100, book_id = $2, updated_at = now()
		WHERE id = $1
	`, id, bookID)
	return err
}

func scanBookDrop(s scanner) (model.BookDropItem, error) {
	var (
		item  model.BookDropItem
		state string
	)
	err := s.Scan(
		&item.ID, &item.Path, &item.FileSize, &item.Format, &state, &item.Progress, &item.ErrorMsg,
		&item.Title, &item.Author, &item.Description, &item.Language, &item.HasCover, &item.CoverMime, &item.BookID,
		&item.DiscoveredAt, &item.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return item, ErrNotFound
		}
		return item, err
	}
	item.State = model.BookDropState(state)
	return item, nil
}

func collectBookDrop(rows pgx.Rows) ([]model.BookDropItem, error) {
	var out []model.BookDropItem
	for rows.Next() {
		item, err := scanBookDrop(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
