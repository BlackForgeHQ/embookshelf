// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
)

// putRecordingStorage captures what a backend placement wrote where.
type putRecordingStorage struct {
	fakeStorage
	puts map[string][]byte
}

// These doubles stand in for an object store, and it is the advertised
// capability that says so now — a backend id no longer does (#202).
func (*putRecordingStorage) Capabilities() storage.Capability { return storage.CapObjectStore }

func (p *putRecordingStorage) Put(_ context.Context, key string, r io.Reader, _ ...storage.PutOption) (storage.PutResult, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return storage.PutResult{}, err
	}
	if p.puts == nil {
		p.puts = map[string][]byte{}
	}
	p.puts[key] = b
	return storage.PutResult{}, nil
}

func tempMP3(t *testing.T, body string) string {
	t.Helper()
	// Deliberately a temp-file name, because that is what finalize hands
	// over and the point of these tests is that it does not survive.
	f, err := os.CreateTemp(t.TempDir(), "embookshelf-audiobook-*.mp3")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	if _, err := f.WriteString(body); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = f.Close()
	return f.Name()
}

func narratedBook(folder string) model.Book {
	b := model.Book{ID: "b1", LibraryID: "lib1", Format: "EPUB", Author: "Kōbō Abe", Title: "Woman in the Dunes"}
	if folder != "" {
		b.FolderPath = &folder
	}
	return b
}

// The whole point of ADR-0025 is that narration is another file of the
// same book. Landing it in a sibling "Title (2)" folder is what Library
// scan would later turn into a second book.
func TestPlaceNarrationWritesIntoTheBooksOwnFolder(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	folder := filepath.Join("Kōbō Abe", "Woman in the Dunes")
	// The book's folder already exists, holding its EPUB — the exact
	// condition that made the generic Placer pick a "(2)" suffix.
	if err := os.MkdirAll(filepath.Join(root, folder), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, folder, "book.epub"), []byte("epub"), 0o600); err != nil {
		t.Fatalf("write epub: %v", err)
	}

	handle := &LibraryHandle{Library: model.Library{ID: "lib1", Root: &root}}
	got, err := handle.PlaceNarration(context.Background(), narratedBook(folder), tempMP3(t, "mp3-bytes"))
	if err != nil {
		t.Fatalf("PlaceNarration: %v", err)
	}

	want := filepath.Join(folder, "Woman in the Dunes.mp3")
	if got.Location != want {
		t.Errorf("Location = %q, want %q", got.Location, want)
	}
	// And the EPUB is still its neighbour, in one folder.
	if _, err := os.Stat(filepath.Join(root, folder, "book.epub")); err != nil {
		t.Errorf("the book's EPUB is no longer beside its narration: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, want)); err != nil {
		t.Errorf("narration not at %q: %v", want, err)
	}
	// Nothing may have been created alongside.
	entries, _ := os.ReadDir(filepath.Join(root, "Kōbō Abe"))
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("author folder holds %v, want exactly one book folder", names)
	}
}

// Regeneration is destructive by design, so a second run must land on the
// same key rather than accumulating renditions.
func TestPlaceNarrationOverwritesAPreviousRendition(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	folder := filepath.Join("Kōbō Abe", "Woman in the Dunes")
	if err := os.MkdirAll(filepath.Join(root, folder), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dest := filepath.Join(root, folder, "Woman in the Dunes.mp3")
	if err := os.WriteFile(dest, []byte("old-narration"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	handle := &LibraryHandle{Library: model.Library{ID: "lib1", Root: &root}}
	got, err := handle.PlaceNarration(context.Background(), narratedBook(folder), tempMP3(t, "new-narration"))
	if err != nil {
		t.Fatalf("PlaceNarration: %v", err)
	}
	if got.Location != filepath.Join(folder, "Woman in the Dunes.mp3") {
		t.Errorf("Location = %q, want the same key as before", got.Location)
	}
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(b) != "new-narration" {
		t.Errorf("content = %q, want the regenerated audio", b)
	}
}

// Books predating the folder-per-book layout have no folder_path, and
// still have to get a sensible destination.
func TestPlaceNarrationFallsBackToAuthorTitleWithoutAFolderPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	handle := &LibraryHandle{Library: model.Library{ID: "lib1", Root: &root}}

	got, err := handle.PlaceNarration(context.Background(), narratedBook(""), tempMP3(t, "mp3"))
	if err != nil {
		t.Fatalf("PlaceNarration: %v", err)
	}
	want := filepath.Join("Kōbō Abe", "Woman in the Dunes", "Woman in the Dunes.mp3")
	if got.Location != want {
		t.Errorf("Location = %q, want %q", got.Location, want)
	}
}

func TestPlaceNarrationUploadsToTheBooksKeyOnABackend(t *testing.T) {
	t.Parallel()

	backendID := "backend-1"
	store := &putRecordingStorage{}
	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", BackendID: &backendID},
		Storage: store,
	}

	src := tempMP3(t, "mp3-bytes")
	got, err := handle.PlaceNarration(context.Background(), narratedBook("Kōbō Abe/Woman in the Dunes"), src)
	if err != nil {
		t.Fatalf("PlaceNarration: %v", err)
	}

	want := "Kōbō Abe/Woman in the Dunes/Woman in the Dunes.mp3"
	if got.Location != want {
		t.Errorf("Location = %q, want %q", got.Location, want)
	}
	if string(store.puts[want]) != "mp3-bytes" {
		t.Errorf("uploaded %q to %q, want the audio at the book's key", store.puts[want], want)
	}
	// The local temp file is redundant once the bytes are durable.
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("the local temp file survived the upload")
	}
}
