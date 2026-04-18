package service

import (
	"context"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

type ShelfService struct {
	repo *repo.ShelfRepo
}

func NewShelfService(r *repo.ShelfRepo) *ShelfService {
	return &ShelfService{repo: r}
}

// List returns every shelf the user owns and fills in the live
// book_count for smart shelves (the repo emits 0 for those so we don't
// embed rule evaluation inside a correlated subquery).
func (s *ShelfService) List(ctx context.Context, userID string) ([]model.Shelf, error) {
	shelves, err := s.repo.ListForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range shelves {
		if !shelves[i].IsSmart || shelves[i].Rule == nil {
			continue
		}
		n, err := s.repo.CountForSmartShelf(ctx, userID, shelves[i].Rule)
		if err != nil {
			// A broken rule shouldn't 500 the whole sidebar — leave
			// BookCount at 0 and continue.
			continue
		}
		shelves[i].BookCount = n
	}
	return shelves, nil
}

func (s *ShelfService) GetBySlug(ctx context.Context, userID, slug string) (model.Shelf, error) {
	return s.repo.GetBySlugForUser(ctx, userID, slug)
}

func (s *ShelfService) Books(ctx context.Context, userID, slug string) ([]model.Book, error) {
	return s.repo.BooksInShelfForUser(ctx, userID, slug)
}

// Create accepts an optional rule; nil creates a regular shelf and a
// non-nil rule creates a smart shelf (validated up-front).
func (s *ShelfService) Create(ctx context.Context, userID, name, accent string, rule *model.ShelfRule) (model.Shelf, error) {
	if rule != nil {
		if err := rule.Validate(); err != nil {
			return model.Shelf{}, err
		}
	}
	return s.repo.Create(ctx, userID, name, accent, rule)
}

// Update edits a shelf's name, accent, and/or rule. Nil pointers are
// untouched. ruleChanged disambiguates "don't touch the rule" from
// "replace with nil" — the latter is rejected at the repo layer for
// smart shelves.
func (s *ShelfService) Update(ctx context.Context, userID, slug string, name, accent *string, rule *model.ShelfRule, ruleChanged bool) (model.Shelf, error) {
	if ruleChanged && rule != nil {
		if err := rule.Validate(); err != nil {
			return model.Shelf{}, err
		}
	}
	return s.repo.Update(ctx, userID, slug, name, accent, rule, ruleChanged)
}

func (s *ShelfService) Delete(ctx context.Context, userID, slug string) error {
	return s.repo.Delete(ctx, userID, slug)
}

func (s *ShelfService) AddBook(ctx context.Context, userID, slug, bookID string) error {
	return s.repo.AddBook(ctx, userID, slug, bookID)
}

func (s *ShelfService) RemoveBook(ctx context.Context, userID, slug, bookID string) error {
	return s.repo.RemoveBook(ctx, userID, slug, bookID)
}

func (s *ShelfService) SlugsForBook(ctx context.Context, userID, bookID string) ([]string, error) {
	return s.repo.ShelfSlugsForBook(ctx, userID, bookID)
}
