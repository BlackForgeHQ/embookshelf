package service

import (
	"context"
	"strings"

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

// Create inserts a new library row. The slug is derived from the name so the
// URL space stays predictable; uniqueness is enforced at the DB layer, and
// the repo surfaces a 409-worthy sentinel (ErrLibraryNameTaken) on collision.
func (s *LibraryService) Create(ctx context.Context, name string) (model.Library, error) {
	name = strings.TrimSpace(name)
	return s.repo.CreateLibrary(ctx, name, slugify(name))
}

// GetByID returns a single library (with its naming pattern) by id.
func (s *LibraryService) GetByID(ctx context.Context, id string) (model.Library, error) {
	return s.repo.GetByID(ctx, id)
}

// SetFileNamingPattern stores (or clears) the per-library template used by
// the bookdrop approval flow. An empty trimmed string clears the pattern so
// the library falls back to keeping the original filename on disk.
func (s *LibraryService) SetFileNamingPattern(ctx context.Context, id string, pattern *string) error {
	if pattern != nil {
		trimmed := strings.TrimSpace(*pattern)
		if trimmed == "" {
			pattern = nil
		} else {
			pattern = &trimmed
		}
	}
	return s.repo.SetFileNamingPattern(ctx, id, pattern)
}

// slugify collapses a human-readable name into a URL-safe slug:
// lowercase ASCII alphanumerics pass through, everything else (spaces,
// punctuation, non-ASCII) becomes a single '-'. Leading/trailing dashes are
// trimmed. Not perfect for non-Latin scripts — admins picking those names
// will see an empty slug and can retry with something portable.
func slugify(name string) string {
	lower := strings.ToLower(name)
	var b strings.Builder
	b.Grow(len(lower))
	dash := true
	for _, r := range lower {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
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
