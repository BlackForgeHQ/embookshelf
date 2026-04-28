package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/db/dberr"
	"github.com/blackforge/embookshelf/internal/model"
)

type AnnotationRepo struct {
	db *db.DB
}

func NewAnnotationRepo(db *db.DB) *AnnotationRepo {
	return &AnnotationRepo{db: db}
}

const annotationCols = `
    a.id, a.user_id, a.book_id, a.locator,
    a.selected_text, a.note, a.color,
    a.created_at, a.updated_at
`

// ListForBook returns every annotation the user has on a single book,
// ordered by creation time ascending so the client list reads like a
// chronological reading log.
func (r *AnnotationRepo) ListForBook(ctx context.Context, userID, bookID string) ([]model.Annotation, error) {
	rows, err := r.db.SQL.QueryContext(ctx, `
        SELECT `+annotationCols+`
        FROM annotations a
        WHERE a.user_id = $1 AND a.book_id = $2
        ORDER BY a.created_at ASC
    `, userID, bookID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectAnnotations(rows)
}

// ListRecent returns every annotation the user has across every book,
// newest first. Used by the Notebook view.
func (r *AnnotationRepo) ListRecent(ctx context.Context, userID string, limit int) ([]model.Annotation, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := r.db.SQL.QueryContext(ctx, `
        SELECT `+annotationCols+`
        FROM annotations a
        WHERE a.user_id = $1
        ORDER BY a.created_at DESC
        LIMIT $2
    `, userID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectAnnotations(rows)
}

// Get returns a single annotation, scoped to the owner. Callers are
// expected to pass the session user's id so a user can't PATCH/DELETE
// another user's row even if they guess the uuid.
func (r *AnnotationRepo) Get(ctx context.Context, userID, id string) (model.Annotation, error) {
	row := r.db.SQL.QueryRowContext(ctx, `
        SELECT `+annotationCols+`
        FROM annotations a
        WHERE a.user_id = $1 AND a.id = $2
    `, userID, id)
	return scanAnnotation(row)
}

// Create inserts a new annotation and returns the hydrated row.
// Uses a WITH/RETURNING wrapper so the shared annotationCols SELECT list
// is reusable (the bare INSERT RETURNING form doesn't let us alias as `a`).
func (r *AnnotationRepo) Create(ctx context.Context, a model.Annotation) (model.Annotation, error) {
	if a.Color == "" {
		a.Color = "accent"
	}
	row := r.db.SQL.QueryRowContext(ctx, `
        WITH inserted AS (
            INSERT INTO annotations (user_id, book_id, locator, selected_text, note, color)
            VALUES ($1, $2, $3, $4, $5, $6)
            RETURNING *
        )
        SELECT `+annotationCols+`
        FROM inserted a
    `, a.UserID, a.BookID, a.Locator, a.SelectedText, a.Note, a.Color)
	return scanAnnotation(row)
}

// Update applies optional-field edits. Zero-length *string pointers are
// used in the service to distinguish "don't touch" from "set to empty".
// Kept narrow here: the three fields a user actually edits from the UI.
func (r *AnnotationRepo) Update(ctx context.Context, userID, id string, note, selectedText, color *string) (model.Annotation, error) {
	// Build the SET list dynamically so unset pointers stay untouched.
	var (
		sets []string
		args = []any{userID, id}
	)
	if note != nil {
		args = append(args, *note)
		sets = append(sets, "note = $"+strconv.Itoa(len(args)))
	}
	if selectedText != nil {
		args = append(args, *selectedText)
		sets = append(sets, "selected_text = $"+strconv.Itoa(len(args)))
	}
	if color != nil {
		args = append(args, *color)
		sets = append(sets, "color = $"+strconv.Itoa(len(args)))
	}
	if len(sets) == 0 {
		// Nothing to patch → return current row.
		return r.Get(ctx, userID, id)
	}

	query := `
        UPDATE annotations
        SET ` + strings.Join(sets, ", ") + `, updated_at = now()
        WHERE user_id = $1 AND id = $2
        RETURNING ` + annotationCols + `
    `
	row := r.db.SQL.QueryRowContext(ctx, query, args...)
	a, err := scanAnnotation(row)
	if dberr.IsNotFound(err) {
		return model.Annotation{}, ErrNotFound
	}
	return a, err
}

// Delete removes one annotation. Scoping by user_id prevents a user
// from deleting someone else's row even if they fish the uuid out.
func (r *AnnotationRepo) Delete(ctx context.Context, userID, id string) error {
	res, err := r.db.SQL.ExecContext(ctx, `
        DELETE FROM annotations WHERE user_id = $1 AND id = $2
    `, userID, id)
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

// scanAnnotation hydrates a sql row into the model shape. Mirrors the
// scanBook / scanShelf pattern.
func scanAnnotation(s scanner) (model.Annotation, error) {
	var a model.Annotation
	err := s.Scan(
		&a.ID, &a.UserID, &a.BookID, &a.Locator,
		&a.SelectedText, &a.Note, &a.Color,
		&a.CreatedAt, &a.UpdatedAt,
	)
	if dberr.IsNotFound(err) {
		return model.Annotation{}, ErrNotFound
	}
	return a, err
}

func collectAnnotations(rows *sql.Rows) ([]model.Annotation, error) {
	out := make([]model.Annotation, 0)
	for rows.Next() {
		a, err := scanAnnotation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
