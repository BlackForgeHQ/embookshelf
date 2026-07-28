// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/service"
)

// audiobookGetHandler builds the narrowest Handler BookAudiobookGet can
// run against: a real library service and audiobook repo over an empty
// schema, plus a non-nil audiobooks seam so the "generation is not
// configured" guard does not short-circuit before the code under test.
func audiobookGetHandler(t *testing.T) (*Handler, *repo.BookRepo, *repo.LibraryRepo, *repo.BookAudiobookRepo) {
	t.Helper()
	d := repotest.New(t)
	books := repo.NewBookRepo(d)
	libs := repo.NewLibraryRepo(d)
	runs := repo.NewBookAudiobookRepo(d)
	h := &Handler{
		lib:           service.NewLibraryService(libs, books, service.LibraryServiceDeps{}, nil),
		books:         books,
		audiobookRepo: runs,
		audiobooks:    &service.AudiobookService{},
	}
	return h, books, libs, runs
}

// audiobookTestUser is a well-formed UUID because the per-user columns
// GetBook joins on are uuid-typed — a placeholder like "u1" fails at the
// driver and lands every case in the 500 arm.
const audiobookTestUser = "0a5d3b2c-1111-4111-8111-111111111111"

// audiobookGetCtx wires a GET for one book id, optionally with a session
// user attached the way auth.RequireAuth would have.
func audiobookGetCtx(t *testing.T, bookID string, withUser bool) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/books/"+bookID+"/audiobook", nil)
	c.Params = gin.Params{{Key: "id", Value: bookID}}
	if withUser {
		c.Request = c.Request.WithContext(
			auth.WithUser(c.Request.Context(), &model.User{ID: audiobookTestUser}))
	}
	return c, rec
}

// assertSingleResponse is the assertion this file exists for. A handler
// that writes twice keeps the status of the first write but appends both
// bodies, so gin reports a plausible code over an unparseable stream.
// Decoding the body as exactly one JSON value is what catches it.
func assertSingleResponse(t *testing.T, body string) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(body))
	var first json.RawMessage
	if err := dec.Decode(&first); err != nil {
		t.Fatalf("body is not a single JSON value: %v (body %q)", err, body)
	}
	if _, err := dec.Token(); err != io.EOF {
		t.Fatalf("handler wrote more than one response: %q", body)
	}
}

// TestBookAudiobookGetMissingBookIsA404 pins the branch the handler used
// to skip: the book lookup error went to the blank identifier, so a book
// that could not be loaded produced a zero-value Book that was then
// reported on as though it were real.
//
// The id is a well-formed UUID that no row carries — books.id is a uuid
// column, so a garbage string fails at the driver and reaches the 500
// arm instead of the missing-book one this test is about. The body is
// checked because both arms answer 404: only the book branch says the
// book is the thing that is missing.
func TestBookAudiobookGetMissingBookIsA404(t *testing.T) {
	h, _, _, _ := audiobookGetHandler(t)
	c, rec := audiobookGetCtx(t, "6f1c0f7e-0000-4000-8000-000000000000", true)

	h.bookScoped(h.BookAudiobookGet)(c)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "book not found") {
		t.Fatalf("body = %s, want the missing-book branch", rec.Body.String())
	}
	assertSingleResponse(t, rec.Body.String())
}

// TestBookAudiobookGetWithoutUserWritesOnlyThe401 is the regression the
// issue is about. requireUserID writes its own 401 and returns "", so
// calling it inline in the book lookup — after the run and coverage
// queries had already succeeded — left the handler free to carry on and
// write a 200 body after it. Two responses, and the status the client
// saw did not match the payload it got.
func TestBookAudiobookGetWithoutUserWritesOnlyThe401(t *testing.T) {
	h, books, libs, runs := audiobookGetHandler(t)
	ctx := context.Background()

	lib, err := libs.CreateLibrary(ctx, "Audiobook Lib", "audiobook-lib", "/tmp/abl", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	book, err := books.Create(ctx, model.Book{
		LibraryID: lib.ID,
		Title:     "Narrated Book",
		Format:    "EPUB",
	})
	if err != nil {
		t.Fatalf("Create book: %v", err)
	}
	// A real run, so the handler gets past both audiobook queries and
	// reaches the point where it used to derive the user id.
	if err := runs.Start(ctx, model.Audiobook{
		BookID: book.ID, Engine: "openai", Voice: "alloy", TotalChars: 10,
	}, []model.AudiobookSegment{{Seq: 0, ChapterTitle: "One", CharEnd: 10}}); err != nil {
		t.Fatalf("Start run: %v", err)
	}

	c, rec := audiobookGetCtx(t, book.ID, false)

	h.bookScoped(h.BookAudiobookGet)(c)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}
	assertSingleResponse(t, rec.Body.String())
}
