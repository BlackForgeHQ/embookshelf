// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/storage"
)

// deleteRecordingStorage records Delete calls and can fail a chosen key,
// so a test can prove one bad object does not abandon the rest.
type deleteRecordingStorage struct {
	fakeStorage
	deleted  []string
	failKey  string
	failWith error
}

func (d *deleteRecordingStorage) Delete(_ context.Context, key string, _ ...storage.DeleteOption) error {
	if d.failKey != "" && key == d.failKey {
		return d.failWith
	}
	d.deleted = append(d.deleted, key)
	return nil
}

type recordingOrphans struct {
	rows []repo.PendingOrphanInsert
	err  error
}

func (r *recordingOrphans) Insert(_ context.Context, rows []repo.PendingOrphanInsert) error {
	if r.err != nil {
		return r.err
	}
	r.rows = append(r.rows, rows...)
	return nil
}

// A book with an EPUB and a generated narration — the shape that makes
// the old single-path unlink insufficient.
func twoFileBook() (model.Book, *fakeFiles) {
	return model.Book{ID: "b1", Format: "EPUB"}, &fakeFiles{byBook: map[string][]model.File{
		"b1": {
			{Location: "Author/Title/book.epub", Format: "EPUB"},
			{Location: "Author/Title/book.mp3", Format: "MP3"},
		},
	}}
}

// ---------------------------------------------------------------------------
// BookFileLocations
// ---------------------------------------------------------------------------

func TestBookFileLocationsReturnsEveryKey(t *testing.T) {
	t.Parallel()

	book, files := twoFileBook()
	handle := &LibraryHandle{Library: model.Library{ID: "lib1"}, files: files}

	got, err := handle.BookFileLocations(context.Background(), book.ID)
	if err != nil {
		t.Fatalf("BookFileLocations: %v", err)
	}
	sort.Strings(got)
	want := []string{"Author/Title/book.epub", "Author/Title/book.mp3"}
	if len(got) != len(want) {
		t.Fatalf("locations = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("locations[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// The keys must be readable before the row is deleted; a book whose
// files rows have already cascaded yields nothing, which is exactly why
// the caller has to snapshot first.
func TestBookFileLocationsIsEmptyOnceRowsAreGone(t *testing.T) {
	t.Parallel()

	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1"},
		files:   &fakeFiles{byBook: map[string][]model.File{}},
	}

	got, err := handle.BookFileLocations(context.Background(), "b1")
	if err != nil {
		t.Fatalf("BookFileLocations: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("locations = %v, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// DeleteBookBytes
// ---------------------------------------------------------------------------

// A local library owns its bytes outright, so delete is immediate.
func TestDeleteBookBytesRemovesEveryKeyOnLocalLibrary(t *testing.T) {
	t.Parallel()

	store := &deleteRecordingStorage{}
	handle := &LibraryHandle{Library: model.Library{ID: "lib1"}, Storage: store}

	err := handle.DeleteBookBytes(context.Background(), "b1", []string{
		"Author/Title/book.epub",
		"Author/Title/book.mp3",
	})
	if err != nil {
		t.Fatalf("DeleteBookBytes: %v", err)
	}

	sort.Strings(store.deleted)
	want := []string{"Author/Title/book.epub", "Author/Title/book.mp3"}
	if len(store.deleted) != len(want) {
		t.Fatalf("deleted %v, want %v", store.deleted, want)
	}
	for i := range want {
		if store.deleted[i] != want[i] {
			t.Errorf("deleted[%d] = %q, want %q", i, store.deleted[i], want[i])
		}
	}
}

// On a backend-backed library the keys are queued for the sweeper rather
// than deleted inline, so an in-flight presigned download is not pulled
// out from under a reader (ADR-0005).
func TestDeleteBookBytesEnqueuesOrphansOnBackendBackedLibrary(t *testing.T) {
	t.Parallel()

	backendID := "backend-1"
	store := &deleteRecordingStorage{}
	orphans := &recordingOrphans{}
	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", BackendID: &backendID},
		Storage: store,
		orphans: orphans,
	}

	err := handle.DeleteBookBytes(context.Background(), "b1", []string{
		"Author/Title/book.epub",
		"Author/Title/book.mp3",
	})
	if err != nil {
		t.Fatalf("DeleteBookBytes: %v", err)
	}

	if len(store.deleted) != 0 {
		t.Errorf("backend-backed delete removed %v inline, want deferred", store.deleted)
	}
	if len(orphans.rows) != 2 {
		t.Fatalf("enqueued %d orphans, want 2", len(orphans.rows))
	}
	for _, row := range orphans.rows {
		if row.LibraryID != "lib1" {
			t.Errorf("orphan library = %q, want lib1", row.LibraryID)
		}
		if row.Reason != repo.ReasonOrphanBookDelete {
			t.Errorf("orphan reason = %q, want %q", row.Reason, repo.ReasonOrphanBookDelete)
		}
		if row.BookID == nil || *row.BookID != "b1" {
			t.Errorf("orphan book id = %v, want b1", row.BookID)
		}
	}
}

// One unreachable object must not strand the others — the whole point of
// fixing this path is that a 500 MB narration does not survive its book.
func TestDeleteBookBytesContinuesPastAFailureAndReportsIt(t *testing.T) {
	t.Parallel()

	store := &deleteRecordingStorage{
		failKey:  "Author/Title/book.epub",
		failWith: errors.New("permission denied"),
	}
	handle := &LibraryHandle{Library: model.Library{ID: "lib1"}, Storage: store}

	err := handle.DeleteBookBytes(context.Background(), "b1", []string{
		"Author/Title/book.epub",
		"Author/Title/book.mp3",
	})
	if err == nil {
		t.Fatal("want an error reporting the failed key, got nil")
	}
	if len(store.deleted) != 1 || store.deleted[0] != "Author/Title/book.mp3" {
		t.Errorf("deleted = %v, want the mp3 to have been removed anyway", store.deleted)
	}
}

// An object the backend has already lost is not a failure — the desired
// end state is that it is gone, and it is.
func TestDeleteBookBytesTreatsMissingObjectsAsDone(t *testing.T) {
	t.Parallel()

	store := &deleteRecordingStorage{
		failKey:  "Author/Title/book.epub",
		failWith: storage.ErrNotFound,
	}
	handle := &LibraryHandle{Library: model.Library{ID: "lib1"}, Storage: store}

	if err := handle.DeleteBookBytes(context.Background(), "b1", []string{"Author/Title/book.epub"}); err != nil {
		t.Fatalf("a missing object must not be an error, got %v", err)
	}
}

// No storage configured is a degrade, not a crash: the row still goes.
func TestDeleteBookBytesIsANoOpWithoutStorage(t *testing.T) {
	t.Parallel()

	handle := &LibraryHandle{Library: model.Library{ID: "lib1"}}
	if err := handle.DeleteBookBytes(context.Background(), "b1", []string{"k.epub"}); err != nil {
		t.Fatalf("DeleteBookBytes without storage: %v", err)
	}
}
