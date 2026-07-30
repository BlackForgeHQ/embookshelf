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

// annotationProjection is the annotations row, declared once. The
// aliased SELECT list, the RETURNING clause and the Scan destinations
// all render from here.
//
// The table carries the sharpest instance of the Column-order coupling
// hazard: selected_text, note and color are three adjacent TEXT columns,
// so swapping two of them in any one of the three hand-kept lists
// compiled, ran, and crossed every annotation's text with its colour.
// Stating a column's position and its destination in a single value
// makes that swap unrepresentable.
//
// No entry carries an `arg`: Update patches an optional subset of three
// fields rather than writing the whole row, so it names its columns
// inline instead of walking updateSet.
var annotationProjection = projection[model.Annotation]{
	{name: "id", dest: func(a *model.Annotation) any { return &a.ID }},
	{name: "user_id", dest: func(a *model.Annotation) any { return &a.UserID }},
	{name: "book_id", dest: func(a *model.Annotation) any { return &a.BookID }},
	{name: "locator", dest: func(a *model.Annotation) any { return &a.Locator }},
	{name: "selected_text", dest: func(a *model.Annotation) any { return &a.SelectedText }},
	{name: "note", dest: func(a *model.Annotation) any { return &a.Note }},
	{name: "color", dest: func(a *model.Annotation) any { return &a.Color }},
	{name: "created_at", dest: func(a *model.Annotation) any { return &a.CreatedAt }},
	{name: "updated_at", dest: func(a *model.Annotation) any { return &a.UpdatedAt }},
}

var (
	// annotationCols is the projection rendered for the read queries,
	// which alias the table as `a`.
	annotationCols = annotationProjection.selectList("a")
	// annotationReturning is the same projection with no alias in
	// scope — INSERT ... RETURNING and UPDATE ... RETURNING have no
	// FROM clause to alias the table.
	annotationReturning = annotationProjection.returningList("annotations")
)

// ListForBook returns every annotation the user has on a single book,
// ordered by creation time ascending so the client list reads like a
// chronological reading log.
func (r *AnnotationRepo) ListForBook(ctx context.Context, userID, bookID string) ([]model.Annotation, error) {
	q := `
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
	q := `
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
	q := `
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
	// The INSERT's own column list stays outside the projection: it
	// names the insertable subset, which is a different membership
	// question. Create's round-trip test guards it.
	q := `
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
// scanBook / scanShelf pattern: the destinations come from the
// projection, in the order it declares.
func (r *AnnotationRepo) scanAnnotation(s scanner) (model.Annotation, error) {
	var a model.Annotation
	err := annotationProjection.scan(s, &a)
	if dberr.IsNotFound(err) {
		return model.Annotation{}, ErrNotFound
	}
	if err != nil {
		return model.Annotation{}, err
	}
	return a, nil
}
