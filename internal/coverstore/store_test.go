// SPDX-License-Identifier: AGPL-3.0-or-later

package coverstore

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
)

func TestSaveBookHashed_writesToExpectedPath(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	data := []byte("cover image bytes")
	hash := sha256.Sum256(data)

	if err := s.SaveBookHashed(hash[:], "image/jpeg", data); err != nil {
		t.Fatalf("SaveBookHashed: %v", err)
	}

	expected := s.hashedPath(hash[:], "image/jpeg")
	if expected == "" {
		t.Fatal("hashedPath returned empty string")
	}

	got, err := os.ReadFile(expected)
	if err != nil {
		t.Fatalf("ReadFile at expected path: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("bytes mismatch: got len=%d want len=%d", len(got), len(data))
	}

	// Verify the path layout: covers/<hash[0:2]>/<hash>.jpg
	hexHash := filepath.Base(expected)
	bucket := filepath.Base(filepath.Dir(expected))
	if len(bucket) != 2 {
		t.Fatalf("bucket dir len=%d want 2 (got %q)", len(bucket), bucket)
	}
	if len(hexHash) != len("0000000000000000000000000000000000000000000000000000000000000000.jpg") {
		// hex(32 bytes) = 64 chars + 4 for .jpg = 68
		t.Logf("hexHash=%q (ok if extension matches)", hexHash)
	}
}

func TestSaveBookHashed_roundTripsThroughOpen(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	data := []byte("round trip test")
	hash := sha256.Sum256(data)

	if err := s.SaveBookHashed(hash[:], "image/png", data); err != nil {
		t.Fatalf("SaveBookHashed: %v", err)
	}

	rc, err := s.Open(model.Book{ID: "book-rt", CoverHash: hash[:], CoverMime: "image/png"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = rc.Close() }()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("round-trip mismatch")
	}
}

func TestSaveBookHashed_twoDistinctHashes(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	d1 := []byte("cover1")
	d2 := []byte("cover2")
	h1 := sha256.Sum256(d1)
	h2 := sha256.Sum256(d2)

	if err := s.SaveBookHashed(h1[:], "image/jpeg", d1); err != nil {
		t.Fatalf("SaveBookHashed h1: %v", err)
	}
	if err := s.SaveBookHashed(h2[:], "image/jpeg", d2); err != nil {
		t.Fatalf("SaveBookHashed h2: %v", err)
	}

	p1 := s.hashedPath(h1[:], "image/jpeg")
	p2 := s.hashedPath(h2[:], "image/jpeg")
	if p1 == p2 {
		t.Fatal("distinct hashes should produce distinct paths")
	}
}

func TestSaveBookHashed_sameHashDifferentMIME(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	data := []byte("same bytes different mime")
	hash := sha256.Sum256(data)

	if err := s.SaveBookHashed(hash[:], "image/jpeg", data); err != nil {
		t.Fatalf("SaveBookHashed jpeg: %v", err)
	}
	if err := s.SaveBookHashed(hash[:], "image/png", data); err != nil {
		t.Fatalf("SaveBookHashed png: %v", err)
	}

	pJPEG := s.hashedPath(hash[:], "image/jpeg")
	pPNG := s.hashedPath(hash[:], "image/png")
	if pJPEG == pPNG {
		t.Fatal("same hash but different MIME should produce distinct paths")
	}
}

func TestSaveBookHashed_idempotent(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	data := []byte("idempotent test data")
	hash := sha256.Sum256(data)

	// First save
	if err := s.SaveBookHashed(hash[:], "image/jpeg", data); err != nil {
		t.Fatalf("first SaveBookHashed: %v", err)
	}

	// Second save — should be a no-op (file already exists)
	if err := s.SaveBookHashed(hash[:], "image/jpeg", data); err != nil {
		t.Fatalf("second SaveBookHashed (idempotent): %v", err)
	}

	// Verify only one file exists at that path (no .tmp leakage)
	p := s.hashedPath(hash[:], "image/jpeg")
	dir := filepath.Dir(p)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file after idempotent save, got %d", len(entries))
	}
}

// TestDeleteBook_removesOnlyTheLegacyCopy pins the asymmetry the module
// deliberately keeps: hashed bytes are content-addressed and may be
// shared with another book, so nothing deletes them per book. Deleting a
// book takes its legacy copy and leaves the hashed one where it is.
func TestDeleteBook_removesOnlyTheLegacyCopy(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	data := []byte("shared cover bytes")
	hash := sha256.Sum256(data)
	if err := s.SaveBookHashed(hash[:], "image/jpeg", data); err != nil {
		t.Fatalf("SaveBookHashed: %v", err)
	}
	writeLegacy(t, root, "book-del", data)

	if err := s.DeleteBook("book-del"); err != nil {
		t.Fatalf("DeleteBook: %v", err)
	}
	// Second delete of the same book must stay quiet — library delete and
	// cover clear both run it best-effort on rows that may never have had
	// a legacy file.
	if err := s.DeleteBook("book-del"); err != nil {
		t.Fatalf("DeleteBook (already gone): %v", err)
	}

	if _, err := s.Open(model.Book{ID: "book-del", CoverMime: "image/jpeg"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy copy survived DeleteBook: %v", err)
	}
	rc, err := s.Open(model.Book{ID: "other", CoverHash: hash[:], CoverMime: "image/jpeg"})
	if err != nil {
		t.Fatalf("hashed copy should survive DeleteBook: %v", err)
	}
	_ = rc.Close()
}

// writeLegacy plants bytes at the pre-backfill books/{id} path. Written
// out longhand rather than through a path builder: the legacy namespace
// is on its way out and nothing outside this package should be able to
// name it, so the test spells the layout it is asserting about.
func writeLegacy(t *testing.T, root, bookID string, data []byte) {
	t.Helper()
	p := filepath.Join(root, "books", bookID)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("MkdirAll legacy: %v", err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("WriteFile legacy: %v", err)
	}
}

func readAllAndClose(t *testing.T, rc io.ReadCloser) []byte {
	t.Helper()
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return got
}

// TestOpen_prefersHashedOverLegacy is the fallback rule's first half. A
// backfilled book has bytes in both namespaces until the legacy copy is
// swept, and the hash-keyed one is the truth.
func TestOpen_prefersHashedOverLegacy(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	hashed := []byte("the current cover")
	stale := []byte("the pre-backfill cover")
	hash := sha256.Sum256(hashed)

	if err := s.SaveBookHashed(hash[:], "image/jpeg", hashed); err != nil {
		t.Fatalf("SaveBookHashed: %v", err)
	}
	writeLegacy(t, root, "book-1", stale)

	rc, err := s.Open(model.Book{ID: "book-1", CoverHash: hash[:], CoverMime: "image/jpeg"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := readAllAndClose(t, rc); !bytes.Equal(got, hashed) {
		t.Fatalf("Open returned the legacy bytes: %q", got)
	}
}

// TestOpen_fallsBackToLegacyWhenNotBackfilled is the criterion that
// matters: a book the Covers backfill has not reached yet has no
// cover_hash at all, and its cover must still serve.
func TestOpen_fallsBackToLegacyWhenNotBackfilled(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	data := []byte("legacy only")
	writeLegacy(t, root, "book-2", data)

	rc, err := s.Open(model.Book{ID: "book-2", CoverMime: "image/jpeg"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := readAllAndClose(t, rc); !bytes.Equal(got, data) {
		t.Fatalf("Open = %q, want the legacy bytes", got)
	}
}

// TestOpen_fallsBackWhenHashedBytesAreMissing covers the half-migrated
// row: cover_hash landed but the hashed file did not, or was swept.
func TestOpen_fallsBackWhenHashedBytesAreMissing(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	data := []byte("still on the legacy path")
	writeLegacy(t, root, "book-3", data)
	phantom := sha256.Sum256([]byte("never written"))

	rc, err := s.Open(model.Book{ID: "book-3", CoverHash: phantom[:], CoverMime: "image/jpeg"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := readAllAndClose(t, rc); !bytes.Equal(got, data) {
		t.Fatalf("Open = %q, want the legacy bytes", got)
	}
}

// TestOpen_missingEverywhereIsErrNotExist keeps the 404 the two cover
// routes write. They branch on os.ErrNotExist to tell "no cover" from
// "the disk is broken", so exhausting both namespaces has to look like
// the former.
func TestOpen_missingEverywhereIsErrNotExist(t *testing.T) {
	s := New(t.TempDir())

	phantom := sha256.Sum256([]byte("nothing here"))
	if _, err := s.Open(model.Book{ID: "book-4", CoverHash: phantom[:], CoverMime: "image/jpeg"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Open with hash: got %v, want ErrNotExist", err)
	}
	if _, err := s.Open(model.Book{ID: "book-4", CoverMime: "image/jpeg"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Open without hash: got %v, want ErrNotExist", err)
	}
}

// TestMigrateLegacy_rekeysBytesAndKeepsTheSource pins the Covers
// backfill's byte half. The legacy file deliberately survives: the DB
// write that makes the new key reachable is the caller's, and a cover
// whose only copy is deleted before cover_hash lands is a cover nobody
// can serve.
func TestMigrateLegacy_rekeysBytesAndKeepsTheSource(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	data := []byte("cover to re-key")
	writeLegacy(t, root, "book-5", data)

	hash, err := s.MigrateLegacy(model.Book{ID: "book-5", CoverMime: "image/jpeg"})
	if err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}
	want := sha256.Sum256(data)
	if !bytes.Equal(hash, want[:]) {
		t.Fatalf("hash = %x, want %x", hash, want)
	}
	if _, err := os.Stat(filepath.Join(root, "books", "book-5")); err != nil {
		t.Fatalf("legacy source should survive the re-key: %v", err)
	}

	rc, err := s.Open(model.Book{ID: "book-5", CoverHash: hash, CoverMime: "image/jpeg"})
	if err != nil {
		t.Fatalf("Open after migrate: %v", err)
	}
	if got := readAllAndClose(t, rc); !bytes.Equal(got, data) {
		t.Fatalf("re-keyed bytes mismatch: %q", got)
	}
}

// TestMigrateLegacy_missingSourceIsErrNotExist keeps the backfill's skip
// path intact: a has_cover row whose file was deleted by hand is logged
// and stepped over, not retried forever.
func TestMigrateLegacy_missingSourceIsErrNotExist(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.MigrateLegacy(model.Book{ID: "book-6", CoverMime: "image/jpeg"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("MigrateLegacy: got %v, want ErrNotExist", err)
	}
}

func TestExtForMIME(t *testing.T) {
	cases := []struct {
		mime string
		want string
	}{
		{"image/jpeg", ".jpg"},
		{"image/jpg", ".jpg"},
		{"IMAGE/JPEG", ".jpg"},
		{"image/png", ".png"},
		{"image/webp", ".webp"},
		{"image/gif", ".gif"},
		{"image/avif", ".avif"},
		{"application/octet-stream", ".bin"},
		{"", ".bin"},
	}
	for _, tc := range cases {
		got := extForMIME(tc.mime)
		if got != tc.want {
			t.Errorf("extForMIME(%q) = %q, want %q", tc.mime, got, tc.want)
		}
	}
}
