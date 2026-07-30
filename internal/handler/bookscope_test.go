// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// The seam is tested once, here, with a substituted book store. Every
// book-scoped route inherits these three answers — found, not-found,
// lookup-error — instead of restating them, so this file is where the
// preamble's behaviour is pinned for all of them.

const scopeTestUserID = "b1b1b1b1-0002-4002-8002-000000000001"

// fakeBookStore is the substituted catalog. calls counts lookups so a
// test can prove the seam never reached the store, which is the whole
// claim of the unauthenticated case.
type fakeBookStore struct {
	book  model.Book
	err   error
	calls int
	// gotUser and gotID record what the seam asked for, so the test can
	// show the session user reaches the store rather than something the
	// caller supplied.
	gotUser string
	gotID   string
}

func (f *fakeBookStore) GetByID(_ context.Context, userID, id string) (model.Book, error) {
	f.calls++
	f.gotUser, f.gotID = userID, id
	if f.err != nil {
		return model.Book{}, f.err
	}
	return f.book, nil
}

func (f *fakeBookStore) Search(context.Context, string, string, model.SearchParams) ([]model.Book, error) {
	return nil, nil
}

// scopeRequest drives one wrapped handler. withUser mirrors what
// auth.RequireAuth would have attached; omitting it is the
// unauthenticated case.
func scopeRequest(t *testing.T, fn gin.HandlerFunc, bookID string, withUser bool) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/books/"+bookID, nil)
	c.Params = gin.Params{{Key: "id", Value: bookID}}
	if withUser {
		c.Request = c.Request.WithContext(
			auth.WithUser(c.Request.Context(), &model.User{ID: scopeTestUserID}))
	}
	fn(c)
	return rec
}

func TestBookScopedResolvesTheBookForTheSessionUser(t *testing.T) {
	store := &fakeBookStore{book: model.Book{ID: "book-1", Title: "Dune"}}
	h := &Handler{books: store}

	var got bookScope
	ran := false
	rec := scopeRequest(t, h.bookScoped(func(c *gin.Context, s bookScope) {
		ran, got = true, s
		c.Status(http.StatusOK)
	}), "book-1", true)

	if !ran {
		t.Fatal("handler body never ran on a book that resolves")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got.Book.Title != "Dune" {
		t.Errorf("body got book %q, want the one the store returned", got.Book.Title)
	}
	if got.UserID != scopeTestUserID {
		t.Errorf("body got user %q, want the session user", got.UserID)
	}
	if store.gotUser != scopeTestUserID || store.gotID != "book-1" {
		t.Errorf("store queried with (%q, %q), want (%q, %q)",
			store.gotUser, store.gotID, scopeTestUserID, "book-1")
	}
}

func TestBookScopedMissingBookIsA404AndStopsThere(t *testing.T) {
	h := &Handler{books: &fakeBookStore{err: repo.ErrNotFound}}

	ran := false
	rec := scopeRequest(t, h.bookScoped(func(*gin.Context, bookScope) {
		ran = true
	}), "gone", true)

	if ran {
		t.Fatal("handler body ran for a book that does not exist")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "book not found") {
		t.Errorf("body = %s, want the missing-book message", rec.Body.String())
	}
}

func TestBookScopedLookupErrorIsA500AndStopsThere(t *testing.T) {
	h := &Handler{books: &fakeBookStore{err: errors.New("connection reset")}}

	ran := false
	rec := scopeRequest(t, h.bookScoped(func(*gin.Context, bookScope) {
		ran = true
	}), "book-1", true)

	if ran {
		t.Fatal("handler body ran after the lookup failed")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body %s)", rec.Code, rec.Body.String())
	}
	// The raw error stays in the log; the wire carries the generic
	// message every 500 in this API carries.
	if strings.Contains(rec.Body.String(), "connection reset") {
		t.Errorf("body = %s, leaks the underlying error", rec.Body.String())
	}
}

// The fail-closed claim. An unauthenticated request must not reach a
// handler body, and must not cost a catalog lookup either — the seam
// answers before it asks anything.
func TestBookScopedUnauthenticatedNeverReachesTheBody(t *testing.T) {
	store := &fakeBookStore{book: model.Book{ID: "book-1"}}
	h := &Handler{books: store}

	ran := false
	rec := scopeRequest(t, h.bookScoped(func(*gin.Context, bookScope) {
		ran = true
	}), "book-1", false)

	if ran {
		t.Fatal("handler body ran for an unauthenticated request")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}
	if store.calls != 0 {
		t.Errorf("store was queried %d times before the 401", store.calls)
	}
}

// The OPDS surface shares the resolve and differs only in vocabulary:
// plain text instead of the Error envelope.
func TestOPDSBookScopedMissingBookAnswersInPlainText(t *testing.T) {
	h := &Handler{books: &fakeBookStore{err: repo.ErrNotFound}}

	rec := scopeRequest(t, h.opdsBookScoped(func(*gin.Context, bookScope) {
		t.Error("handler body ran for a book that does not exist")
	}), "gone", true)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `"error"`) {
		t.Errorf("body = %s, want plain text rather than the Error envelope", rec.Body.String())
	}
}

// A handler body that was previously unreachable without a database.
// ComicPagesIndex begins its real work — the format gate — only after the
// preamble it used to carry itself; with the seam holding the preamble,
// the gate is exercisable against a substituted store.
func TestComicPagesIndexRejectsANonComic(t *testing.T) {
	h := &Handler{books: &fakeBookStore{
		book: model.Book{ID: "book-1", Format: "EPUB", Path: "/tmp/x.epub"},
	}}

	rec := scopeRequest(t, h.bookScoped(h.ComicPagesIndex), "book-1", true)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not a comic book") {
		t.Errorf("body = %s, want the format-gate message", rec.Body.String())
	}
}

// The same body's second gate: a CBZ row whose file was never placed.
// Also previously unreachable — reaching it meant creating a real book.
//
// "Stored", not "on disk": since #240 the comic reader gets its bytes
// through the storage seam, so a book with no file may be missing from an
// object store rather than from a filesystem.
func TestComicPagesIndexRejectsAComicWithNoFile(t *testing.T) {
	h := &Handler{books: &fakeBookStore{
		book: model.Book{ID: "book-1", Format: "CBZ"},
	}}

	rec := scopeRequest(t, h.bookScoped(h.ComicPagesIndex), "book-1", true)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no file stored") {
		t.Errorf("body = %s, want the missing-file message", rec.Body.String())
	}
}
