// SPDX-License-Identifier: AGPL-3.0-or-later

package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

const (
	bookID  = "11111111-1111-1111-1111-111111111111"
	otherID = "22222222-2222-2222-2222-222222222222"
)

// --- fakes ------------------------------------------------------------------

type fakeBooks struct {
	books    map[string]model.Book
	seenUser []string
	params   model.SearchParams
	library  string
	err      error
	total    int
}

func (f *fakeBooks) GetByID(_ context.Context, userID, id string) (model.Book, error) {
	f.seenUser = append(f.seenUser, userID)
	if f.err != nil {
		return model.Book{}, f.err
	}
	b, ok := f.books[id]
	if !ok {
		return model.Book{}, repo.ErrNotFound
	}
	return b, nil
}

// Search honours Limit/Offset over the books by title. total, when set,
// overrides the reported match count.
func (f *fakeBooks) Search(_ context.Context, userID, library string, p model.SearchParams) ([]model.Book, int, error) {
	f.seenUser = append(f.seenUser, userID)
	f.params, f.library = p, library
	all := make([]model.Book, 0, len(f.books))
	for _, b := range f.books {
		all = append(all, b)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Title < all[j].Title })
	total := len(all)
	if f.total > 0 {
		total = f.total
	}
	start := min(p.Offset, len(all))
	end := len(all)
	if p.Limit > 0 {
		end = min(start+p.Limit, len(all))
	}
	return all[start:end], total, nil
}

type fakeLibraries []model.Library

func (f fakeLibraries) List(context.Context) ([]model.Library, error) { return f, nil }

type fakeShelves struct {
	shelves []model.Shelf
	books   map[string][]model.Book
	slugs   []string
}

func (f fakeShelves) List(context.Context, string) ([]model.Shelf, error) { return f.shelves, nil }
func (f fakeShelves) Books(_ context.Context, _, slug, _ string) ([]model.Book, error) {
	b, ok := f.books[slug]
	if !ok {
		return nil, repo.ErrNotFound
	}
	return b, nil
}
func (f fakeShelves) SlugsForBook(context.Context, string, string) ([]string, error) {
	return f.slugs, nil
}

type fakeSuggest struct{ calls int }

func (f *fakeSuggest) Suggest(context.Context, string, string, int) (service.SuggestResult, error) {
	f.calls++
	return service.SuggestResult{
		Shelves:   []repo.SuggestShelf{{Slug: "dune-reads", Name: "Dune reads"}},
		Libraries: []repo.SuggestLibrary{{Slug: "scifi", Name: "Sci-fi"}},
	}, nil
}

type fakeAnnotations struct{ list []model.Annotation }

func (f fakeAnnotations) ListForBook(context.Context, string, string) ([]model.Annotation, error) {
	return f.list, nil
}
func (f fakeAnnotations) ListRecent(context.Context, string, int) ([]model.Annotation, error) {
	return f.list, nil
}

type fakeGuides map[string]model.ReadingGuide

func (f fakeGuides) GetByBookID(_ context.Context, id string) (model.ReadingGuide, error) {
	g, ok := f[id]
	if !ok {
		return model.ReadingGuide{}, repo.ErrNotFound
	}
	return g, nil
}

type fakeText struct {
	text    string
	err     error
	outline []service.BookOutlineEntry
	search  service.TextSearchResult
}

func (f fakeText) Read(_ context.Context, _ model.Book, limit int64) (string, bool, error) {
	if f.err != nil {
		return "", false, f.err
	}
	if int64(len(f.text)) > limit {
		return f.text[:limit], true, nil
	}
	return f.text, false, nil
}

func (f fakeText) Outline(context.Context, model.Book) ([]service.BookOutlineEntry, error) {
	return f.outline, f.err
}

func (f fakeText) Search(_ context.Context, _ model.Book, query string, _ int) (service.TextSearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return service.TextSearchResult{}, service.ErrEmptyTextQuery
	}
	return f.search, f.err
}

func (f fakeText) Page(_ context.Context, _ model.Book, offset, size int64) (service.BookTextPage, error) {
	if f.err != nil {
		return service.BookTextPage{}, f.err
	}
	end := min(offset+size, int64(len(f.text)))
	start := min(offset, end)
	p := service.BookTextPage{Text: f.text[start:end], Offset: start}
	if end < int64(len(f.text)) {
		p.NextOffset = &end
	}
	return p, nil
}

// --- harness ----------------------------------------------------------------

type harness struct {
	books   *fakeBooks
	suggest *fakeSuggest
	deps    Deps
}

func newHarness() *harness {
	h := &harness{
		books: &fakeBooks{books: map[string]model.Book{
			bookID: {ID: bookID, Title: "Dune", Author: "Frank Herbert", Format: "EPUB", Progress: 40, Tags: []string{"classic"}},
		}},
		suggest: &fakeSuggest{},
	}
	h.deps = Deps{
		Books:     h.books,
		Libraries: fakeLibraries{{ID: "l1", Name: "Sci-fi", Slug: "scifi", BookCount: 3}},
		Shelves: fakeShelves{
			shelves: []model.Shelf{
				{Slug: "reading", Name: "Reading", UserID: "u1", BookCount: 1},
				{Slug: "best", Name: "Best of", UserID: "u2", IsPublic: true, OwnerName: "Bob"},
				{Slug: "long", Name: "Long", UserID: "u1", IsSmart: true},
			},
			books: map[string][]model.Book{"reading": {{ID: bookID, Title: "Dune", Format: "EPUB"}}},
			slugs: []string{"reading"},
		},
		Search:      h.suggest,
		Annotations: fakeAnnotations{list: []model.Annotation{{ID: "a1", BookID: bookID, SelectedText: "Fear is the mind-killer."}}},
		Guides:      fakeGuides{bookID: {BookID: bookID, ReadingGuideText: model.ReadingGuideText{About: "Desert politics."}, SourceKind: model.GuideSourceFullText}},
		Text:        fakeText{text: strings.Repeat("abcdefghij", 5)},
		Version:     "test",
	}
	return h
}

// connect serves the MCP handler behind a stand-in for auth.BearerToken
// that attaches user u, and returns a client session over real HTTP.
func (h *harness) connect(t *testing.T, u *model.User) *mcp.ClientSession {
	t.Helper()
	inner := Handler(New(h.deps))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u != nil {
			r = r.WithContext(auth.WithUser(r.Context(), u))
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   srv.URL,
		HTTPClient: &http.Client{Transport: bearer{"ebk_test"}},
		MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

var reader = &model.User{ID: "u1", Email: "reader@example.com", Role: model.RoleUser, Status: model.UserStatusActive}

// call invokes a tool and decodes its structured output into out (when
// non-nil). It returns the result for error checks.
func call(t *testing.T, cs *mcp.ClientSession, name string, args any, out any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	if out != nil && !res.IsError {
		raw, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s: decode structured content %s: %v", name, raw, err)
		}
	}
	return res
}

func errorText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if !res.IsError {
		t.Fatalf("expected a tool error, got success: %+v", res.StructuredContent)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// --- tests ------------------------------------------------------------------

func TestListsEveryToolAsReadOnly(t *testing.T) {
	cs := newHarness().connect(t, reader)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"search_library": true, "list_libraries": true, "list_shelves": true,
		"list_shelf_books": true, "get_book": true, "get_book_text": true,
		"search_book_text": true, "get_book_toc": true,
		"list_annotations": true, "get_reading_guide": true,
	}
	for _, tool := range res.Tools {
		if !want[tool.Name] {
			t.Errorf("unexpected tool %q", tool.Name)
			continue
		}
		delete(want, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not annotated read-only", tool.Name)
		}
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
	}
	for name := range want {
		t.Errorf("tool %q is missing", name)
	}
}

func TestRefusesARequestWithoutAUser(t *testing.T) {
	inner := Handler(New(newHarness().deps))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer ebk_test")
	inner.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no user on the context: status %d, want 401", rec.Code)
	}
}

func TestToolsActAsTheTokenOwner(t *testing.T) {
	h := newHarness()
	cs := h.connect(t, reader)

	call(t, cs, "get_book", map[string]any{"book_id": bookID}, nil)
	call(t, cs, "search_library", map[string]any{"query": "dune"}, nil)

	for _, u := range h.books.seenUser {
		if u != reader.ID {
			t.Fatalf("a tool read the catalog as %q, want %q", u, reader.ID)
		}
	}
}

func TestSearchLibrary(t *testing.T) {
	h := newHarness()
	h.books.total = 42
	cs := h.connect(t, reader)

	var out searchOut
	call(t, cs, "search_library", map[string]any{"query": " dune ", "format": "epub", "library": "scifi", "limit": 500}, &out)

	if out.Total != 42 || len(out.Books) != 1 || out.Books[0].Title != "Dune" {
		t.Fatalf("out = %+v", out)
	}
	if h.books.params.Query != "dune" || h.books.params.Limit != maxSearchLimit || h.books.params.Format[0] != "EPUB" || h.books.library != "scifi" {
		t.Fatalf("search params = %+v, library %q", h.books.params, h.books.library)
	}
	if len(out.Shelves) != 1 || len(out.Libraries) != 1 {
		t.Fatalf("first page lacks shelf/library matches: %+v", out)
	}

	call(t, cs, "search_library", map[string]any{"query": "dune", "offset": 20}, &out)
	if h.suggest.calls != 1 {
		t.Fatalf("later page re-ran the suggest fan-out (%d calls)", h.suggest.calls)
	}

	if msg := errorText(t, call(t, cs, "search_library", map[string]any{"query": "  "}, nil)); !strings.Contains(msg, "query is required") {
		t.Fatalf("blank query error = %q", msg)
	}
}

func TestListShelvesNamesKindsAndPublicSlugs(t *testing.T) {
	cs := newHarness().connect(t, reader)
	var out shelvesOut
	call(t, cs, "list_shelves", map[string]any{}, &out)

	got := map[string]shelfOut{}
	for _, s := range out.Shelves {
		got[s.Name] = s
	}
	if got["Reading"].Kind != "regular" || got["Reading"].Slug != "reading" {
		t.Errorf("regular shelf = %+v", got["Reading"])
	}
	if got["Best of"].Kind != "shared" || got["Best of"].Slug != "public:best" || got["Best of"].Owner != "Bob" {
		t.Errorf("shared shelf = %+v", got["Best of"])
	}
	if got["Long"].Kind != "smart" {
		t.Errorf("smart shelf = %+v", got["Long"])
	}
}

func TestListShelfBooks(t *testing.T) {
	cs := newHarness().connect(t, reader)
	var out shelfBooksOut
	call(t, cs, "list_shelf_books", map[string]any{"slug": "reading"}, &out)
	if len(out.Books) != 1 {
		t.Fatalf("books = %+v", out.Books)
	}
	if msg := errorText(t, call(t, cs, "list_shelf_books", map[string]any{"slug": "nope"}, nil)); msg != "shelf not found" {
		t.Fatalf("unknown shelf error = %q", msg)
	}
}

func TestGetBook(t *testing.T) {
	cs := newHarness().connect(t, reader)
	var out bookDetailOut
	call(t, cs, "get_book", map[string]any{"book_id": bookID}, &out)
	if out.Title != "Dune" || out.Progress != 40 || len(out.Shelves) != 1 || out.Tags[0] != "classic" {
		t.Fatalf("out = %+v", out)
	}
	if out.Genres == nil {
		t.Fatal("empty genres encoded as null")
	}
}

func TestGetBookNotFound(t *testing.T) {
	h := newHarness()
	cs := h.connect(t, reader)

	if msg := errorText(t, call(t, cs, "get_book", map[string]any{"book_id": otherID}, nil)); msg != "book not found" {
		t.Fatalf("unknown id error = %q", msg)
	}
	before := len(h.books.seenUser)
	msg := errorText(t, call(t, cs, "get_book", map[string]any{"book_id": "Dune"}, nil))
	if !strings.HasPrefix(msg, "book not found") {
		t.Fatalf("malformed id error = %q", msg)
	}
	if len(h.books.seenUser) != before {
		t.Fatal("a malformed id reached the database")
	}
}

// An infrastructure failure must not leak its text to the client.
func TestInternalErrorsStayInternal(t *testing.T) {
	h := newHarness()
	h.books.err = errors.New(`pq: relation "books" does not exist at /var/lib/secret`)
	cs := h.connect(t, reader)

	msg := errorText(t, call(t, cs, "get_book", map[string]any{"book_id": bookID}, nil))
	if msg != "internal error" {
		t.Fatalf("leaked error text: %q", msg)
	}
}

func TestGetBookTextPagesToTheEnd(t *testing.T) {
	h := newHarness()
	cs := h.connect(t, reader)

	var (
		got    strings.Builder
		offset int64
		pages  int
	)
	for {
		var out bookTextOut
		call(t, cs, "get_book_text", map[string]any{"book_id": bookID, "offset": offset, "max_chars": 15}, &out)
		if out.Status != "ok" {
			t.Fatalf("status %q", out.Status)
		}
		got.WriteString(out.Text)
		pages++
		if out.NextOffset == nil {
			break
		}
		offset = *out.NextOffset
		if pages > 10 {
			t.Fatal("paging never ended")
		}
	}
	if got.String() != strings.Repeat("abcdefghij", 5) || pages != 4 {
		t.Fatalf("reassembled %d pages into %q", pages, got.String())
	}
}

func TestGetBookTextOutcomes(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		pending bool
		wantMsg string
	}{
		{"pending rendition is a result", service.ErrRenditionPending, true, ""},
		{"failed rendition is verbatim", &service.RenditionFailedError{Msg: "converter extension is not configured"}, false, "could not read this book: converter extension is not configured"},
		{"textless format", service.ErrNoBookText, false, "no readable text for EPUB books"},
		{"image-only epub", fileproc.ErrNoReadableText, false, "this book has no readable text (it may be images only)"},
		{"storage failure", errors.New("s3: access denied"), false, "internal error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness()
			h.deps.Text = fakeText{err: tc.err}
			cs := h.connect(t, reader)
			var out bookTextOut
			res := call(t, cs, "get_book_text", map[string]any{"book_id": bookID}, &out)
			if tc.pending {
				if res.IsError || out.Status != "pending" || out.Message == "" {
					t.Fatalf("pending: isError=%v out=%+v", res.IsError, out)
				}
				return
			}
			if msg := errorText(t, res); msg != tc.wantMsg {
				t.Fatalf("error = %q, want %q", msg, tc.wantMsg)
			}
		})
	}
}

func TestListAnnotations(t *testing.T) {
	cs := newHarness().connect(t, reader)
	var out annotationsOut
	call(t, cs, "list_annotations", map[string]any{"book_id": bookID}, &out)
	if len(out.Annotations) != 1 || out.Annotations[0].Text != "Fear is the mind-killer." {
		t.Fatalf("out = %+v", out)
	}
	call(t, cs, "list_annotations", map[string]any{}, &out)
	if len(out.Annotations) != 1 {
		t.Fatalf("recent = %+v", out)
	}
}

func TestGetReadingGuide(t *testing.T) {
	h := newHarness()
	h.books.books[otherID] = model.Book{ID: otherID, Title: "No guide", Format: "PDF"}
	cs := h.connect(t, reader)

	var out guideOut
	call(t, cs, "get_reading_guide", map[string]any{"book_id": bookID}, &out)
	if !out.Available || out.About != "Desert politics." || out.Source != "full_text" {
		t.Fatalf("out = %+v", out)
	}
	call(t, cs, "get_reading_guide", map[string]any{"book_id": otherID}, &out)
	if out.Available {
		t.Fatalf("a book without a guide reported one: %+v", out)
	}
}
