// SPDX-License-Identifier: AGPL-3.0-or-later

package mcpserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

type tools struct{ d Deps }

const (
	defaultSearchLimit = 20
	maxSearchLimit     = 50
	// suggestLimit bounds the shelf and library matches riding along with
	// a search. They are a hint about where to look, not a listing.
	suggestLimit = 5
)

// bookSummary is a book as it appears in a list.
type bookSummary struct {
	ID          string `json:"book_id"`
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle,omitempty"`
	Author      string `json:"author,omitempty"`
	Series      string `json:"series,omitempty"`
	SeriesIndex int    `json:"series_index,omitempty"`
	Year        int    `json:"year,omitempty"`
	Format      string `json:"format"`
	Language    string `json:"language,omitempty"`
	LibraryID   string `json:"library_id"`
	Progress    int    `json:"progress_percent" jsonschema:"the user's reading progress, 0-100"`
}

func toSummary(b model.Book) bookSummary {
	return bookSummary{
		ID: b.ID, Title: b.Title, Subtitle: b.Subtitle, Author: b.Author,
		Series: b.Series, SeriesIndex: b.SeriesIndex, Year: b.Year,
		Format: b.Format, Language: b.Language, LibraryID: b.LibraryID,
		Progress: b.Progress,
	}
}

func summaries(books []model.Book) []bookSummary {
	out := make([]bookSummary, 0, len(books))
	for _, b := range books {
		out = append(out, toSummary(b))
	}
	return out
}

// lookupBook is the book-scoped seam for tools: one place that validates
// the id, resolves it as the user, and turns "not there" into the
// model's message — the MCP twin of the handler tier's bookScoped.
func (t tools) lookupBook(ctx context.Context, u model.User, id string) (model.Book, error) {
	id = strings.TrimSpace(id)
	if _, err := uuid.Parse(id); err != nil {
		return model.Book{}, failf("book not found: book_id must be an id returned by search_library or list_shelf_books")
	}
	b, err := t.d.Books.GetByID(ctx, u.ID, id)
	if errors.Is(err, repo.ErrNotFound) {
		return model.Book{}, failf("book not found")
	}
	return b, err
}

// --- search_library ---------------------------------------------------------

type searchIn struct {
	Query   string `json:"query" jsonschema:"words to find in title, author, series or description; supports quoted phrases, OR, and -exclusion"`
	Library string `json:"library,omitempty" jsonschema:"restrict to one library, by slug from list_libraries"`
	Format  string `json:"format,omitempty" jsonschema:"restrict to one format, e.g. EPUB or PDF"`
	Limit   int    `json:"limit,omitempty" jsonschema:"books per page, default 20, at most 50"`
	Offset  int    `json:"offset,omitempty" jsonschema:"books to skip, for the next page"`
}

type namedRef struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type searchOut struct {
	Total int           `json:"total" jsonschema:"books matching in all, across every page"`
	Books []bookSummary `json:"books"`
	// Shelves and Libraries ride along on the first page only.
	Shelves   []namedRef `json:"shelves" jsonschema:"the user's shelves whose name matches (first page only)"`
	Libraries []namedRef `json:"libraries" jsonschema:"libraries whose name matches (first page only)"`
}

func (t tools) search(ctx context.Context, u model.User, in searchIn) (searchOut, error) {
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return searchOut{}, failf("query is required")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	limit = min(limit, maxSearchLimit)
	offset := max(in.Offset, 0)

	p := model.SearchParams{Query: q, Limit: limit, Offset: offset}
	if f := strings.TrimSpace(in.Format); f != "" {
		p.Format = []string{strings.ToUpper(f)}
	}
	books, total, err := t.d.Books.Search(ctx, u.ID, strings.TrimSpace(in.Library), p)
	if err != nil {
		return searchOut{}, err
	}
	out := searchOut{Total: total, Books: summaries(books), Shelves: []namedRef{}, Libraries: []namedRef{}}

	if offset == 0 {
		sug, err := t.d.Search.Suggest(ctx, u.ID, q, suggestLimit)
		if err != nil {
			return searchOut{}, err
		}
		for _, s := range sug.Shelves {
			out.Shelves = append(out.Shelves, namedRef{Slug: s.Slug, Name: s.Name})
		}
		for _, l := range sug.Libraries {
			out.Libraries = append(out.Libraries, namedRef{Slug: l.Slug, Name: l.Name})
		}
	}
	return out, nil
}

// --- list_libraries ---------------------------------------------------------

type libraryOut struct {
	ID        string `json:"library_id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	BookCount int    `json:"book_count"`
}

type librariesOut struct {
	Libraries []libraryOut `json:"libraries"`
}

func (t tools) listLibraries(ctx context.Context, _ model.User, _ struct{}) (librariesOut, error) {
	libs, err := t.d.Libraries.List(ctx)
	if err != nil {
		return librariesOut{}, err
	}
	out := librariesOut{Libraries: make([]libraryOut, 0, len(libs))}
	for _, l := range libs {
		out.Libraries = append(out.Libraries, libraryOut{ID: l.ID, Name: l.Name, Slug: l.Slug, BookCount: l.BookCount})
	}
	return out, nil
}

// --- list_shelves -----------------------------------------------------------

type shelfOut struct {
	Slug      string `json:"slug" jsonschema:"pass to list_shelf_books"`
	Name      string `json:"name"`
	Kind      string `json:"kind" jsonschema:"regular, smart (filled by a rule), or shared (another user's published shelf)"`
	BookCount int    `json:"book_count"`
	Owner     string `json:"owner,omitempty" jsonschema:"who shared it, for shared shelves"`
}

type shelvesOut struct {
	Shelves []shelfOut `json:"shelves"`
}

func (t tools) listShelves(ctx context.Context, u model.User, _ struct{}) (shelvesOut, error) {
	list, err := t.d.Shelves.List(ctx, u.ID)
	if err != nil {
		return shelvesOut{}, err
	}
	out := shelvesOut{Shelves: make([]shelfOut, 0, len(list))}
	for _, s := range list {
		o := shelfOut{Slug: s.Slug, Name: s.Name, Kind: "regular", BookCount: s.BookCount}
		switch {
		case s.IsSmart:
			o.Kind = "smart"
		case s.IsPublic:
			// Public shelves are addressed under the public: prefix,
			// the user's own included — the same rule the API's shelf
			// DTO follows (ADR-0017).
			o.Slug = service.PublicSlugPrefix + s.Slug
			if s.UserID != u.ID {
				o.Kind = "shared"
				o.Owner = s.OwnerName
			}
		}
		out.Shelves = append(out.Shelves, o)
	}
	return out, nil
}

// --- list_shelf_books -------------------------------------------------------

type shelfBooksIn struct {
	Slug string `json:"slug" jsonschema:"shelf slug from list_shelves"`
	Sort string `json:"sort,omitempty" jsonschema:"title, author, recent, year or rating"`
}

type shelfBooksOut struct {
	Books []bookSummary `json:"books"`
}

func (t tools) listShelfBooks(ctx context.Context, u model.User, in shelfBooksIn) (shelfBooksOut, error) {
	slug := strings.TrimSpace(in.Slug)
	if slug == "" {
		return shelfBooksOut{}, failf("slug is required")
	}
	books, err := t.d.Shelves.Books(ctx, u.ID, slug, strings.TrimSpace(in.Sort))
	if errors.Is(err, repo.ErrNotFound) {
		return shelfBooksOut{}, failf("shelf not found")
	}
	if err != nil {
		return shelfBooksOut{}, err
	}
	return shelfBooksOut{Books: summaries(books)}, nil
}

// --- get_book ---------------------------------------------------------------

type bookIDIn struct {
	BookID string `json:"book_id" jsonschema:"from search_library or list_shelf_books"`
}

type bookDetailOut struct {
	bookSummary
	Description string   `json:"description,omitempty"`
	Publisher   string   `json:"publisher,omitempty"`
	PublishDate string   `json:"publish_date,omitempty"`
	ISBN        string   `json:"isbn,omitempty"`
	ISBN10      string   `json:"isbn10,omitempty"`
	Pages       int      `json:"pages,omitempty"`
	Genres      []string `json:"genres"`
	Tags        []string `json:"tags"`
	Rating      int      `json:"rating,omitempty" jsonschema:"the user's rating, 1-5"`
	Narrator    string   `json:"narrator,omitempty"`
	DurationSec int      `json:"duration_seconds,omitempty"`
	Shelves     []string `json:"shelves" jsonschema:"slugs of the user's shelves holding this book"`
}

func (t tools) getBook(ctx context.Context, u model.User, in bookIDIn) (bookDetailOut, error) {
	b, err := t.lookupBook(ctx, u, in.BookID)
	if err != nil {
		return bookDetailOut{}, err
	}
	shelves, err := t.d.Shelves.SlugsForBook(ctx, u.ID, b.ID)
	if err != nil {
		return bookDetailOut{}, err
	}
	out := bookDetailOut{
		bookSummary: toSummary(b),
		Description: b.Description,
		Publisher:   b.Publisher,
		ISBN:        b.ISBN,
		ISBN10:      b.ISBN10,
		Pages:       b.Pages,
		Genres:      nonNil(b.Genres),
		Tags:        nonNil(b.Tags),
		Rating:      b.Rating,
		Narrator:    b.Narrator,
		Shelves:     nonNil(shelves),
	}
	if b.PublishDate != nil {
		out.PublishDate = b.PublishDate.Format(time.DateOnly)
	}
	if b.DurationSeconds != nil {
		out.DurationSec = *b.DurationSeconds
	}
	return out, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (t tools) registerCatalog(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "search_library",
		Description: "Full-text search over the library's books (title, author, series, description). Also returns the user's shelves and libraries whose names match, on the first page.",
		Annotations: readOnly("Search library"),
	}, withUser("search_library", t.search))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_libraries",
		Description: "List the libraries on this instance with their book counts.",
		Annotations: readOnly("List libraries"),
	}, withUser("list_libraries", t.listLibraries))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_shelves",
		Description: "List the user's shelves, including smart shelves and shelves other users have shared.",
		Annotations: readOnly("List shelves"),
	}, withUser("list_shelves", t.listShelves))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_shelf_books",
		Description: "List the books on one shelf.",
		Annotations: readOnly("List shelf books"),
	}, withUser("list_shelf_books", t.listShelfBooks))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_book",
		Description: "Get one book's full metadata, the user's reading progress and rating, and which of the user's shelves hold it.",
		Annotations: readOnly("Get book"),
	}, withUser("get_book", t.getBook))
}
