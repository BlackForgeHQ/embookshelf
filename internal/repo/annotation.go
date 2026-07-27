// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
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

// annotationReturning is annotationCols stripped of the `a.` alias —
// used by INSERT ... RETURNING and UPDATE ... RETURNING where there's
// no FROM clause to alias the table.
const annotationReturning = `
    id, user_id, book_id, locator,
    selected_text, note, color,
    created_at, updated_at
`

// ListForBook returns every annotation the user has on a single book,
// ordered by creation time ascending so the client list reads like a
// chronological reading log.
func (r *AnnotationRepo) ListForBook(ctx context.Context, userID, bookID string) ([]model.Annotation, error) {
	const q = `
        SELECT ` + annotationCols + `
        FROM annotations a
        WHERE a.user_id = $1 AND a.book_id = $2
        ORDER BY a.created_at ASC
    `
	rows, err := r.db.SQL.QueryContext(ctx, q, userID, bookID)
	if err != nil {
		return nil, err
	}
	return collect(rows, make([]model.Annotation, 0), r.scanAnnotation)
}

// ListRecent returns every annotation the user has across every book,
// newest first. Used by the Notebook view.
func (r *AnnotationRepo) ListRecent(ctx context.Context, userID string, limit int) ([]model.Annotation, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	const q = `
        SELECT ` + annotationCols + `
        FROM annotations a
        WHERE a.user_id = $1
        ORDER BY a.created_at DESC
        LIMIT $2
    `
	rows, err := r.db.SQL.QueryContext(ctx, q, userID, limit)
	if err != nil {
		return nil, err
	}
	return collect(rows, make([]model.Annotation, 0), r.scanAnnotation)
}

// Get returns a single annotation, scoped to the owner. Callers are
// expected to pass the session user's id so a user can't PATCH/DELETE
// another user's row even if they guess the uuid.
func (r *AnnotationRepo) Get(ctx context.Context, userID, id string) (model.Annotation, error) {
	const q = `
        SELECT ` + annotationCols + `
        FROM annotations a
        WHERE a.user_id = $1 AND a.id = $2
    `
	row := r.db.SQL.QueryRowContext(ctx, q, userID, id)
	return r.scanAnnotation(row)
}

// Create inserts a new annotation and returns the hydrated row.
// UUID is generated app-side via db.NewID() so the id is known to the
// caller before the round-trip completes.
func (r *AnnotationRepo) Create(ctx context.Context, a model.Annotation) (model.Annotation, error) {
	if a.Color == "" {
		a.Color = "accent"
	}
	id := db.NewID()
	const q = `
        INSERT INTO annotations (id, user_id, book_id, locator, selected_text, note, color)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
        RETURNING ` + annotationReturning
	row := r.db.SQL.QueryRowContext(ctx, q,
		id, a.UserID, a.BookID, a.Locator, a.SelectedText, a.Note, a.Color)
	return r.scanAnnotation(row)
}

// Update applies optional-field edits. Zero-length *string pointers are
// used in the service to distinguish "don't touch" from "set to empty".
// Kept narrow here: the three fields a user actually edits from the UI.
func (r *AnnotationRepo) Update(ctx context.Context, userID, id string, note, selectedText, color *string) (model.Annotation, error) {
	// Build the SET list dynamically so unset pointers stay untouched.
	if note == nil && selectedText == nil && color == nil {
		// Nothing to patch → return current row.
		return r.Get(ctx, userID, id)
	}

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

	query := `
        UPDATE annotations
        SET ` + strings.Join(sets, ", ") + `, updated_at = now()
        WHERE user_id = $1 AND id = $2
        RETURNING ` + annotationReturning
	row := r.db.SQL.QueryRowContext(ctx, query, args...)
	a, err := r.scanAnnotation(row)
	if dberr.IsNotFound(err) {
		return model.Annotation{}, ErrNotFound
	}
	return a, err
}

// Delete removes one annotation. Scoping by user_id prevents a user
// from deleting someone else's row even if they fish the uuid out.
func (r *AnnotationRepo) Delete(ctx context.Context, userID, id string) error {
	const q = `
        DELETE FROM annotations WHERE user_id = $1 AND id = $2
    `
	return execOne(ctx, r.db.SQL, q, userID, id)
}

// scanAnnotation hydrates a sql row into the model shape. Mirrors the
// scanBook / scanShelf pattern.
func (r *AnnotationRepo) scanAnnotation(s scanner) (model.Annotation, error) {
	var a model.Annotation
	err := s.Scan(
		&a.ID, &a.UserID, &a.BookID, &a.Locator,
		&a.SelectedText, &a.Note, &a.Color,
		&a.CreatedAt, &a.UpdatedAt,
	)
	if dberr.IsNotFound(err) {
		return model.Annotation{}, ErrNotFound
	}
	if err != nil {
		return model.Annotation{}, err
	}
	return a, nil
}
