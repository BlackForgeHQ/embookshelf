// SPDX-License-Identifier: AGPL-3.0-or-later

package task_test

import (
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/task"
)

// fakeStore tracks Delete calls and lets tests inject errors per key.
type fakeStore struct {
	mu      sync.Mutex
	deleted []string
	errOn   map[string]error
}

func (f *fakeStore) Capabilities() storage.Capability { return 0 }
func (f *fakeStore) List(context.Context, string) (storage.Iterator, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeStore) Head(context.Context, string) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, errors.New("not implemented")
}
func (f *fakeStore) Get(context.Context, string, ...storage.GetOption) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeStore) Put(context.Context, string, io.Reader, ...storage.PutOption) (storage.PutResult, error) {
	return storage.PutResult{}, errors.New("not implemented")
}
func (f *fakeStore) Delete(_ context.Context, key string, _ ...storage.DeleteOption) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.errOn[key]; ok {
		return err
	}
	f.deleted = append(f.deleted, key)
	return nil
}
func (f *fakeStore) Copy(context.Context, string, string) (storage.CopyResult, error) {
	return storage.CopyResult{}, errors.New("not implemented")
}
func (f *fakeStore) MovePrefix(context.Context, string, string) (storage.MoveResult, error) {
	return storage.MoveResult{}, errors.New("not implemented")
}
func (f *fakeStore) Open(context.Context, string) (storage.Source, error) {
	return nil, errors.New("not implemented")
}

// narrationKey is where a book's generated audio lives. Deterministic, so
// a regeneration lands on exactly this key again (ADR-0025 §4) — which is
// the whole hazard the two tests below are about.
const narrationKey = "An Author/A Book/A Book.mp3"

// sweepFixture is a library, a fake backend, and the deps the sweeper
// takes, over a real database.
func sweepFixture(t *testing.T) (context.Context, model.Library, *repo.FileRepo, *repo.PendingOrphanRepo, *fakeStore, task.OrphanedKeysDeps) {
	t.Helper()
	d := repotest.New(t)
	libRepo := repo.NewLibraryRepo(d)
	files := repo.NewFileRepo(d)
	orphans := repo.NewPendingOrphanRepo(d)
	ctx := context.Background()

	lib, err := libRepo.CreateLibrary(ctx, "Narrated", "narrated", "/tmp/narrated", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	store := &fakeStore{}
	return ctx, lib, files, orphans, store, task.OrphanedKeysDeps{
		Orphans:  orphans,
		Libs:     libRepo,
		Resolver: storage.ConstantResolver{S: store},
	}
}

// writeNarration records the files row finalize writes for placed audio.
func writeNarration(t *testing.T, ctx context.Context, files *repo.FileRepo, libID, key string) model.File {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	f, err := files.Insert(ctx, model.File{
		LibraryID:   libID,
		Location:    key,
		Size:        512 << 20,
		Mtime:       now,
		Format:      "MP3",
		LastScanned: now,
	})
	if err != nil {
		t.Fatalf("Insert narration file row: %v", err)
	}
	return f
}

// Deleting a narration on an object-store library queues its key with
// ADR-0005's hour of grace rather than removing it inline (#267). The key
// is deterministic, so a regeneration started straight afterwards writes
// new audio to the very same key — and the queued row is still sitting
// there, due in an hour.
//
// The grace window is a promise to presigned URLs already in a browser's
// hands: the bytes behind the key they name stay readable until the URL
// has expired. A rewritten key keeps that promise on its own — those URLs
// now serve the new audio. What the window never promised is that the key
// would be deleted regardless of what is at it by then. So the sweeper
// asks the one question that distinguishes abandoned from rewritten: does
// a live files row still point here?
func TestRunOrphanedKeysOnce_KeepsAKeyARegenerationRewrote(t *testing.T) {
	ctx, lib, files, orphans, store, deps := sweepFixture(t)

	// The first narration, then the delete: its row goes and its key is
	// queued with the hour of grace DeleteBookBytes gives it.
	first := writeNarration(t, ctx, files, lib.ID, narrationKey)
	if err := files.Delete(ctx, first.ID); err != nil {
		t.Fatalf("delete narration row: %v", err)
	}
	deletedAt := time.Now().UTC()
	if err := orphans.Insert(ctx, []repo.PendingOrphanInsert{{
		LibraryID:  lib.ID,
		Key:        narrationKey,
		EligibleAt: deletedAt.Add(time.Hour),
		Reason:     repo.ReasonOrphanBookDelete,
	}}); err != nil {
		t.Fatalf("enqueue orphan: %v", err)
	}

	// The regeneration, finishing inside the window: new bytes at the same
	// key, and a files row naming it again.
	writeNarration(t, ctx, files, lib.ID, narrationKey)

	// An hour later the sweeper wakes up.
	if _, err := task.RunOrphanedKeysOnce(ctx, deps, deletedAt.Add(2*time.Hour)); err != nil {
		t.Fatalf("RunOrphanedKeysOnce: %v", err)
	}

	if slices.Contains(store.deleted, narrationKey) {
		t.Errorf("swept %q, which a live files row points at — the regenerated audio is gone and the row names nothing",
			narrationKey)
	}
	exists, err := files.ExistsByLocation(ctx, lib.ID, narrationKey)
	if err != nil {
		t.Fatalf("ExistsByLocation: %v", err)
	}
	if !exists {
		t.Fatalf("the regenerated narration's row vanished; the test no longer says what it means to")
	}

	// The queue entry itself goes: it recorded an abandonment the rewrite
	// undid. Left behind it would be re-judged on every later pass, and a
	// pass that catches the row briefly absent would delete live bytes with
	// no grace at all.
	left, err := orphans.SelectDue(ctx, deletedAt.Add(3*time.Hour), 10)
	if err != nil {
		t.Fatalf("SelectDue: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("left %d row(s) queued for a key nothing orphaned any more", len(left))
	}
}

// The other half of the same judgement, and the one that keeps the
// sweeper worth having: a key nothing points at still goes when its
// grace runs out. Both kinds are in one pass, so the test can only pass
// by discriminating between them rather than by declining to sweep.
//
// It then runs the case where the regeneration finishes *after* the
// window instead of inside it: the sweep has already taken the bytes and
// the row, so the later write has nothing queued against it and survives
// the next pass untouched.
func TestRunOrphanedKeysOnce_StillSweepsAKeyNothingPointsAt(t *testing.T) {
	ctx, lib, files, orphans, store, deps := sweepFixture(t)

	const abandoned = "An Author/Old Title/Old Title.mp3"
	deletedAt := time.Now().UTC()
	writeNarration(t, ctx, files, lib.ID, narrationKey)
	if err := orphans.Insert(ctx, []repo.PendingOrphanInsert{
		{LibraryID: lib.ID, Key: narrationKey, EligibleAt: deletedAt.Add(time.Hour), Reason: repo.ReasonOrphanBookDelete},
		{LibraryID: lib.ID, Key: abandoned, EligibleAt: deletedAt.Add(time.Hour), Reason: repo.ReasonOrphanRename},
	}); err != nil {
		t.Fatalf("enqueue orphans: %v", err)
	}

	n, err := task.RunOrphanedKeysOnce(ctx, deps, deletedAt.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("RunOrphanedKeysOnce: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted=%d want 1 — the abandoned key, and only it", n)
	}
	if !slices.Contains(store.deleted, abandoned) {
		t.Errorf("kept %q, which no files row names; the sweeper stopped collecting", abandoned)
	}
	if slices.Contains(store.deleted, narrationKey) {
		t.Errorf("swept %q, which a live files row names", narrationKey)
	}

	// A regeneration landing after the window: the queue is empty, so the
	// next pass has nothing to act on.
	writeNarration(t, ctx, files, lib.ID, abandoned)
	store.deleted = nil
	if _, err := task.RunOrphanedKeysOnce(ctx, deps, deletedAt.Add(3*time.Hour)); err != nil {
		t.Fatalf("second RunOrphanedKeysOnce: %v", err)
	}
	if len(store.deleted) != 0 {
		t.Errorf("swept %v on a pass with an empty queue", store.deleted)
	}
}

// TestRunOrphanedKeysOnce_DrainsDueRows verifies the sweeper only
// touches rows whose eligible_at has passed and removes them after a
// successful Delete.
func TestRunOrphanedKeysOnce_DrainsDueRows(t *testing.T) {
	d := repotest.New(t)
	libRepo := repo.NewLibraryRepo(d)
	orphans := repo.NewPendingOrphanRepo(d)
	ctx := context.Background()

	lib, err := libRepo.CreateLibrary(ctx, "Sweep", "sweep", "/tmp/sweep", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	now := time.Now().UTC()
	bookID := "bbbbbbbb-0001-4001-8001-000000000001"
	rows := []repo.PendingOrphanInsert{
		{LibraryID: lib.ID, Key: "Old/file.epub", EligibleAt: now.Add(-time.Hour), Reason: repo.ReasonOrphanRename, BookID: &bookID},
		{LibraryID: lib.ID, Key: "Old/cover.jpg", EligibleAt: now.Add(-time.Hour), Reason: repo.ReasonOrphanRename, BookID: &bookID},
		{LibraryID: lib.ID, Key: "Future/key.epub", EligibleAt: now.Add(time.Hour), Reason: repo.ReasonOrphanRename, BookID: &bookID},
	}
	if err := orphans.Insert(ctx, rows); err != nil {
		t.Fatalf("Insert orphans: %v", err)
	}

	store := &fakeStore{}
	deps := task.OrphanedKeysDeps{
		Orphans:  orphans,
		Libs:     libRepo,
		Resolver: storage.ConstantResolver{S: store},
	}

	n, err := task.RunOrphanedKeysOnce(ctx, deps, now)
	if err != nil {
		t.Fatalf("RunOrphanedKeysOnce: %v", err)
	}
	if n != 2 {
		t.Errorf("deleted=%d want 2", n)
	}
	if len(store.deleted) != 2 {
		t.Errorf("Delete calls=%d want 2", len(store.deleted))
	}

	// Future row still present.
	remaining, err := orphans.SelectDue(ctx, now.Add(2*time.Hour), 10)
	if err != nil {
		t.Fatalf("SelectDue: %v", err)
	}
	if len(remaining) != 1 || remaining[0].Key != "Future/key.epub" {
		t.Errorf("remaining=%+v", remaining)
	}
}

// TestRunOrphanedKeysOnce_NotFoundIsSuccess verifies that a missing
// key is treated as a successful delete (the desired state — gone).
func TestRunOrphanedKeysOnce_NotFoundIsSuccess(t *testing.T) {
	d := repotest.New(t)
	libRepo := repo.NewLibraryRepo(d)
	orphans := repo.NewPendingOrphanRepo(d)
	ctx := context.Background()
	lib, _ := libRepo.CreateLibrary(ctx, "x", "x", "/tmp/x", nil)
	now := time.Now().UTC()
	bookID := "bbbbbbbb-0001-4001-8001-000000000001"
	if err := orphans.Insert(ctx, []repo.PendingOrphanInsert{
		{LibraryID: lib.ID, Key: "missing.epub", EligibleAt: now.Add(-time.Minute), Reason: repo.ReasonOrphanRename, BookID: &bookID},
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	store := &fakeStore{errOn: map[string]error{
		"missing.epub": errors.Join(storage.ErrNotFound, errors.New("nope")),
	}}
	deps := task.OrphanedKeysDeps{
		Orphans:  orphans,
		Libs:     libRepo,
		Resolver: storage.ConstantResolver{S: store},
	}
	if _, err := task.RunOrphanedKeysOnce(ctx, deps, now); err != nil {
		t.Fatalf("Run: %v", err)
	}
	left, _ := orphans.SelectDue(ctx, now.Add(time.Hour), 10)
	if len(left) != 0 {
		t.Errorf("not-found should still dequeue; left=%d", len(left))
	}
}
