// SPDX-License-Identifier: AGPL-3.0-or-later

package task_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/task"
)

// stagedRun creates a running Audiobook run with n pending segments and a
// staging directory holding one file per segment, so a sweep has
// something real to reclaim.
func stagedRun(t *testing.T, d *db.DB, staging task.Staging, segments int) (string, *repo.BookAudiobookRepo) {
	t.Helper()
	ctx := context.Background()

	lib, err := repo.NewLibraryRepo(d).CreateLibrary(ctx, "Narration", "narration", "/tmp/narration", nil)
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	b, err := repo.NewBookRepo(d).Create(ctx, model.Book{
		LibraryID: lib.ID, Title: "Dune", Author: "Frank Herbert", Format: "EPUB", Path: "dune.epub",
	})
	if err != nil {
		t.Fatalf("create book: %v", err)
	}

	audiobooks := repo.NewBookAudiobookRepo(d)
	plan := make([]model.AudiobookSegment, 0, segments)
	for i := range segments {
		plan = append(plan, model.AudiobookSegment{
			BookID: b.ID, Seq: i, ChapterIndex: i, ChapterTitle: "Chapter", State: model.SegmentPending,
		})
	}
	if err := audiobooks.Start(ctx, model.Audiobook{
		BookID: b.ID, Engine: "openai", Voice: "alloy",
	}, plan); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if _, err := audiobooks.Transition(ctx, b.ID, model.Transition{
		To: model.AudiobookRunning, From: []model.AudiobookState{model.AudiobookPending, model.AudiobookRunning},
	}); err != nil {
		t.Fatalf("set running: %v", err)
	}

	dir := stagingDir(t, staging, b.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create staging: %v", err)
	}
	for i := range segments {
		if err := os.WriteFile(filepath.Join(dir, "seg.mp3"), []byte{byte(i)}, 0o600); err != nil {
			t.Fatalf("stage segment %d: %v", i, err)
		}
	}
	return b.ID, audiobooks
}

func ageRun(t *testing.T, d *db.DB, bookID string, days int) {
	t.Helper()
	if _, err := d.SQL.ExecContext(context.Background(),
		`UPDATE book_audiobooks SET updated_at = now() - make_interval(days => $2) WHERE book_id = $1`,
		bookID, days); err != nil {
		t.Fatalf("age run: %v", err)
	}
}

func stagingExists(t *testing.T, staging task.Staging, bookID string) bool {
	t.Helper()
	_, err := os.Stat(stagingDir(t, staging, bookID))
	return err == nil
}

// tempRoot is a configured data root over a temp dir.
func tempRoot(t *testing.T) config.DataRoot {
	t.Helper()
	root, err := config.NewDataRoot(t.TempDir())
	if err != nil {
		t.Fatalf("NewDataRoot: %v", err)
	}
	return root
}

// tempStaging is a staging area over a temp dir, already configured, so
// the unset case does not have to be threaded through every caller — it
// has its own test.
func tempStaging(t *testing.T) task.Staging {
	t.Helper()
	return task.NewStaging(tempRoot(t))
}

// stagingDir is Staging.Dir for an area the test has already configured.
func stagingDir(t *testing.T, staging task.Staging, bookID string) string {
	t.Helper()
	dir, err := staging.Dir(bookID)
	if err != nil {
		t.Fatalf("Staging.Dir: %v", err)
	}
	return dir
}

// A run abandoned at running — every segment written, no finalize job,
// nobody ever coming back — used to match nothing the sweeper looked for,
// so its staged MP3s sat on disk forever (#157).
func TestStagingSweepReclaimsAStrandedRun(t *testing.T) {
	d := repotest.New(t)
	staging := tempStaging(t)
	bookID, audiobooks := stagedRun(t, d, staging, 2)
	ctx := context.Background()

	// One segment landed, one never did: nothing here is one finalize away.
	if _, err := audiobooks.RecordSegment(ctx, bookID, 0, model.SegmentResult{
		State: model.SegmentDone, StagedPath: "seg-0.mp3", DurationMS: 1000,
	}); err != nil {
		t.Fatalf("RecordSegment: %v", err)
	}
	ageRun(t, d, bookID, 14)

	n, err := staging.Sweep(ctx, audiobooks)
	if err != nil {
		t.Fatalf("Staging.Sweep: %v", err)
	}
	if n != 1 {
		t.Fatalf("swept %d runs, want 1", n)
	}
	if stagingExists(t, staging, bookID) {
		t.Error("the stranded run's staging survived the sweep")
	}
}

// ADR-0028 §6: failure keeps the paid-for work. A run that failed
// yesterday is exactly the run a user is about to retry, and Retry
// re-enqueues only the segments that never finished — so the ones that
// did have to still be on disk.
func TestStagingSweepRetainsAFreshlyFailedRun(t *testing.T) {
	d := repotest.New(t)
	staging := tempStaging(t)
	bookID, audiobooks := stagedRun(t, d, staging, 2)
	ctx := context.Background()

	if _, err := audiobooks.RecordSegment(ctx, bookID, 0, model.SegmentResult{
		State: model.SegmentDone, StagedPath: "seg-0.mp3", DurationMS: 1000,
	}); err != nil {
		t.Fatalf("RecordSegment 0: %v", err)
	}
	out, err := audiobooks.RecordSegment(ctx, bookID, 1, model.SegmentResult{
		State: model.SegmentFailed, Error: "engine returned 500",
	})
	if err != nil {
		t.Fatalf("RecordSegment 1: %v", err)
	}
	if out.Next != model.AudiobookNextFail {
		t.Fatalf("next = %q, want the run failed by the same write", out.Next)
	}
	ageRun(t, d, bookID, 1)

	n, err := staging.Sweep(ctx, audiobooks)
	if err != nil {
		t.Fatalf("Staging.Sweep: %v", err)
	}
	if n != 0 {
		t.Fatalf("swept %d runs, want 0 inside the retry window", n)
	}
	if !stagingExists(t, staging, bookID) {
		t.Error("a failed run's staging was reclaimed — Retry would have to buy it again")
	}
}
