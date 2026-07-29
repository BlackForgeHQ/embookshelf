// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
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

func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(b)
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

	reader, size, closer, err := handle.OpenBook(context.Background(), book)
	if err != nil {
		t.Fatalf("OpenBook: %v", err)
	}
	defer func() { _ = closer.Close() }()

	if got := readAll(t, reader); got != "epub-bytes" {
		t.Errorf("content = %q, want epub-bytes", got)
	}
	if size != int64(len("epub-bytes")) {
		t.Errorf("size = %d, want %d", size, len("epub-bytes"))
	}
}

func TestOpenBookReadsFromDiskWhenNoStorage(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, []byte("local-bytes"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	handle := &LibraryHandle{Library: model.Library{ID: "lib1"}}

	reader, size, closer, err := handle.OpenBook(context.Background(), model.Book{ID: "b1", Path: path})
	if err != nil {
		t.Fatalf("OpenBook: %v", err)
	}
	defer func() { _ = closer.Close() }()

	if got := readAll(t, reader); got != "local-bytes" {
		t.Errorf("content = %q, want local-bytes", got)
	}
	if size != int64(len("local-bytes")) {
		t.Errorf("size = %d, want %d", size, len("local-bytes"))
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

	reader, _, closer, err := handle.OpenBook(context.Background(), model.Book{ID: "b1", Format: "epub"})
	if err != nil {
		t.Fatalf("OpenBook: %v", err)
	}
	defer func() { _ = closer.Close() }()

	if got := readAll(t, reader); got != "real-bytes" {
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
		Library: model.Library{ID: "lib1"},
		Storage: &fakeStorage{objects: map[string][]byte{}},
		files:   &fakeFiles{byBook: map[string][]model.File{}},
	}

	reader, _, closer, err := handle.OpenBook(context.Background(), model.Book{ID: "b1", Path: path})
	if err != nil {
		t.Fatalf("OpenBook: %v", err)
	}
	defer func() { _ = closer.Close() }()

	if got := readAll(t, reader); got != "legacy" {
		t.Errorf("content = %q, want legacy", got)
	}
}

func TestOpenBookErrorsWhenNoBytesAvailable(t *testing.T) {
	t.Parallel()

	handle := &LibraryHandle{Library: model.Library{ID: "lib1"}}
	if _, _, _, err := handle.OpenBook(context.Background(), model.Book{ID: "b1"}); err == nil {
		t.Fatal("want an error for a book with no storage and no path, got nil")
	}
}

// ---------------------------------------------------------------------------
// IsBackendBacked — the named capability question
// ---------------------------------------------------------------------------

func TestIsBackendBacked(t *testing.T) {
	t.Parallel()

	backendID := "backend-1"
	if !(&LibraryHandle{Library: model.Library{BackendID: &backendID}}).IsBackendBacked() {
		t.Error("library with a backend id must report backend-backed")
	}
	if (&LibraryHandle{Library: model.Library{}}).IsBackendBacked() {
		t.Error("library without a backend id must report local")
	}
}

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

	reader, _, closer, err := handle.OpenBook(context.Background(), book)
	if err != nil {
		t.Fatalf("OpenBook on a local library: %v", err)
	}
	defer func() { _ = closer.Close() }()

	if got := readAll(t, reader); got != "epub-bytes" {
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

	if got := handle.storageKey(legacy); got != legacy {
		t.Errorf("storageKey(%q) = %q, want it unchanged", legacy, got)
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

	if got := handle.storageKey("Kobo Abe/dunes.epub"); got != want {
		t.Errorf("storageKey = %q, want %q", got, want)
	}
}
