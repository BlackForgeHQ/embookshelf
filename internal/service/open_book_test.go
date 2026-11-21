// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// byteSource is a storage.Source over a fixed byte slice.
type byteSource struct {
	*bytes.Reader
	closed bool
}

func (b *byteSource) Close() error { b.closed = true; return nil }
func (b *byteSource) Size() int64  { return b.Reader.Size() }

// fakeStorage embeds the interface so unimplemented methods panic loudly
// if OpenBook ever reaches for more than it should.
type fakeStorage struct {
	storage.Storage
	objects  map[string][]byte
	caps     storage.Capability
	presigns int
}

func (f *fakeStorage) Capabilities() storage.Capability { return f.caps }

func (f *fakeStorage) Open(_ context.Context, key string) (storage.Source, error) {
	b, ok := f.objects[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	return &byteSource{Reader: bytes.NewReader(b)}, nil
}

// PresignGet makes the fake satisfy Presigner, so a test can prove
// OpenBook never takes the presign path.
func (f *fakeStorage) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	f.presigns++
	return "https://signed.example/" + key, nil
}

type fakeFiles struct {
	byBook map[string][]model.File
}

func (f *fakeFiles) ListByBook(_ context.Context, bookID string) ([]model.File, error) {
	return f.byBook[bookID], nil
}

func readSource(t *testing.T, src storage.Source) string {
	t.Helper()
	b, err := io.ReadAll(io.NewSectionReader(src, 0, src.Size()))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(b)
}

// rootsOf is a fixed Book file sandbox allow-list.
func rootsOf(roots ...string) func(context.Context) []string {
	return func(context.Context) []string { return roots }
}

// ---------------------------------------------------------------------------
// OpenBook
// ---------------------------------------------------------------------------

func TestOpenBookReadsFromStorageForBackendBackedLibrary(t *testing.T) {
	t.Parallel()

	store := &fakeStorage{objects: map[string][]byte{
		"Author/Title/book.epub": []byte("epub-bytes"),
	}}
	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1"},
		Storage: store,
		files: &fakeFiles{byBook: map[string][]model.File{
			"b1": {{Location: "Author/Title/book.epub", Format: "epub"}},
		}},
	}
	// No on-disk path — this is the S3 shape that used to fail.
	book := model.Book{ID: "b1", Format: "epub", Path: ""}

	src, err := handle.OpenBook(context.Background(), book)
	if err != nil {
		t.Fatalf("OpenBook: %v", err)
	}
	defer func() { _ = src.Close() }()

	if got := readSource(t, src); got != "epub-bytes" {
		t.Errorf("content = %q, want epub-bytes", got)
	}
	if src.Size() != int64(len("epub-bytes")) {
		t.Errorf("size = %d, want %d", src.Size(), len("epub-bytes"))
	}
}

func TestOpenBookReadsFromDiskWhenNoStorage(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, []byte("local-bytes"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	handle := &LibraryHandle{Library: model.Library{ID: "lib1"}, sandboxRoots: rootsOf(filepath.Dir(path))}

	src, err := handle.OpenBook(context.Background(), model.Book{ID: "b1", Path: path})
	if err != nil {
		t.Fatalf("OpenBook: %v", err)
	}
	defer func() { _ = src.Close() }()

	if got := readSource(t, src); got != "local-bytes" {
		t.Errorf("content = %q, want local-bytes", got)
	}
	if src.Size() != int64(len("local-bytes")) {
		t.Errorf("size = %d, want %d", src.Size(), len("local-bytes"))
	}
}

// Presign is a routing answer for the browser. A caller that wants the
// bytes in-process must get bytes, never a URL — even when the library
// is configured for presigned delivery.
func TestOpenBookNeverPresigns(t *testing.T) {
	t.Parallel()

	store := &fakeStorage{
		objects: map[string][]byte{"k.epub": []byte("real-bytes")},
		caps:    storage.CapPresign,
	}
	handle := &LibraryHandle{
		Library:         model.Library{ID: "lib1"},
		Storage:         store,
		files:           &fakeFiles{byBook: map[string][]model.File{"b1": {{Location: "k.epub", Format: "epub"}}}},
		presignFallback: BookDeliveryPresign,
		presignTTL:      time.Minute,
	}

	src, err := handle.OpenBook(context.Background(), model.Book{ID: "b1", Format: "epub"})
	if err != nil {
		t.Fatalf("OpenBook: %v", err)
	}
	defer func() { _ = src.Close() }()

	if got := readSource(t, src); got != "real-bytes" {
		t.Errorf("content = %q, want real-bytes", got)
	}
	if store.presigns != 0 {
		t.Errorf("OpenBook issued %d presigned URLs, want 0", store.presigns)
	}
}

// A book with no files row still opens when it has a legacy on-disk
// path — the same fallback BookSource makes.
func TestOpenBookFallsBackToPathWhenNoFilesRow(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "legacy.epub")
	if err := os.WriteFile(path, []byte("legacy"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	handle := &LibraryHandle{
		Library:      model.Library{ID: "lib1"},
		Storage:      &fakeStorage{objects: map[string][]byte{}},
		files:        &fakeFiles{byBook: map[string][]model.File{}},
		sandboxRoots: rootsOf(filepath.Dir(path)),
	}

	src, err := handle.OpenBook(context.Background(), model.Book{ID: "b1", Path: path})
	if err != nil {
		t.Fatalf("OpenBook: %v", err)
	}
	defer func() { _ = src.Close() }()

	if got := readSource(t, src); got != "legacy" {
		t.Errorf("content = %q, want legacy", got)
	}
}

func TestOpenBookErrorsWhenNoBytesAvailable(t *testing.T) {
	t.Parallel()

	handle := &LibraryHandle{Library: model.Library{ID: "lib1"}}
	_, err := handle.OpenBook(context.Background(), model.Book{ID: "b1"})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist — a book with no files row and no path is not placed", err)
	}
}

// ---------------------------------------------------------------------------
// IsObjectStore — the named capability question
// ---------------------------------------------------------------------------

// Asked of the adapter, so a backend id is not the answer: the
// storage-v2 backfill gave every migrated local library one (#202).
func TestIsObjectStore(t *testing.T) {
	t.Parallel()

	localFS, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	backendID := "backend-1"

	if (&LibraryHandle{Library: model.Library{BackendID: &backendID}, Storage: localFS}).IsObjectStore() {
		t.Error("a local backend must not report as an object store, backend row or not")
	}
	if (&LibraryHandle{Storage: objectStoreStub{}}).IsObjectStore() != true {
		t.Error("a backend advertising CapObjectStore must report as one")
	}
	// A library whose backend could not be resolved is unusable, not an
	// object store; every caller guards Storage separately.
	if (&LibraryHandle{Library: model.Library{BackendID: &backendID}}).IsObjectStore() {
		t.Error("a handle with no Storage must not report as an object store")
	}
}

// objectStoreStub advertises the one capability under test.
type objectStoreStub struct{ storage.Storage }

func (objectStoreStub) Capabilities() storage.Capability { return storage.CapObjectStore }

// A local library stores files.location relative to the library root
// (CONTEXT, "Files row"), while its LocalFS is rooted at "/" and expects
// absolute keys (internal/storageloader). Nothing reconciled the two, so
// opening a book on a local library asked the filesystem for
// "/Author/Title/book.epub" and got nothing.
//
// The symptom was silent rather than loud: reading-guide generation
// degrades an unreadable book to a metadata-only guide by design, so
// every EPUB on a local library quietly produced the weaker guide that
// ADR-0024 §2 reserves for formats with no extractable text.
func TestOpenBookResolvesRelativeLocationsOnALocalLibrary(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "Kobo Abe", "Woman in the Dunes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dunes.epub"), []byte("epub-bytes"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Exactly what boot builds for an install with no storage backend.
	rootedAtSlash, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", Root: &root},
		Storage: rootedAtSlash,
		files: &fakeFiles{byBook: map[string][]model.File{
			"b1": {{Location: "Kobo Abe/Woman in the Dunes/dunes.epub", Format: "EPUB"}},
		}},
	}
	// books.path is relative too on a storage-v2 approve, so there is no
	// second chance behind this.
	book := model.Book{ID: "b1", Format: "EPUB", Path: "Kobo Abe/Woman in the Dunes/dunes.epub"}

	src, err := handle.OpenBook(context.Background(), book)
	if err != nil {
		t.Fatalf("OpenBook on a local library: %v", err)
	}
	defer func() { _ = src.Close() }()

	if got := readSource(t, src); got != "epub-bytes" {
		t.Errorf("content = %q, want epub-bytes", got)
	}
}

// A legacy row holds an absolute path: books.path predates storage-v2,
// and the storage-v2 backfill wrote files.location verbatim whenever the
// library root was unknown at seed time
// (migrator.seedFilesFromBooks). Those strings are already the key a
// "/"-rooted LocalFS wants, so joining them onto the root would ask for
// /lib/root/lib/root/... and find nothing.
//
// The shim has to be total over both shapes, because the edit-side write
// pipeline reads books.path — which is mixed — through it.
func TestStorageKeyLeavesALegacyAbsolutePathAlone(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	rootedAtSlash, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", Root: &root},
		Storage: rootedAtSlash,
	}

	legacy := filepath.Join(root, "Kobo Abe", "Woman in the Dunes", "dunes.epub")

	if got := handle.StorageKey(legacy); got != legacy {
		t.Errorf("StorageKey(%q) = %q, want it unchanged", legacy, got)
	}
}

// The relative shape still resolves against the root, which is the case
// the shim was written for.
func TestStorageKeyResolvesARelativeLocationAgainstTheRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	rootedAtSlash, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", Root: &root},
		Storage: rootedAtSlash,
	}

	want := filepath.Join(root, "Kobo Abe", "dunes.epub")

	if got := handle.StorageKey("Kobo Abe/dunes.epub"); got != want {
		t.Errorf("storageKey = %q, want %q", got, want)
	}
}

// The storage-v2 backfill wired every pre-existing local library to a
// kind=local backend row, so libraries.backend_id is non-NULL for them.
// Libraries created since get NULL. The two shapes must behave
// identically — the column says a backend row exists, not that the bytes
// live in an object store (#202).
func TestALocalLibraryWithABackendRowResolvesKeysLikeOneWithout(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	rootedAtSlash, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	backendID := "backend-1"

	migrated := &LibraryHandle{
		// What the backfill leaves behind: a local library pointing at a
		// kind=local backend row, resolved to the same "/"-rooted store.
		Library: model.Library{ID: "lib1", Root: &root, BackendID: &backendID},
		Storage: rootedAtSlash,
	}
	fresh := &LibraryHandle{
		Library: model.Library{ID: "lib2", Root: &root},
		Storage: rootedAtSlash,
	}

	const location = "Kobo Abe/Woman in the Dunes/dunes.epub"
	want := filepath.Join(root, "Kobo Abe", "Woman in the Dunes", "dunes.epub")

	if got := fresh.StorageKey(location); got != want {
		t.Fatalf("fresh library: StorageKey = %q, want %q", got, want)
	}
	if got := migrated.StorageKey(location); got != want {
		t.Errorf("migrated library: StorageKey = %q, want %q — a kind=local backend row "+
			"made it answer as though its bytes were in an object store", got, want)
	}
}

// ---------------------------------------------------------------------------
// FileSource — the delivery decision, asked of one location
// ---------------------------------------------------------------------------

// The decision a deployment configured applies to every rendition, not
// just to the primary file. The narration path used to answer this for
// itself and always streamed, so an install that turned presign on
// redirected its EPUBs and piped its half-gigabyte MP3s through the app.
func TestFileSourcePresignsWhateverRenditionIsAskedFor(t *testing.T) {
	t.Parallel()

	store := &fakeStorage{caps: storage.CapObjectStore | storage.CapPresign}
	handle := &LibraryHandle{
		Library:         model.Library{ID: "lib1"},
		Storage:         store,
		presignFallback: BookDeliveryPresign,
		presignTTL:      time.Minute,
	}

	src, err := handle.FileSource(context.Background(), "Author/Title/book.mp3")
	if err != nil {
		t.Fatalf("FileSource: %v", err)
	}
	if src.Kind != BookDeliveryPresign {
		t.Fatalf("kind = %q, want %q", src.Kind, BookDeliveryPresign)
	}
	if src.URL != "https://signed.example/Author/Title/book.mp3" {
		t.Errorf("URL = %q, want the signed URL for the location as stored", src.URL)
	}
}

// Streaming is the default, and the key it streams is the one this
// library's Storage answers to: a local install's LocalFS is rooted at
// "/", so the library-relative location has to be rooted first.
func TestFileSourceStreamsALocalLibraryAtItsRootedKey(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	rootedAtSlash, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", Root: &root},
		Storage: rootedAtSlash,
	}

	src, err := handle.FileSource(context.Background(), "Author/Title/book.mp3")
	if err != nil {
		t.Fatalf("FileSource: %v", err)
	}
	if src.Kind != BookDeliveryStream {
		t.Fatalf("kind = %q, want %q", src.Kind, BookDeliveryStream)
	}
	if want := filepath.Join(root, "Author/Title/book.mp3"); src.Key != want {
		t.Errorf("key = %q, want %q", src.Key, want)
	}
}

// An object store owns its own per-library prefix, so a stored location
// is already the key — and a nil Storage is the legacy install with no
// backend wired at all, which can still hand the handler a path.
func TestFileSourceFallsBackToTheLocalPathWithoutStorage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	handle := &LibraryHandle{Library: model.Library{ID: "lib1", Root: &root}, sandboxRoots: rootsOf(root)}

	src, err := handle.FileSource(context.Background(), "Author/Title/book.mp3")
	if err != nil {
		t.Fatalf("FileSource: %v", err)
	}
	if src.Kind != BookDeliveryLocal {
		t.Fatalf("kind = %q, want %q", src.Kind, BookDeliveryLocal)
	}
	if want := filepath.Join(root, "Author/Title/book.mp3"); src.Path != want {
		t.Errorf("path = %q, want %q", src.Path, want)
	}
}

// ---------------------------------------------------------------------------
// The Book file sandbox, applied by the handle
// ---------------------------------------------------------------------------

// failingFiles is a files table that cannot be read.
type failingFiles struct{ err error }

func (f failingFiles) ListByBook(context.Context, string) ([]model.File, error) { return nil, f.err }

// writeBook puts a real file at dir/name, so a refusal cannot be a
// missing file in disguise.
func writeBook(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("bytes"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// books.path is a stored string, and the sandbox exists because it is
// not trusted to stay inside a library. The handle used to open it with
// a bare os.Open for every in-process reader — text, guides, Kindle,
// device push, the converter — while only the HTTP layer gated it.
func TestOpenBookRefusesALegacyPathOutsideTheSandbox(t *testing.T) {
	t.Parallel()

	outside := writeBook(t, t.TempDir(), "elsewhere.epub")
	handle := &LibraryHandle{
		Library:      model.Library{ID: "lib1"},
		files:        &fakeFiles{byBook: map[string][]model.File{}},
		sandboxRoots: rootsOf(t.TempDir()),
	}

	_, err := handle.OpenBook(context.Background(), model.Book{ID: "b1", Path: outside})
	if !errors.Is(err, ErrPathOutsideRoots) {
		t.Fatalf("err = %v, want ErrPathOutsideRoots", err)
	}
}

// No roots at all admits nothing: a handle built without them fails
// closed rather than reading wherever books.path points.
func TestOpenBookWithNoSandboxRootsRefusesALegacyPath(t *testing.T) {
	t.Parallel()

	path := writeBook(t, t.TempDir(), "legacy.epub")
	handle := &LibraryHandle{Library: model.Library{ID: "lib1"}}

	_, err := handle.OpenBook(context.Background(), model.Book{ID: "b1", Path: path})
	if !errors.Is(err, ErrPathOutsideRoots) {
		t.Fatalf("err = %v, want ErrPathOutsideRoots", err)
	}
}

// A files lookup that failed is not a book with no files row. Reading
// books.path then would read this machine's disk for a library whose
// bytes may not be on it — so it is an error, even with a path that the
// sandbox would admit.
func TestOpenBookFailedFilesLookupNeverFallsBackToThePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeBook(t, dir, "legacy.epub")
	handle := &LibraryHandle{
		Library:      model.Library{ID: "lib1"},
		Storage:      &fakeStorage{objects: map[string][]byte{}},
		files:        failingFiles{err: errors.New("connection reset")},
		sandboxRoots: rootsOf(dir),
	}

	_, err := handle.OpenBook(context.Background(), model.Book{ID: "b1", Path: path})
	if err == nil {
		t.Fatal("OpenBook read the legacy path after the files lookup failed")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v — a broken lookup was reported as a book that is not placed", err)
	}
}

// The identity is the strongest the bytes allow, and finding it opens
// nothing: a warm cache answers from the identity alone.
func TestLocateBookIdentity(t *testing.T) {
	t.Parallel()

	mtime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for name, tc := range map[string]struct {
		row  model.File
		want string
	}{
		"content hash": {
			row:  model.File{ID: "f1", Location: "k", Format: "epub", ContentHash: []byte{0xab, 0xcd}},
			want: "sha256:abcd",
		},
		"no hash yet: the row's own facts": {
			row:  model.File{ID: "f1", Location: "k", Format: "epub", Size: 7, Mtime: mtime},
			want: "file:f1:7:" + strconv.FormatInt(mtime.UnixNano(), 10),
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := &countingStorage{fakeStorage: &fakeStorage{objects: map[string][]byte{"k": []byte("x")}}}
			handle := &LibraryHandle{
				Library: model.Library{ID: "lib1"},
				Storage: store,
				files:   &fakeFiles{byBook: map[string][]model.File{"b1": {tc.row}}},
			}

			located, err := handle.LocateBook(context.Background(), model.Book{ID: "b1", Format: "epub"})
			if err != nil {
				t.Fatalf("LocateBook: %v", err)
			}
			if located.Identity != tc.want {
				t.Errorf("identity = %q, want %q", located.Identity, tc.want)
			}
			if store.opens != 0 {
				t.Errorf("LocateBook opened the bytes %d times, want 0", store.opens)
			}
		})
	}

	t.Run("legacy path: the sandboxed path's own facts", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := writeBook(t, dir, "legacy.epub")
		handle := &LibraryHandle{Library: model.Library{ID: "lib1"}, sandboxRoots: rootsOf(dir)}

		located, err := handle.LocateBook(context.Background(), model.Book{ID: "b1", Path: path})
		if err != nil {
			t.Fatalf("LocateBook: %v", err)
		}
		if !strings.HasPrefix(located.Identity, "path:"+path+":5:") {
			t.Errorf("identity = %q, want path:%s:5:<mtime>", located.Identity, path)
		}
	})
}

type countingStorage struct {
	*fakeStorage
	opens int
}

func (c *countingStorage) Open(ctx context.Context, key string) (storage.Source, error) {
	c.opens++
	return c.fakeStorage.Open(ctx, key)
}

// A "local" delivery answer leaves the handle already sandboxed: the
// handler streams the path as given and holds no copy of the gate.
func TestBookSourceSandboxesALegacyPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	inside := writeBook(t, dir, "inside.epub")
	outside := writeBook(t, t.TempDir(), "outside.epub")
	handle := &LibraryHandle{Library: model.Library{ID: "lib1"}, sandboxRoots: rootsOf(dir)}

	src, err := handle.BookSource(context.Background(), model.Book{ID: "b1", Path: inside})
	if err != nil {
		t.Fatalf("BookSource inside the sandbox: %v", err)
	}
	if src.Kind != BookDeliveryLocal || src.Path != inside {
		t.Errorf("source = %+v, want local %q", src, inside)
	}

	if _, err := handle.BookSource(context.Background(), model.Book{ID: "b1", Path: outside}); !errors.Is(err, ErrPathOutsideRoots) {
		t.Errorf("BookSource outside the sandbox: err = %v, want ErrPathOutsideRoots", err)
	}
}

// rootLibraries is a library catalog for the store's sandbox roots.
type rootLibraries []model.Library

func (r rootLibraries) GetByID(_ context.Context, id string) (model.Library, error) {
	for _, lib := range r {
		if lib.ID == id {
			return lib, nil
		}
	}
	return model.Library{}, errors.New("not found")
}

func (r rootLibraries) List(context.Context) ([]model.Library, error) { return r, nil }

// Handles from the store carry the production roots: BookDrop plus every
// library on a filesystem, the same list deletion is gated by.
func TestStoreHandlesSandboxToBookDropAndLibraryRoots(t *testing.T) {
	t.Parallel()

	libRoot, bookDrop := t.TempDir(), t.TempDir()
	libs := rootLibraries{{ID: "lib1", Root: &libRoot}}
	store := NewLibraryStore(LibraryStoreDeps{Libs: libs, Libraries: libs, BookDropPath: bookDrop})
	handle, err := store.For(context.Background(), "lib1")
	if err != nil {
		t.Fatalf("For: %v", err)
	}

	for _, path := range []string{writeBook(t, libRoot, "a.epub"), writeBook(t, bookDrop, "b.epub")} {
		src, err := handle.OpenBook(context.Background(), model.Book{ID: "b1", Path: path})
		if err != nil {
			t.Errorf("OpenBook(%s): %v", path, err)
			continue
		}
		_ = src.Close()
	}
	outside := writeBook(t, t.TempDir(), "c.epub")
	if _, err := handle.OpenBook(context.Background(), model.Book{ID: "b1", Path: outside}); !errors.Is(err, ErrPathOutsideRoots) {
		t.Errorf("OpenBook outside both roots: err = %v, want ErrPathOutsideRoots", err)
	}
}
