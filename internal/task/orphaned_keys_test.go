// SPDX-License-Identifier: AGPL-3.0-or-later

package task_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

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
