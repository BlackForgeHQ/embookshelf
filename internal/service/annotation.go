// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"strings"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// ErrEmptyAnnotation guards the "neither highlight nor note" case so the
// CHECK constraint never fires as a raw SQL error in the handler.
var ErrEmptyAnnotation = errors.New("annotation must include a highlight or a note")

type AnnotationService struct {
	repo *repo.AnnotationRepo
}

func NewAnnotationService(r *repo.AnnotationRepo) *AnnotationService {
	return &AnnotationService{repo: r}
}

func (s *AnnotationService) ListForBook(ctx context.Context, userID, bookID string) ([]model.Annotation, error) {
	return s.repo.ListForBook(ctx, userID, bookID)
}

// ListRecent is capped inside the repo; pass 0 to take the default.
func (s *AnnotationService) ListRecent(ctx context.Context, userID string, limit int) ([]model.Annotation, error) {
	return s.repo.ListRecent(ctx, userID, limit)
}

// Create inserts a new annotation. The caller is responsible for
// scoping user_id / book_id — the service does no ACL check beyond
// what the repo enforces via user_id on every query.
func (s *AnnotationService) Create(ctx context.Context, a model.Annotation) (model.Annotation, error) {
	a.Locator = strings.TrimSpace(a.Locator)
	a.SelectedText = strings.TrimSpace(a.SelectedText)
	a.Note = strings.TrimSpace(a.Note)
	if a.SelectedText == "" && a.Note == "" {
		return model.Annotation{}, ErrEmptyAnnotation
	}
	return s.repo.Create(ctx, a)
}

// Update is a narrow patch — only note text, selected text, and color
// are ever editable after creation. Locator is immutable; the right
// action for a misplaced highlight is delete + re-create.
func (s *AnnotationService) Update(ctx context.Context, userID, id string, note, selectedText, color *string) (model.Annotation, error) {
	// Guard against the CHECK constraint by peeking at what the row
	// would look like after applying the patch.
	if note != nil || selectedText != nil {
		cur, err := s.repo.Get(ctx, userID, id)
		if err != nil {
			return model.Annotation{}, err
		}
		effNote := cur.Note
		effSel := cur.SelectedText
		if note != nil {
			effNote = strings.TrimSpace(*note)
			note = &effNote
		}
		if selectedText != nil {
			effSel = strings.TrimSpace(*selectedText)
			selectedText = &effSel
		}
		if effNote == "" && effSel == "" {
			return model.Annotation{}, ErrEmptyAnnotation
		}
	}
	return s.repo.Update(ctx, userID, id, note, selectedText, color)
}

func (s *AnnotationService) Delete(ctx context.Context, userID, id string) error {
	return s.repo.Delete(ctx, userID, id)
}
