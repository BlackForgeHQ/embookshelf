// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/fileproc"
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
	// files and fileID are what a test needs to say "the bytes changed",
	// which is the only way the page cache's key can be exercised: the
	// key is the files row's content hash (#329).
	files  *repo.FileRepo
	fileID string
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
	file, err := fileRepo.Insert(ctx, model.File{
		LibraryID:   lib.ID,
		BookID:      book.ID,
		Location:    comicLocation,
		Format:      "CBZ",
		Size:        1024,
		Mtime:       time.Now(),
		LastScanned: time.Now(),
	})
	if err != nil {
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
	return comicFixture{h: h, book: book, files: fileRepo, fileID: file.ID}
}

// repointComicFile records the hash of the bytes now at the book's
// location, the way the hashing pass does once a scan has noticed a file
// changed.
func repointComicFile(t *testing.T, f comicFixture, raw []byte) {
	t.Helper()
	sum := sha256.Sum256(raw)
	if err := f.files.SetContentHash(
		context.Background(), f.fileID, sum[:], int64(len(raw)), time.Now(),
	); err != nil {
		t.Fatalf("SetContentHash: %v", err)
	}
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

// The CBZ pin. Paging the other two containers (#329) routes RAR and 7z
// through an extract-once cache; ZIP must not join them — it answers a
// numbered page with a range read of that entry, which is cheaper than
// any cache, and both the bytes and the response headers it produces
// have to come out unchanged. Asserted over a page whose entry name
// lies about its type, because that is the case where "the extension
// decides" and "the bytes decide" give different answers and #331 is
// about which one CBZ picks.
func TestComicPageOnCBZIsByteIdentical(t *testing.T) {
	// A real PNG signature under a .jpg name.
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0x42}, 64)...)
	store := &objectStore{objects: map[string][]byte{
		comicLocation: cbzBytes(t, map[string][]byte{"01.jpg": png}),
	}}
	f := newComicFixture(t, store, "")

	rec := comicRequest(t, f, f.h.ComicPage, "/api/v1/books/x/comic/pages/0", "0")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), png) {
		t.Errorf("body = %d bytes, want the entry's %d bytes verbatim", rec.Body.Len(), len(png))
	}
	// The CBZ arm sets no Content-Type of its own: it streams the entry
	// straight into the response and lets net/http sniff the first bytes
	// (which is why a .jpg-named PNG reaches the browser as image/png
	// today, and why #331's extension-typing is latent rather than live).
	// A recorder does not run that sniff, so the pin is the absent
	// header — the fact that would change if the CBZ arm started
	// declaring a type from the entry name.
	if got := rec.Header().Get("Content-Type"); got != "" {
		t.Errorf("Content-Type = %q, want it left to net/http's sniff", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, max-age=86400, immutable" {
		t.Errorf("Cache-Control = %q", got)
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

// A book on the shelf as a comic whose bytes are none of the three
// containers the reader pages. All three comic extensions stamp CBZ
// (model.FormatSpecs), so anything can reach these endpoints; the answer
// is 415 with a sentence naming what can be paged, not a 500 carrying a
// parser's complaint and not a 404 implying the page might exist.
func TestComicEndpointsRefuseAnUnknownContainer(t *testing.T) {
	store := &objectStore{objects: map[string][]byte{
		comicLocation: bytes.Repeat([]byte("not-an-archive!!"), 16),
	}}
	f := newComicFixture(t, store, "")

	rec := comicRequest(t, f, f.h.ComicPagesIndex, "/api/v1/books/x/comic/pages", "")
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("pages index status = %d, want 415 (body %s)", rec.Code, rec.Body.String())
	}
	for _, ext := range []string{".cbz", ".cbr", ".cb7"} {
		if !strings.Contains(rec.Body.String(), ext) {
			t.Errorf("body = %s, want it to name %s among the containers it can page", rec.Body.String(), ext)
		}
	}

	rec = comicRequest(t, f, f.h.ComicPage, "/api/v1/books/x/comic/pages/0", "0")
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("page status = %d, want 415 (body %s)", rec.Code, rec.Body.String())
	}
}

// A RAR that announces itself and then falls apart is a damaged comic,
// not an unpageable one. Before #329 these bytes got the 415 that said
// "pages come from .cbz only", which was the right answer while RAR was
// unpageable and is the wrong one now: its owner would go looking for a
// conversion instead of for a good copy of the file.
func TestComicEndpointsOnADamagedRARDoNotSayItIsTheWrongFormat(t *testing.T) {
	store := &objectStore{objects: map[string][]byte{
		comicLocation: append([]byte("Rar!\x1a\x07\x01\x00"), bytes.Repeat([]byte{0}, 128)...),
	}}
	f := newComicFixture(t, store, "")

	rec := comicRequest(t, f, f.h.ComicPagesIndex, "/api/v1/books/x/comic/pages", "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("pages index status = %d, want 500 (body %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "not a comic archive") {
		t.Errorf("body = %s — a damaged .cbr was told it is not a comic", rec.Body.String())
	}
}

// The other half of that classification: a .cbz that IS a ZIP and is
// damaged keeps the answer it always had — a 500 — because "this file is
// broken" and "this file is in a container the reader does not page" are
// different things to be told, and the 415's sentence would send the
// owner of a damaged comic looking for a conversion they do not need.
func TestComicEndpointsOnADamagedZIPStillFail500(t *testing.T) {
	good := cbzBytes(t, comicPages)
	store := &objectStore{objects: map[string][]byte{
		// Still a ZIP by its magic; the central directory at the tail is
		// gone, so archive/zip cannot open it.
		comicLocation: good[:len(good)/2],
	}}
	f := newComicFixture(t, store, "")

	rec := comicRequest(t, f, f.h.ComicPagesIndex, "/api/v1/books/x/comic/pages", "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("pages index status = %d, want 500 (body %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), ".cbz only") {
		t.Errorf("body = %s — a damaged .cbz was told it is not a .cbz", rec.Body.String())
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

// ---------------------------------------------------------------------------
// #329: paging the other two containers
// ---------------------------------------------------------------------------

// The comic fixtures come from internal/fileproc/testdata rather than
// from a builder here: there is no pure-Go RAR or 7z writer, so the
// archives are committed next to the package that can regenerate them
// (fileproc's TestComicFixture_Generate), and the pipeline tests in
// internal/task reach for them the same way.
//
// Both hold the same comic: ComicInfo.xml, notes.txt, page10.png,
// page2.png — so two pages, and natural sort puts page2 first. The solid
// one is the shape this issue is about, where reaching a page means
// decoding everything in front of it.
const (
	comicFixtureSolidCBR = "../fileproc/testdata/comic-solid.cbr"
	comicFixtureCB7      = "../fileproc/testdata/comic.cb7"
)

// fixturePageBody is what one of those fixtures' pages holds: a real PNG
// header (so the type can be sniffed from the bytes) plus a label.
func fixturePageBody(label string) []byte {
	png := []byte{
		0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89,
	}
	return append(png, label...)
}

func readComicFixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read comic fixture: %v", err)
	}
	return b
}

// countingObjectStore records how many times a comic's bytes were asked
// for. That count is the whole point of #329: paging a solid archive
// must not reach for the object once per page.
type countingObjectStore struct {
	*objectStore
	opens atomic.Int64
}

func (s *countingObjectStore) Open(ctx context.Context, key string) (storage.Source, error) {
	s.opens.Add(1)
	return s.objectStore.Open(ctx, key)
}

// A .cbr book pages in the reader. Before #329 both endpoints answered
// 415 and the book was a download only.
//
// The archive is solid, and the open counter is the acceptance criterion
// underneath the visible one: three requests, one read of the object.
// The naive version of this feature — swapping the container behind the
// page reader — would decode pages 0..n on every request and pull the
// whole object each time on an object-store library.
func TestComicPagesAndPageOnASolidCBR(t *testing.T) {
	store := &countingObjectStore{objectStore: &objectStore{objects: map[string][]byte{
		comicLocation: readComicFixture(t, comicFixtureSolidCBR),
	}}}
	f := newComicFixture(t, store, "")
	f.h.comics = fileproc.NewPageCache(t.TempDir(), 1<<20)

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
	if index.Count != 2 {
		t.Errorf("count = %d, want 2 — the two images out of four entries", index.Count)
	}

	for _, c := range []struct {
		page string
		want []byte
	}{
		{page: "0", want: fixturePageBody("page-two")},
		{page: "1", want: fixturePageBody("ten")},
	} {
		rec = comicRequest(t, f, f.h.ComicPage, "/api/v1/books/x/comic/pages/"+c.page, c.page)
		if rec.Code != http.StatusOK {
			t.Fatalf("page %s status = %d, want 200 (body %s)", c.page, rec.Code, rec.Body.String())
		}
		if !bytes.Equal(rec.Body.Bytes(), c.want) {
			t.Errorf("page %s body = %q, want the fixture's page bytes", c.page, rec.Body.Bytes())
		}
		// Typed from the page's own leading bytes, and stated up front —
		// the extracted arm has the bytes on disk before it answers.
		if got := rec.Header().Get("Content-Type"); got != "image/png" {
			t.Errorf("page %s Content-Type = %q, want image/png", c.page, got)
		}
	}

	if got := store.opens.Load(); got != 1 {
		t.Errorf("the archive was fetched %d times for an index and two pages, want 1 — "+
			"a solid archive is being decoded per page", got)
	}
}

// The same for 7z, whose random access is at the folder level: a comic's
// pages are one solid folder, so page n costs pages 0..n exactly as a
// RAR's would, and it takes the same cure.
func TestComicPagesAndPageOnACB7(t *testing.T) {
	store := &countingObjectStore{objectStore: &objectStore{objects: map[string][]byte{
		comicLocation: readComicFixture(t, comicFixtureCB7),
	}}}
	f := newComicFixture(t, store, "")
	f.h.comics = fileproc.NewPageCache(t.TempDir(), 1<<20)

	rec := comicRequest(t, f, f.h.ComicPagesIndex, "/api/v1/books/x/comic/pages", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("pages index status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	rec = comicRequest(t, f, f.h.ComicPage, "/api/v1/books/x/comic/pages/1", "1")
	if rec.Code != http.StatusOK {
		t.Fatalf("page status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), fixturePageBody("ten")) {
		t.Errorf("page 1 body = %q, want the bytes of page10.png", rec.Body.Bytes())
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", got)
	}
	if got := store.opens.Load(); got != 1 {
		t.Errorf("the archive was fetched %d times, want 1", got)
	}
}

// The cache is keyed on the bytes, not on the book. Replacing a comic's
// file must not serve the reader its predecessor's pages — the failure a
// book-keyed cache would produce, and the reason the key is a content
// hash.
func TestComicPagesFollowAReplacedFile(t *testing.T) {
	store := &objectStore{objects: map[string][]byte{
		comicLocation: readComicFixture(t, comicFixtureSolidCBR),
	}}
	f := newComicFixture(t, store, "")
	f.h.comics = fileproc.NewPageCache(t.TempDir(), 1<<20)
	repointComicFile(t, f, store.objects[comicLocation])

	rec := comicRequest(t, f, f.h.ComicPage, "/api/v1/books/x/comic/pages/0", "0")
	if rec.Code != http.StatusOK {
		t.Fatalf("first read status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), fixturePageBody("page-two")) {
		t.Fatalf("first read body = %q", rec.Body.Bytes())
	}

	// Same book, same location, different bytes — and a files row that
	// says so, which is what the scan writes when it notices.
	store.objects[comicLocation] = cbzBytes(t, map[string][]byte{"01.png": []byte("replaced")})
	repointComicFile(t, f, store.objects[comicLocation])

	rec = comicRequest(t, f, f.h.ComicPage, "/api/v1/books/x/comic/pages/0", "0")
	if rec.Code != http.StatusOK {
		t.Fatalf("second read status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "replaced" {
		t.Errorf("after the file was replaced the reader was served %q", got)
	}
}

// A cache with no room left is a retry, not a failure of the book. It
// happens when every entry is being read by somebody else, which is
// transient by construction, so the status has to say "come back" rather
// than "something broke" — and it must not be flattened into the generic
// 500, which hides the sentence explaining it.
//
// The refusal itself is exercised under real concurrency in fileproc
// (TestPageCacheRefusesAComicItCannotFitRatherThanExceedingTheCap);
// what is asserted here is the only part that lives at this tier, which
// is what the reader is told about it.
func TestComicErrorsMapToStatuses(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
		says string
	}{
		{
			name: "page cache full",
			err:  fmt.Errorf("open: %w", fileproc.ErrPageCacheFull),
			want: http.StatusServiceUnavailable,
			says: "try again",
		},
		{
			name: "unknown container",
			err:  fmt.Errorf("open: %w", fileproc.ErrComicContainer),
			want: http.StatusUnsupportedMediaType,
			says: ".cbr",
		},
		{
			name: "no bytes stored",
			err:  fmt.Errorf("open: %w", storage.ErrNotFound),
			want: http.StatusNotFound,
		},
		{
			name: "outside the sandbox",
			err:  fmt.Errorf("open: %w", service.ErrPathOutsideRoots),
			want: http.StatusForbidden,
		},
		{
			name: "anything else",
			err:  errors.New("rardecode: bad block header"),
			want: http.StatusInternalServerError,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)

			writeComicError(c, "open comic archive", tc.err)

			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
			if tc.says != "" && !strings.Contains(rec.Body.String(), tc.says) {
				t.Errorf("body = %s, want it to mention %q", rec.Body.String(), tc.says)
			}
		})
	}
}
