package service

import (
	"context"
	"errors"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/blackforge/embookshelf/internal/repo"
)

// SearchService fans out the three suggest queries that power the command
// palette and the library page combobox. Each result group is independent;
// they run concurrently and an error in any cancels the others.
type SearchService struct {
	lib   *repo.LibraryRepo
	books *repo.BookRepo
	shelf *repo.ShelfRepo
}

// SuggestResult is the slim payload returned to the HTTP layer. The handler
// projects each row into its wire DTO; this struct is package-internal
// shape only.
type SuggestResult struct {
	Books     []repo.SuggestBook
	Shelves   []repo.SuggestShelf
	Libraries []repo.SuggestLibrary
}

// ErrEmptyQuery is returned when the trimmed query is empty.
var ErrEmptyQuery = errors.New("search: query is required")

const (
	defaultSuggestLimit = 8
	maxSuggestLimit     = 20
)

func NewSearchService(lib *repo.LibraryRepo, books *repo.BookRepo, shelf *repo.ShelfRepo) *SearchService {
	return &SearchService{lib: lib, books: books, shelf: shelf}
}

// Suggest runs the three repo queries in parallel and assembles the
// result. `limit` is clamped to [1, 20]; <=0 falls back to the default.
func (s *SearchService) Suggest(ctx context.Context, userID, q string, limit int) (SuggestResult, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return SuggestResult{}, ErrEmptyQuery
	}
	if limit <= 0 {
		limit = defaultSuggestLimit
	}
	if limit > maxSuggestLimit {
		limit = maxSuggestLimit
	}

	var result SuggestResult
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		books, err := s.books.SearchSuggest(ctx, q, limit)
		if err != nil {
			return err
		}
		result.Books = books
		return nil
	})
	g.Go(func() error {
		shelves, err := s.shelf.SearchSuggest(ctx, userID, q, limit)
		if err != nil {
			return err
		}
		result.Shelves = shelves
		return nil
	})
	g.Go(func() error {
		libs, err := s.lib.SearchSuggest(ctx, q, limit)
		if err != nil {
			return err
		}
		result.Libraries = libs
		return nil
	})
	if err := g.Wait(); err != nil {
		return SuggestResult{}, err
	}
	return result, nil
}
