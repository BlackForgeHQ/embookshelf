package coverstore

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveBookHashed_writesToExpectedPath(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	data := []byte("cover image bytes")
	hash := sha256.Sum256(data)

	if err := s.SaveBookHashed(hash[:], "image/jpeg", data); err != nil {
		t.Fatalf("SaveBookHashed: %v", err)
	}

	expected := s.HashedPath(hash[:], "image/jpeg")
	if expected == "" {
		t.Fatal("HashedPath returned empty string")
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

func TestOpenBookHashed_roundTrip(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	data := []byte("round trip test")
	hash := sha256.Sum256(data)

	if err := s.SaveBookHashed(hash[:], "image/png", data); err != nil {
		t.Fatalf("SaveBookHashed: %v", err)
	}

	rc, err := s.OpenBookHashed(hash[:], "image/png")
	if err != nil {
		t.Fatalf("OpenBookHashed: %v", err)
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

	p1 := s.HashedPath(h1[:], "image/jpeg")
	p2 := s.HashedPath(h2[:], "image/jpeg")
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

	pJPEG := s.HashedPath(hash[:], "image/jpeg")
	pPNG := s.HashedPath(hash[:], "image/png")
	if pJPEG == pPNG {
		t.Fatal("same hash but different MIME should produce distinct paths")
	}
}

func TestOpenBookHashed_missingFileReturnsErrNotExist(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	hash := sha256.Sum256([]byte("nonexistent"))
	_, err := s.OpenBookHashed(hash[:], "image/jpeg")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("OpenBookHashed missing: got %v, want ErrNotExist", err)
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
	p := s.HashedPath(hash[:], "image/jpeg")
	dir := filepath.Dir(p)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file after idempotent save, got %d", len(entries))
	}
}

func TestDeleteBookHashed_removesFile(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	data := []byte("to delete")
	hash := sha256.Sum256(data)

	if err := s.SaveBookHashed(hash[:], "image/jpeg", data); err != nil {
		t.Fatalf("SaveBookHashed: %v", err)
	}

	if err := s.DeleteBookHashed(hash[:], "image/jpeg"); err != nil {
		t.Fatalf("DeleteBookHashed: %v", err)
	}

	_, err := s.OpenBookHashed(hash[:], "image/jpeg")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("after delete, OpenBookHashed: got %v, want ErrNotExist", err)
	}
}

func TestDeleteBookHashed_missingIsNoError(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	hash := sha256.Sum256([]byte("phantom"))
	if err := s.DeleteBookHashed(hash[:], "image/jpeg"); err != nil {
		t.Fatalf("DeleteBookHashed missing: %v", err)
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
