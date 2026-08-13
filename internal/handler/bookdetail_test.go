// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/service"
)

// Five endpoints across the library, enrichment and bookdrop surfaces
// answer with the book detail payload, and each used to assemble it by
// hand. This table drives all five through one database and demands the
// same body shape from every one: a book, and a shelves array that is the
// user's real membership rather than a JSON null or a literal.
//
// The drift this pins: bookdrop approve hard-coded Shelves: []string{},
// so its answer was independent of the shelf tables entirely.

// detailShelfSlug is the shelf the fixture book sits on. A non-empty
// membership is what separates "the module queried" from "the module
// returned an empty literal".
const detailShelfSlug = "favourites"

// stubPlacer materialises nothing. Approve only needs a PlaceResult to
// build the books and files rows; the placement adapters have their own
// tests.
type stubPlacer struct{ location string }

func (p stubPlacer) Place(context.Context, service.PlaceSource) (service.PlaceResult, error) {
	return service.PlaceResult{
		Location:   p.location,
		FolderPath: filepath.Dir(p.location),
		Size:       1024,
		Mtime:      time.Now(),
	}, nil
}

// detailFixture is one migrated schema carrying everything the five
// endpoints need: a book already on a shelf, and a ready bookdrop item to
// approve into the same library.
type detailFixture struct {
	h      *Handler
	userID string
	bookID string
	itemID string
}

// autoShelf puts every books row inserted from here on onto one shelf.
//
// A trigger rather than an explicit AddBook, because of the one endpoint
// that cannot be set up any other way: bookdrop approve creates the book
// itself, so there is no moment between the row existing and the response
// being built in which a test could shelve it. Without this, approve's
// membership is empty whether the module queried for it or returned a
// literal — which is exactly the blind spot that let the hard-coded
// Shelves: []string{} sit there unnoticed. With it, a literal fails.
func autoShelf(t *testing.T, d *db.DB, shelfID string) {
	t.Helper()
	if _, err := d.SQL.ExecContext(t.Context(), `
		CREATE FUNCTION detail_auto_shelf() RETURNS trigger AS $$
		BEGIN
			INSERT INTO shelf_books (shelf_id, book_id) VALUES ('`+shelfID+`', NEW.id);
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER detail_auto_shelf_trg AFTER INSERT ON books
		FOR EACH ROW EXECUTE FUNCTION detail_auto_shelf();
	`); err != nil {
		t.Fatalf("install auto-shelf trigger: %v", err)
	}
}

func newDetailFixture(t *testing.T) detailFixture {
	t.Helper()
	ctx := t.Context()
	d := repotest.New(t)
	libRepo := repo.NewLibraryRepo(d)
	bookRepo := repo.NewBookRepo(d)
	shelfRepo := repo.NewShelfRepo(d)
	bdropRepo := repo.NewBookDropRepo(d)

	// A real users row: shelves.user_id carries a foreign key, so the
	// shelf membership this file turns on cannot be faked with a literal.
	user, err := repo.NewUserRepo(d).Create(ctx, "reader@example.com", "Reader", "x", model.RoleUser)
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}

	lib, err := libRepo.CreateLibrary(ctx, "Fiction", "fiction", "", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	shelf, err := shelfRepo.Create(ctx, user.ID, "Favourites", "", "", nil)
	if err != nil {
		t.Fatalf("Create shelf: %v", err)
	}
	if shelf.Slug != detailShelfSlug {
		t.Fatalf("shelf slug = %q, want %q", shelf.Slug, detailShelfSlug)
	}
	autoShelf(t, d, shelf.ID)

	book, err := bookRepo.Create(ctx, model.Book{
		LibraryID: lib.ID,
		Title:     "Stored Title",
		Author:    "Stored Author",
		Format:    "CBZ",
		Path:      "books/x.cbz",
	})
	if err != nil {
		t.Fatalf("Create book: %v", err)
	}

	item, err := bdropRepo.Insert(ctx, filepath.Join(t.TempDir(), "dune.epub"), "EPUB", 2048)
	if err != nil {
		t.Fatalf("Insert bookdrop item: %v", err)
	}
	if err := bdropRepo.SetMetadata(ctx, item.ID,
		"Dune", "Frank Herbert", "", "en", "", false, ""); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}

	// The handle carries no Storage, so the write pipeline plans neither
	// a sidecar nor an in-file embed and every edit below lands clean.
	// Degradation has its own tests; this file is about the body shape.
	handle := &service.LibraryHandle{
		Library: lib,
		Placer:  stubPlacer{location: "Frank Herbert/Dune/dune.epub"},
	}
	writer := service.NewMetadataWriter(service.MetadataWriterDeps{
		Books:    bookRepo,
		LibStore: fixedLibStore{handle},
	})

	h := &Handler{
		lib:    service.NewLibraryService(libRepo, bookRepo, service.LibraryServiceDeps{}, writer),
		books:  bookRepo,
		shelf:  service.NewShelfService(shelfRepo, nil),
		enrich: service.NewEnrichmentService(nil, nil, nil, nil, writer),
		bookdrop: service.NewBookDropService(bdropRepo, libRepo, bookRepo, nil, nil, nil, &jobs.Deferred{}).
			WithLibraryStore(fixedLibStore{handle}),
	}
	return detailFixture{h: h, userID: user.ID, bookID: book.ID, itemID: item.ID}
}

// detailRequest drives one handler with a signed-in user and one :id.
func detailRequest(t *testing.T, f detailFixture, fn gin.HandlerFunc, method, target, id, body string) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	c.Request = r.WithContext(
		auth.WithUser(r.Context(), &model.User{ID: f.userID}))
	c.Params = gin.Params{{Key: "id", Value: id}}

	fn(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v (%s)", err, rec.Body.String())
	}
	return got
}

func TestBookDetailBodyShapeIsTheSameAtEveryEndpoint(t *testing.T) {
	cases := []struct {
		name   string
		method string
		target string
		body   string
		// call names the endpoint. Four are book-scoped and take the
		// book id; approve takes a bookdrop item id, which is why the
		// fixture hands both to the closure.
		call func(detailFixture) (gin.HandlerFunc, string)
		// wantShelves is the user's real membership at the moment the
		// endpoint answers.
		wantShelves []string
		// freshImport marks the one row whose book did not exist before
		// the request, so its id cannot be asserted against the fixture.
		freshImport bool
	}{
		{
			name: "GET /books/:id", method: http.MethodGet, target: "/api/v1/books/x",
			call: func(f detailFixture) (gin.HandlerFunc, string) {
				return f.h.bookScoped(f.h.BookDetail), f.bookID
			},
			wantShelves: []string{detailShelfSlug},
		},
		{
			name: "PATCH /books/:id", method: http.MethodPatch, target: "/api/v1/books/x",
			body: `{"title":"Edited Title"}`,
			call: func(f detailFixture) (gin.HandlerFunc, string) {
				return f.h.bookScoped(f.h.BookPatch), f.bookID
			},
			wantShelves: []string{detailShelfSlug},
		},
		{
			name: "PUT /books/:id/metadata", method: http.MethodPut,
			target: "/api/v1/books/x/metadata",
			body:   `{"source":"googlebooks","sourceId":"g1","title":"Provider Title"}`,
			call: func(f detailFixture) (gin.HandlerFunc, string) {
				return f.h.bookScoped(f.h.EnrichApplyMatch), f.bookID
			},
			wantShelves: []string{detailShelfSlug},
		},
		{
			name: "PUT /books/:id/metadata/locks", method: http.MethodPut,
			target: "/api/v1/books/x/metadata/locks",
			body:   `{"locks":{"title":true}}`,
			call: func(f detailFixture) (gin.HandlerFunc, string) {
				return f.h.bookScoped(f.h.EnrichToggleFieldLocks), f.bookID
			},
			wantShelves: []string{detailShelfSlug},
		},
		{
			name: "POST /bookdrop/:id/approve", method: http.MethodPost,
			target: "/api/v1/bookdrop/x/approve",
			call: func(f detailFixture) (gin.HandlerFunc, string) {
				return f.h.userScoped(f.h.BookDropApprove), f.itemID
			},
			// The row that failed before the module existed: approve
			// hard-coded an empty list, so it answered the same whether
			// the book was on a shelf or not.
			wantShelves: []string{detailShelfSlug},
			freshImport: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDetailFixture(t)
			fn, id := tc.call(f)
			got := detailRequest(t, f, fn, tc.method, tc.target, id, tc.body)

			book, ok := got["book"].(map[string]any)
			if !ok {
				t.Fatalf("no book object in the body: %#v", got)
			}
			if !tc.freshImport {
				if book["id"] != f.bookID {
					t.Errorf("book.id = %v, want %s", book["id"], f.bookID)
				}
			} else if book["id"] == nil || book["id"] == "" {
				t.Errorf("book.id = %v, want the id of the imported book", book["id"])
			}

			raw, present := book["shelves"]
			if !present {
				t.Fatalf("no shelves key on the detail payload: %#v", book)
			}
			// The nil-to-empty rule. A nil slice marshals to null, and the
			// client's Book type declares shelves as string[].
			if raw == nil {
				t.Fatalf("shelves = null, want an array")
			}
			list, ok := raw.([]any)
			if !ok {
				t.Fatalf("shelves = %#v, want an array", raw)
			}
			slugs := make([]string, 0, len(list))
			for _, v := range list {
				s, ok := v.(string)
				if !ok {
					t.Fatalf("shelves entry %#v is not a string", v)
				}
				slugs = append(slugs, s)
			}
			if strings.Join(slugs, ",") != strings.Join(tc.wantShelves, ",") {
				t.Errorf("shelves = %v, want %v", slugs, tc.wantShelves)
			}

			// The base DTO's own nil-to-empty fields travel with it, so a
			// caller parsing any of the five gets arrays throughout.
			for _, field := range []string{"genres", "moods", "tags"} {
				if v, ok := book[field]; !ok || v == nil {
					t.Errorf("book.%s = %#v, want an array", field, v)
				}
			}
		})
	}
}
