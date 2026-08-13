// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
)

// comicPages are the archive entries every fixture below is built from.
// Out of filename order on purpose: the reader sorts naturally, and the
// responses are asserted against the sorted result.
var comicPages = map[string][]byte{
	"03.png": []byte("three"),
	"01.png": []byte("one"),
	"02.png": []byte("two"),
}

// ---------------------------------------------------------------------------
// Object-store fake
// ---------------------------------------------------------------------------

// objectStore is a Storage that reports itself an object store — the
// shape of library the comic endpoints could not read at all, because a
// page reader that takes a filesystem path has nothing to be handed for a
// library whose bytes have no path (#240).
type objectStore struct {
	storage.Storage
	objects map[string][]byte
}

func (s *objectStore) Capabilities() storage.Capability {
	return storage.CapObjectStore | storage.CapRange
}

func (s *objectStore) Open(_ context.Context, key string) (storage.Source, error) {
	b, ok := s.objects[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return objectSource{Reader: bytes.NewReader(b), size: int64(len(b))}, nil
}

type objectSource struct {
	*bytes.Reader
	size int64
}

func (o objectSource) Size() int64  { return o.size }
func (o objectSource) Close() error { return nil }

// cbzBytes builds a CBZ archive in memory.
func cbzBytes(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// ---------------------------------------------------------------------------
// Fixture
// ---------------------------------------------------------------------------

// comicFixture is a handler wired to one library, with one CBZ book in
// it, over a real LibraryStore — real enough that the storage key rule
// and the files-row lookup are the ones production runs, since those are
// what decide whether the bytes are findable at all.
type comicFixture struct {
	h    *Handler
	book model.Book
}

const comicLocation = "Brian K. Vaughan/Saga 1/saga-01.cbz"

// newComicFixture builds the fixture over the given Storage. libRoot is
// the library's local path — empty for an object-store library, which by
// design has none.
func newComicFixture(t *testing.T, store storage.Storage, libRoot string) comicFixture {
	t.Helper()
	ctx := context.Background()
	d := repotest.New(t)
	libRepo := repo.NewLibraryRepo(d)
	bookRepo := repo.NewBookRepo(d)
	fileRepo := repo.NewFileRepo(d)

	lib, err := libRepo.CreateLibrary(ctx, "Comics", "comics", libRoot, nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	// No books.path: that column is the legacy single-path field, and an
	// object-store library has nothing to put in it. The old handler
	// answered 404 on exactly this before it reached any bytes.
	book, err := bookRepo.Create(ctx, model.Book{
		LibraryID: lib.ID,
		Title:     "Saga #1",
		Author:    "Brian K. Vaughan",
		Format:    "CBZ",
	})
	if err != nil {
		t.Fatalf("Create book: %v", err)
	}
	if _, err := fileRepo.Insert(ctx, model.File{
		LibraryID:   lib.ID,
		BookID:      book.ID,
		Location:    comicLocation,
		Format:      "CBZ",
		Size:        1024,
		Mtime:       time.Now(),
		LastScanned: time.Now(),
	}); err != nil {
		t.Fatalf("Insert file: %v", err)
	}

	h := &Handler{
		books: bookRepo,
		libStore: service.NewLibraryStore(service.LibraryStoreDeps{
			Libs:     libRepo,
			Resolver: storage.ConstantResolver{S: store},
			Files:    fileRepo,
		}),
	}
	return comicFixture{h: h, book: book}
}

// comicRequest drives one comic handler body with a resolved scope, the
// way bookScoped would have.
func comicRequest(t *testing.T, f comicFixture, fn bookHandler, target, page string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	if page != "" {
		c.Params = gin.Params{{Key: "n", Value: page}}
	}
	fn(c, bookScope{UserID: "u1", Book: f.book})
	return rec
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// Comic pagination on an object-store-backed library. This could not be
// attempted before: the page reader took a filesystem path, and a library
// whose bytes live in an object store has none to give it, so every comic
// on such a library was unreadable and nothing said so.
func TestComicPagesAndPageOnAnObjectStoreBackedLibrary(t *testing.T) {
	store := &objectStore{objects: map[string][]byte{
		comicLocation: cbzBytes(t, comicPages),
	}}
	f := newComicFixture(t, store, "")

	rec := comicRequest(t, f, f.h.ComicPagesIndex, "/api/v1/books/x/comic/pages", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("pages index status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var index struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &index); err != nil {
		t.Fatalf("decode index: %v (body %s)", err, rec.Body.String())
	}
	if index.Count != 3 {
		t.Errorf("count = %d, want 3", index.Count)
	}

	rec = comicRequest(t, f, f.h.ComicPage, "/api/v1/books/x/comic/pages/1", "1")
	if rec.Code != http.StatusOK {
		t.Fatalf("page status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "two" {
		t.Errorf("page 1 body = %q, want the bytes of 02.png", got)
	}
}

// The local library, unchanged. Same endpoints, same answers, over the
// backend a local install boots.
func TestComicPagesAndPageOnALocalLibrary(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(comicLocation))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, cbzBytes(t, comicPages), 0o600); err != nil {
		t.Fatalf("write cbz: %v", err)
	}
	// Rooted at "/", which is what storageloader builds for a local
	// install (ADR-0030 §1).
	fs, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	f := newComicFixture(t, fs, root)

	rec := comicRequest(t, f, f.h.ComicPagesIndex, "/api/v1/books/x/comic/pages", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("pages index status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var index struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &index); err != nil {
		t.Fatalf("decode index: %v (body %s)", err, rec.Body.String())
	}
	if index.Count != 3 {
		t.Errorf("count = %d, want 3", index.Count)
	}

	rec = comicRequest(t, f, f.h.ComicPage, "/api/v1/books/x/comic/pages/2", "2")
	if rec.Code != http.StatusOK {
		t.Fatalf("page status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "three" {
		t.Errorf("page 2 body = %q, want the bytes of 03.png", got)
	}
}

// A non-comic book is still refused before any bytes are touched.
func TestComicEndpointsRefuseANonComic(t *testing.T) {
	store := &objectStore{objects: map[string][]byte{}}
	f := newComicFixture(t, store, "")
	f.book.Format = "EPUB"

	rec := comicRequest(t, f, f.h.ComicPagesIndex, "/api/v1/books/x/comic/pages", "")
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("pages index status = %d, want 415", rec.Code)
	}
	rec = comicRequest(t, f, f.h.ComicPage, "/api/v1/books/x/comic/pages/0", "0")
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("page status = %d, want 415", rec.Code)
	}
}

// A RAR- or 7z-packed comic is stamped CBZ like every other comic
// (model.FormatSpecs) and reaches the shelf since #310, but its pages are
// in a container these endpoints do not page through. That is the
// format's answer — 415 with a sentence saying so — not a 500 carrying a
// zip parser's complaint, and not a 404 implying the page might exist.
func TestComicEndpointsRefuseANonZIPComic(t *testing.T) {
	store := &objectStore{objects: map[string][]byte{
		// A RAR signature is enough: what matters is that it is not a ZIP.
		comicLocation: append([]byte("Rar!\x1a\x07\x01\x00"), bytes.Repeat([]byte{0}, 128)...),
	}}
	f := newComicFixture(t, store, "")

	rec := comicRequest(t, f, f.h.ComicPagesIndex, "/api/v1/books/x/comic/pages", "")
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("pages index status = %d, want 415 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), ".cbz") {
		t.Errorf("body = %s, want it to say which comics can be paged", rec.Body.String())
	}

	rec = comicRequest(t, f, f.h.ComicPage, "/api/v1/books/x/comic/pages/0", "0")
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("page status = %d, want 415 (body %s)", rec.Code, rec.Body.String())
	}
}

// A book whose object is not in the store answers 404 rather than a 500
// or a truncated body.
func TestComicPagesMissingObjectIsNotFound(t *testing.T) {
	store := &objectStore{objects: map[string][]byte{}}
	f := newComicFixture(t, store, "")

	rec := comicRequest(t, f, f.h.ComicPagesIndex, "/api/v1/books/x/comic/pages", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

// An out-of-range page is a 404, and the local path is where that used to
// be exercised.
func TestComicPageOutOfRange(t *testing.T) {
	store := &objectStore{objects: map[string][]byte{
		comicLocation: cbzBytes(t, comicPages),
	}}
	f := newComicFixture(t, store, "")

	rec := comicRequest(t, f, f.h.ComicPage, "/api/v1/books/x/comic/pages/99", "99")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

// failingLibStore resolves nothing. It stands for a transient failure —
// the catalog row is there, the lookup broke — which is a different
// thing from the documented no-LibraryStore degrade.
type failingLibStore struct{ err error }

func (f failingLibStore) For(context.Context, string) (*service.LibraryHandle, error) {
	return nil, f.err
}

// A library that cannot be resolved must not fall through to reading the
// book's legacy path off local disk. handler.Options documents one
// fallback — no LibraryStore wired — and a resolve error is not it: on an
// object-store library the bytes are not on this machine at all, and
// silently answering from disk is how a stale local copy gets served in
// place of the real file.
func TestComicResolveFailureDoesNotFallBackToDisk(t *testing.T) {
	f := newComicFixture(t, &objectStore{objects: map[string][]byte{}}, "")
	f.h.libStore = failingLibStore{err: errors.New("resolve: connection reset")}

	rec := comicRequest(t, f, f.h.ComicPagesIndex, "/api/v1/books/x/comic/pages", "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 — a broken resolve was reported as something else (body %s)",
			rec.Code, rec.Body.String())
	}
}

// A book carrying a legacy path and no files row must still pass the
// allow-list. serveBookFile gates exactly this read; before CBZ moved
// onto the storage seam the comic handler gated it too, with its own
// copy. Routing through the seam must not have dropped the gate — a
// books.path is a stored string, and the sandbox exists because it is
// not trusted to stay inside a library.
func TestComicRefusesALegacyPathOutsideTheSandbox(t *testing.T) {
	ctx := context.Background()
	d := repotest.New(t)
	libRepo := repo.NewLibraryRepo(d)
	bookRepo := repo.NewBookRepo(d)
	fileRepo := repo.NewFileRepo(d)

	libRoot := t.TempDir()
	lib, err := libRepo.CreateLibrary(ctx, "Comics", "comics", libRoot, nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	// Outside every root, and real, so a missing file cannot be what
	// makes this pass.
	outside := filepath.Join(t.TempDir(), "elsewhere.cbz")
	if err := os.WriteFile(outside, cbzBytes(t, comicPages), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	book, err := bookRepo.Create(ctx, model.Book{
		LibraryID: lib.ID,
		Title:     "Escapee",
		Author:    "Nobody",
		Format:    "CBZ",
		Path:      outside,
	})
	if err != nil {
		t.Fatalf("Create book: %v", err)
	}

	h := &Handler{
		books: bookRepo,
		lib:   nil,
		libStore: service.NewLibraryStore(service.LibraryStoreDeps{
			Libs:     libRepo,
			Resolver: storage.ConstantResolver{S: &objectStore{objects: map[string][]byte{}}},
			Files:    fileRepo,
		}),
	}
	f := comicFixture{h: h, book: book}

	rec := comicRequest(t, f, f.h.ComicPagesIndex, "/api/v1/books/x/comic/pages", "")
	if rec.Code == http.StatusOK {
		t.Fatalf("status = 200 — a path outside every library root was served (body %s)", rec.Body.String())
	}
}
