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
	objectStore bool
	fakeStorage
	deleted  []string
	failKey  string
	failWith error
	// rowGone is consulted at the moment each Delete runs, so an ordering
	// assertion is about when the byte removal happened rather than about
	// a call log the doubles agreed on among themselves. Optional: the
	// tests that only care *which* keys went leave it nil.
	rowGone    func() bool
	sawRowGone []bool
}

// What IsObjectStore reads. This double serves both shapes in this
// file, so the capability is a field rather than a constant — a backend
// id no longer decides it (#202).
func (s *deleteRecordingStorage) Capabilities() storage.Capability {
	if s.objectStore {
		return storage.CapObjectStore
	}
	return 0
}

func (d *deleteRecordingStorage) Delete(_ context.Context, key string, _ ...storage.DeleteOption) error {
	if d.rowGone != nil {
		d.sawRowGone = append(d.sawRowGone, d.rowGone())
	}
	if d.failKey != "" && key == d.failKey {
		return d.failWith
	}
	d.deleted = append(d.deleted, key)
	return nil
}

type recordingOrphans struct {
	rows []repo.PendingOrphanInsert
	err  error
	// rowGone mirrors the storage double's: the deferred arm has the same
	// ordering obligation as the inline one.
	rowGone    func() bool
	sawRowGone []bool
}

func (r *recordingOrphans) Insert(_ context.Context, rows []repo.PendingOrphanInsert) error {
	if r.rowGone != nil {
		r.sawRowGone = append(r.sawRowGone, r.rowGone())
	}
	if r.err != nil {
		return r.err
	}
	r.rows = append(r.rows, rows...)
	return nil
}

// cascadingFiles is a files lister whose rows vanish when the books row
// does, which is what the FK cascade does in Postgres. Nothing else in
// this file needs it: it exists so the collect-then-delete sequence can
// be exercised without a database and still be wrong in the one way that
// matters — a snapshot taken after the row delete comes back empty.
type cascadingFiles struct {
	rows []model.File
	gone bool
}

func (f *cascadingFiles) ListByBook(context.Context, string) ([]model.File, error) {
	if f.gone {
		return nil, nil
	}
	return f.rows, nil
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
// DeleteBookAndBytes — the operation that owns the ordering
// ---------------------------------------------------------------------------

// The sequence is the operation's, not the caller's. A caller that could
// still get it wrong is a caller that will: reading the locations after
// the row delete yields nothing (the files rows cascaded) and removing
// the bytes before it strands a live catalog entry when the row delete
// then fails.
//
// The double's rows disappear the moment the row delete runs, so this
// only passes if the snapshot was taken first; the storage double records
// the row's state at each Delete, so it only passes if the bytes went
// after.
func TestDeleteBookAndBytesSnapshotsBeforeTheRowGoesAndRemovesBytesAfter(t *testing.T) {
	t.Parallel()

	files := &cascadingFiles{rows: []model.File{
		{Location: "Author/Title/book.epub", Format: "EPUB"},
		{Location: "Author/Title/book.mp3", Format: "MP3"},
	}}
	store := &deleteRecordingStorage{rowGone: func() bool { return files.gone }}
	handle := &LibraryHandle{Library: model.Library{ID: "lib1"}, Storage: store, files: files}

	rowDeletes := 0
	bytesErr, err := handle.DeleteBookAndBytes(context.Background(), "b1",
		func(context.Context) error {
			rowDeletes++
			files.gone = true // the FK cascade
			return nil
		})
	if err != nil {
		t.Fatalf("DeleteBookAndBytes: %v", err)
	}
	if bytesErr != nil {
		t.Fatalf("byte cleanup reported %v", bytesErr)
	}
	if rowDeletes != 1 {
		t.Fatalf("row delete ran %d times, want exactly 1", rowDeletes)
	}

	sort.Strings(store.deleted)
	want := []string{"Author/Title/book.epub", "Author/Title/book.mp3"}
	if len(store.deleted) != len(want) {
		t.Fatalf("deleted %v, want %v — the locations were snapshotted after the cascade", store.deleted, want)
	}
	for i := range want {
		if store.deleted[i] != want[i] {
			t.Errorf("deleted[%d] = %q, want %q", i, store.deleted[i], want[i])
		}
	}
	if len(store.sawRowGone) != 2 {
		t.Fatalf("storage saw %d deletes, want 2", len(store.sawRowGone))
	}
	for i, gone := range store.sawRowGone {
		if !gone {
			t.Errorf("delete %d removed bytes while the books row still existed", i)
		}
	}
}

// The deferred arm has the same obligation: an object-store library
// queues its keys for the sweeper (ADR-0005), and it must queue the keys
// the snapshot found, after the row is gone.
func TestDeleteBookAndBytesDefersToTheSweeperAfterTheRowGoes(t *testing.T) {
	t.Parallel()

	files := &cascadingFiles{rows: []model.File{
		{Location: "Author/Title/book.epub", Format: "EPUB"},
		{Location: "Author/Title/book.mp3", Format: "MP3"},
	}}
	store := &deleteRecordingStorage{objectStore: true, rowGone: func() bool { return files.gone }}
	orphans := &recordingOrphans{rowGone: func() bool { return files.gone }}
	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1"},
		Storage: store,
		files:   files,
		orphans: orphans,
	}

	bytesErr, err := handle.DeleteBookAndBytes(context.Background(), "b1",
		func(context.Context) error { files.gone = true; return nil })
	if err != nil || bytesErr != nil {
		t.Fatalf("DeleteBookAndBytes: err=%v bytesErr=%v", err, bytesErr)
	}

	if len(store.deleted) != 0 {
		t.Errorf("removed %v inline; the grace window exists so an in-flight presigned download finishes", store.deleted)
	}
	if len(orphans.rows) != 2 {
		t.Fatalf("enqueued %d orphans, want 2 — the snapshot was taken after the cascade", len(orphans.rows))
	}
	if len(orphans.sawRowGone) != 1 || !orphans.sawRowGone[0] {
		t.Errorf("orphans were enqueued while the books row still existed (%v)", orphans.sawRowGone)
	}
}

// The row is the authoritative step. When it refuses, the bytes must stay
// put: removing them would strip a book that is still in the catalog.
func TestDeleteBookAndBytesTouchesNoBytesWhenTheRowDeleteFails(t *testing.T) {
	t.Parallel()

	files := &cascadingFiles{rows: []model.File{{Location: "Author/Title/book.epub", Format: "EPUB"}}}
	store := &deleteRecordingStorage{}
	handle := &LibraryHandle{Library: model.Library{ID: "lib1"}, Storage: store, files: files}

	refused := errors.New("book not found")
	bytesErr, err := handle.DeleteBookAndBytes(context.Background(), "b1",
		func(context.Context) error { return refused })
	if !errors.Is(err, refused) {
		t.Fatalf("err = %v, want the row delete's own error", err)
	}
	if bytesErr != nil {
		t.Errorf("bytesErr = %v, want nil — no cleanup was attempted", bytesErr)
	}
	if len(store.deleted) != 0 {
		t.Errorf("removed %v for a book that is still in the catalog", store.deleted)
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
	store := &deleteRecordingStorage{objectStore: true}
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
