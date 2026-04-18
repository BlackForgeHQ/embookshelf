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

func (s *ShelfService) List(ctx context.Context, userID string) ([]model.Shelf, error) {
	return s.repo.ListForUser(ctx, userID)
}

func (s *ShelfService) GetBySlug(ctx context.Context, userID, slug string) (model.Shelf, error) {
	return s.repo.GetBySlugForUser(ctx, userID, slug)
}

func (s *ShelfService) Books(ctx context.Context, userID, slug string) ([]model.Book, error) {
	return s.repo.BooksInShelfForUser(ctx, userID, slug)
}

func (s *ShelfService) Create(ctx context.Context, userID, name, accent string) (model.Shelf, error) {
	return s.repo.Create(ctx, userID, name, accent)
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
