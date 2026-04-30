package task_test

import (
	"context"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/task"
)

// newPurgeTestRepo creates a FileRepo wired to a fresh SQLite DB (migrated).
func newPurgeTestRepo(t *testing.T) (*repo.FileRepo, *repo.LibraryRepo) {
	t.Helper()
	d := repotest.NewWithDialect(t, "sqlite")
	return repo.NewFileRepo(d), repo.NewLibraryRepo(d)
}

// insertMissingFile inserts a file row with missing_since set to `when`.
func insertMissingFile(t *testing.T, fr *repo.FileRepo, libID string, when time.Time) model.File {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	f, err := fr.Insert(ctx, model.File{
		LibraryID:   libID,
		Location:    "book-" + when.Format("20060102150405.999999999") + ".epub",
		Size:        100,
		Mtime:       now,
		Format:      "EPUB",
		LastScanned: now,
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := fr.MarkMissing(ctx, f.ID, when); err != nil {
		t.Fatalf("MarkMissing: %v", err)
	}
	return f
}

// TestRunMissingPurge_nilFiles ensures nil FileRepo returns (0, nil).
func TestRunMissingPurge_nilFiles(t *testing.T) {
	n, err := task.RunMissingPurge(context.Background(), nil)
	if err != nil {
		t.Fatalf("nil files: got err %v, want nil", err)
	}
	if n != 0 {
		t.Fatalf("nil files: got n=%d, want 0", n)
	}
}

// TestRunMissingPurge_noMissingRows returns (0, nil) when no rows are missing.
func TestRunMissingPurge_noMissingRows(t *testing.T) {
	fr, lr := newPurgeTestRepo(t)
	ctx := context.Background()
	lib, err := lr.CreateLibrary(ctx, "Purge Test", "purge-test", "/tmp/purge", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	// Insert a normal (non-missing) file.
	now := time.Now().UTC().Truncate(time.Millisecond)
	_, err = fr.Insert(ctx, model.File{
		LibraryID:   lib.ID,
		Location:    "present.epub",
		Size:        100,
		Mtime:       now,
		Format:      "EPUB",
		LastScanned: now,
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	n, err := task.RunMissingPurge(ctx, fr)
	if err != nil {
		t.Fatalf("got err %v, want nil", err)
	}
	if n != 0 {
		t.Fatalf("got n=%d, want 0 (no missing rows)", n)
	}
}

// TestRunMissingPurge_withinTTL verifies a row missing < 24h is not deleted.
func TestRunMissingPurge_withinTTL(t *testing.T) {
	fr, lr := newPurgeTestRepo(t)
	ctx := context.Background()
	lib, err := lr.CreateLibrary(ctx, "Purge Test TTL", "purge-ttl", "/tmp/purge-ttl", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	// Mark missing 1 hour ago — well within the 24h TTL.
	insertMissingFile(t, fr, lib.ID, time.Now().Add(-1*time.Hour))

	n, err := task.RunMissingPurge(ctx, fr)
	if err != nil {
		t.Fatalf("got err %v, want nil", err)
	}
	if n != 0 {
		t.Fatalf("got n=%d, want 0 (row within TTL should be kept)", n)
	}

	// Row should still exist.
	rows, err := fr.ListByLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ListByLibrary: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (row kept)", len(rows))
	}
}

// TestRunMissingPurge_beyondTTL verifies a row missing > 24h is deleted.
func TestRunMissingPurge_beyondTTL(t *testing.T) {
	fr, lr := newPurgeTestRepo(t)
	ctx := context.Background()
	lib, err := lr.CreateLibrary(ctx, "Purge Test Old", "purge-old", "/tmp/purge-old", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	// Mark missing 25 hours ago — past the 24h TTL.
	insertMissingFile(t, fr, lib.ID, time.Now().Add(-25*time.Hour))

	n, err := task.RunMissingPurge(ctx, fr)
	if err != nil {
		t.Fatalf("got err %v, want nil", err)
	}
	if n != 1 {
		t.Fatalf("got n=%d, want 1 (row past TTL should be deleted)", n)
	}

	// Row should be gone.
	rows, err := fr.ListByLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ListByLibrary: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("got %d rows, want 0 (row deleted)", len(rows))
	}
}

// TestRunMissingPurge_mixed verifies only rows past TTL are deleted while
// rows within TTL are preserved.
func TestRunMissingPurge_mixed(t *testing.T) {
	fr, lr := newPurgeTestRepo(t)
	ctx := context.Background()
	lib, err := lr.CreateLibrary(ctx, "Purge Test Mix", "purge-mix", "/tmp/purge-mix", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	// One row within TTL, one beyond.
	insertMissingFile(t, fr, lib.ID, time.Now().Add(-1*time.Hour))  // keep
	insertMissingFile(t, fr, lib.ID, time.Now().Add(-25*time.Hour)) // delete

	n, err := task.RunMissingPurge(ctx, fr)
	if err != nil {
		t.Fatalf("got err %v, want nil", err)
	}
	if n != 1 {
		t.Fatalf("got n=%d, want 1 (only the old row deleted)", n)
	}

	rows, err := fr.ListByLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ListByLibrary: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (new-missing row kept)", len(rows))
	}
}

// TestLoopMissingPurge_nilFiles returns immediately when files is nil.
func TestLoopMissingPurge_nilFiles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		task.LoopMissingPurge(ctx, nil, 50*time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
		// Expected: nil files → immediate return.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("LoopMissingPurge with nil files did not return promptly")
	}
}

// TestLoopMissingPurge_cancellation verifies the loop exits cleanly when
// ctx is cancelled, without leaking goroutines.
func TestLoopMissingPurge_cancellation(t *testing.T) {
	fr, lr := newPurgeTestRepo(t)
	ctx := context.Background()
	lib, err := lr.CreateLibrary(ctx, "Loop Test", "loop-test", "/tmp/loop-test", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	// Insert a row past TTL so the loop has work to do on its ticks.
	insertMissingFile(t, fr, lib.ID, time.Now().Add(-25*time.Hour))

	loopCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		task.LoopMissingPurge(loopCtx, fr, 50*time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
		// Loop exited after cancellation — clean.
	case <-time.After(1 * time.Second):
		t.Fatal("LoopMissingPurge did not exit after context cancellation")
	}
}
