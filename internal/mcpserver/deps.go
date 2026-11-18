// SPDX-License-Identifier: AGPL-3.0-or-later

package mcpserver

import (
	"context"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
)

// Deps are the narrow views of the services the tools read through.
// Interfaces rather than the concrete services so every tool is
// reachable in a test with fakes.
type Deps struct {
	Books       BookCatalog
	Libraries   LibraryLister
	Shelves     ShelfReader
	Search      Suggester
	Annotations AnnotationReader
	Guides      GuideReader
	Text        TextSource
	// Version is reported to clients in the initialize handshake.
	Version string
}

// BookCatalog is the user-scoped book read the HTTP API's book seam uses.
type BookCatalog interface {
	GetByID(ctx context.Context, userID, id string) (model.Book, error)
	Search(ctx context.Context, userID, librarySlug string, p model.SearchParams) ([]model.Book, int, error)
}

type LibraryLister interface {
	List(ctx context.Context) ([]model.Library, error)
}

type ShelfReader interface {
	List(ctx context.Context, userID string) ([]model.Shelf, error)
	Books(ctx context.Context, userID, slug, sort string) ([]model.Book, error)
	SlugsForBook(ctx context.Context, userID, bookID string) ([]string, error)
}

type Suggester interface {
	Suggest(ctx context.Context, userID, q string, limit int) (service.SuggestResult, error)
}

type AnnotationReader interface {
	ListForBook(ctx context.Context, userID, bookID string) ([]model.Annotation, error)
	ListRecent(ctx context.Context, userID string, limit int) ([]model.Annotation, error)
}

type GuideReader interface {
	GetByBookID(ctx context.Context, bookID string) (model.ReadingGuide, error)
}

// TextSource is service.BookText: a book's text, its outline and search
// over it, all in the same offset coordinates.
type TextSource interface {
	Read(ctx context.Context, book model.Book, limit int64) (string, bool, error)
	Page(ctx context.Context, book model.Book, offset, size int64) (service.BookTextPage, error)
	Outline(ctx context.Context, book model.Book) ([]service.BookOutlineEntry, error)
	Search(ctx context.Context, book model.Book, query string, maxMatches int) (service.TextSearchResult, error)
}

var (
	_ ShelfReader      = (*service.ShelfService)(nil)
	_ Suggester        = (*service.SearchService)(nil)
	_ AnnotationReader = (*service.AnnotationService)(nil)
	_ TextSource       = (*service.BookText)(nil)
	_ LibraryLister    = (*service.LibraryService)(nil)
)
