package service

import (
	"context"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

type LibraryService struct {
	repo *repo.LibraryRepo
}

func NewLibraryService(r *repo.LibraryRepo) *LibraryService {
	return &LibraryService{repo: r}
}

func (s *LibraryService) List(ctx context.Context) ([]model.Library, error) {
	return s.repo.List(ctx)
}

func (s *LibraryService) Books(ctx context.Context, userID, librarySlug string) ([]model.Book, error) {
	return s.repo.BooksByLibrarySlug(ctx, userID, librarySlug)
}

func (s *LibraryService) Search(ctx context.Context, userID, librarySlug string, p model.SearchParams) ([]model.Book, error) {
	return s.repo.Search(ctx, userID, librarySlug, p)
}

func (s *LibraryService) GetBook(ctx context.Context, userID, id string) (model.Book, error) {
	return s.repo.GetBookByID(ctx, userID, id)
}

func (s *LibraryService) UpdateBookMetadata(ctx context.Context, b model.Book) error {
	return s.repo.UpdateMetadata(ctx, b)
}

// DeleteBook hard-deletes a book. FKs on shelf_books, annotations,
// user_book_progress, and reading_sessions cascade in the DB; cover art
// and the source file on disk are the caller's responsibility — the
// service stays out of the filesystem to keep this layer testable
// without a mounted library root.
func (s *LibraryService) DeleteBook(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

// BookExistsByPath reports whether any non-deleted book already references
// this on-disk path. Used by the library scanner to avoid re-queuing files
// that are already in the library.
func (s *LibraryService) BookExistsByPath(ctx context.Context, path string) (bool, error) {
	return s.repo.BookExistsByPath(ctx, path)
}
