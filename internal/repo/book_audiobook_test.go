// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"context"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// audiobookFixture creates a library, a book, and a started run with n
// pending segments — the state a worker picks a job up in.
func audiobookFixture(t *testing.T, d *db.DB, segments int) (string, *repo.BookAudiobookRepo) {
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
			CharStart: i * 100, CharEnd: (i + 1) * 100,
		})
	}
	if err := audiobooks.Start(ctx, model.Audiobook{
		BookID: b.ID, Engine: "openai", Voice: "alloy", Model: "tts-1", TotalChars: segments * 100,
	}, plan); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := audiobooks.SetState(ctx, b.ID, model.AudiobookRunning, ""); err != nil {
		t.Fatalf("set running: %v", err)
	}
	return b.ID, audiobooks
}

func doneResult(seq int) model.SegmentResult {
	return model.SegmentResult{
		State:      model.SegmentDone,
		StagedPath: "/tmp/narration/seg-" + string(rune('0'+seq)) + ".mp3",
		DurationMS: 60_000,
	}
}

// The defect in one place: recording the last segment must itself say
// that finalize is due. Before this, the worker recorded the segment and
// then made a second call to find that out, and a process killed between
// the two left a run every segment of which was done, still marked
// running, with no finalize job and nothing anywhere to notice.
func TestRecordSegmentAsksForFinalizeWhenTheLastSegmentLands(t *testing.T) {
	bookID, audiobooks := audiobookFixture(t, repotest.New(t), 3)
	ctx := context.Background()

	for seq := range 2 {
		out, err := audiobooks.RecordSegment(ctx, bookID, seq, doneResult(seq))
		if err != nil {
			t.Fatalf("RecordSegment %d: %v", seq, err)
		}
		if out.Next != model.AudiobookNextNothing {
			t.Fatalf("segment %d asked for %q, want nothing while the run is unfinished", seq, out.Next)
		}
		if out.Coverage.Done != seq+1 {
			t.Errorf("after segment %d coverage.Done = %d, want %d", seq, out.Coverage.Done, seq+1)
		}
	}

	out, err := audiobooks.RecordSegment(ctx, bookID, 2, doneResult(2))
	if err != nil {
		t.Fatalf("RecordSegment 2: %v", err)
	}
	if out.Next != model.AudiobookNextFinalize {
		t.Fatalf("the last segment asked for %q, want finalize", out.Next)
	}
	if out.Coverage.Done != 3 || out.Coverage.Total != 3 {
		t.Errorf("coverage = %d/%d, want 3/3", out.Coverage.Done, out.Coverage.Total)
	}
}

// The alignment map is written by the same call. A segment result that
// recorded the transition but not the duration would leave finalize
// unable to place the segment in the finished file.
func TestRecordSegmentPersistsTheSegmentItReportedOn(t *testing.T) {
	bookID, audiobooks := audiobookFixture(t, repotest.New(t), 2)
	ctx := context.Background()

	if _, err := audiobooks.RecordSegment(ctx, bookID, 0, model.SegmentResult{
		State: model.SegmentDone, StagedPath: "/staging/seg-0.mp3", DurationMS: 91_500,
	}); err != nil {
		t.Fatalf("RecordSegment: %v", err)
	}

	seg, err := audiobooks.GetSegment(ctx, bookID, 0)
	if err != nil {
		t.Fatalf("GetSegment: %v", err)
	}
	if seg.State != model.SegmentDone {
		t.Errorf("state = %q, want done", seg.State)
	}
	if seg.StagedPath != "/staging/seg-0.mp3" {
		t.Errorf("staged path = %q, want the file the worker wrote", seg.StagedPath)
	}
	if seg.DurationMS != 91_500 {
		t.Errorf("duration = %d, want 91500", seg.DurationMS)
	}
}

// ADR-0028 §6: when the last outstanding segment gives up, the run fails
// with a count of what was lost — and the run is failed by the same write
// that recorded the segment, not by a follow-up call that a crash can
// swallow.
func TestRecordSegmentFailsTheRunWhenTheLastSegmentGivesUp(t *testing.T) {
	bookID, audiobooks := audiobookFixture(t, repotest.New(t), 3)
	ctx := context.Background()

	for seq := range 2 {
		if _, err := audiobooks.RecordSegment(ctx, bookID, seq, doneResult(seq)); err != nil {
			t.Fatalf("RecordSegment %d: %v", seq, err)
		}
	}

	out, err := audiobooks.RecordSegment(ctx, bookID, 2, model.SegmentResult{
		State: model.SegmentFailed, Error: "engine returned 500",
	})
	if err != nil {
		t.Fatalf("RecordSegment 2: %v", err)
	}
	if out.Next != model.AudiobookNextFail {
		t.Fatalf("next = %q, want fail", out.Next)
	}

	run, err := audiobooks.GetByBookID(ctx, bookID)
	if err != nil {
		t.Fatalf("GetByBookID: %v", err)
	}
	if run.State != model.AudiobookFailed {
		t.Fatalf("run state = %q, want failed", run.State)
	}
	if !strings.Contains(run.Error, "1 of 3") {
		t.Errorf("run error = %q, want it to say how much was lost", run.Error)
	}

	// The two segments that succeeded keep their staged paths: Retry
	// re-enqueues only what never finished, so the paid-for audio has to
	// still be addressable (ADR-0028 §6).
	unfinished, err := audiobooks.ListUnfinishedSegments(ctx, bookID)
	if err != nil {
		t.Fatalf("ListUnfinishedSegments: %v", err)
	}
	if len(unfinished) != 1 || unfinished[0].Seq != 2 {
		t.Fatalf("unfinished = %+v, want only segment 2", unfinished)
	}
	for seq := range 2 {
		seg, err := audiobooks.GetSegment(ctx, bookID, seq)
		if err != nil {
			t.Fatalf("GetSegment %d: %v", seq, err)
		}
		if seg.StagedPath == "" {
			t.Errorf("segment %d lost its staged audio when the run failed", seq)
		}
	}
}

// A run already marked failed must not have its failure rewritten every
// time a straggler lands — the message would keep changing under the
// user for a run that is not going anywhere.
func TestRecordSegmentDoesNotRefailAnAlreadyFailedRun(t *testing.T) {
	bookID, audiobooks := audiobookFixture(t, repotest.New(t), 2)
	ctx := context.Background()

	if _, err := audiobooks.RecordSegment(ctx, bookID, 0, model.SegmentResult{
		State: model.SegmentFailed, Error: "first",
	}); err != nil {
		t.Fatalf("RecordSegment 0: %v", err)
	}
	out, err := audiobooks.RecordSegment(ctx, bookID, 1, model.SegmentResult{
		State: model.SegmentFailed, Error: "second",
	})
	if err != nil {
		t.Fatalf("RecordSegment 1: %v", err)
	}
	if out.Next != model.AudiobookNextFail {
		t.Fatalf("next = %q, want fail on the segment that settled the run", out.Next)
	}

	// And once more, as a River retry of the first segment would do.
	out, err = audiobooks.RecordSegment(ctx, bookID, 0, model.SegmentResult{
		State: model.SegmentFailed, Error: "again",
	})
	if err != nil {
		t.Fatalf("RecordSegment 0 again: %v", err)
	}
	if out.Next != model.AudiobookNextNothing {
		t.Errorf("next = %q on an already-failed run, want nothing", out.Next)
	}
}

// A cancel taken while a segment was in flight outranks the segment's own
// result: the user said stop, and finalizing would publish the partial
// they asked not to have (ADR-0028 §6).
func TestRecordSegmentDoesNotFinalizeACanceledRun(t *testing.T) {
	bookID, audiobooks := audiobookFixture(t, repotest.New(t), 1)
	ctx := context.Background()

	if err := audiobooks.SetState(ctx, bookID, model.AudiobookCanceled, ""); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	out, err := audiobooks.RecordSegment(ctx, bookID, 0, doneResult(0))
	if err != nil {
		t.Fatalf("RecordSegment: %v", err)
	}
	if out.Next != model.AudiobookNextNothing {
		t.Fatalf("next = %q on a canceled run, want nothing", out.Next)
	}
}

// ---------------------------------------------------------------------------
// Staging reclaim
// ---------------------------------------------------------------------------

// age moves a run's updated_at back so the sweeper's TTL applies. Written
// with SQL because every repo write stamps now(), which is the point.
func age(t *testing.T, d *db.DB, bookID string, days int) {
	t.Helper()
	if _, err := d.SQL.ExecContext(context.Background(),
		`UPDATE book_audiobooks SET updated_at = now() - make_interval(days => $2) WHERE book_id = $1`,
		bookID, days); err != nil {
		t.Fatalf("age run: %v", err)
	}
}

// The third way #157 made a stranded run unrecoverable: the sweeper
// matched only failed and canceled, so a run stuck at running parked its
// staging forever with nothing that would ever come back for it.
func TestListStaleStagingReclaimsARunStrandedAtRunning(t *testing.T) {
	d := repotest.New(t)
	bookID, audiobooks := audiobookFixture(t, d, 2)
	ctx := context.Background()

	// One segment landed, one never did, and the process that owned the
	// run has been gone for a fortnight.
	if _, err := audiobooks.RecordSegment(ctx, bookID, 0, doneResult(0)); err != nil {
		t.Fatalf("RecordSegment: %v", err)
	}
	age(t, d, bookID, 14)

	ids, err := audiobooks.ListStaleStaging(ctx, 7)
	if err != nil {
		t.Fatalf("ListStaleStaging: %v", err)
	}
	if len(ids) != 1 || ids[0] != bookID {
		t.Fatalf("stale = %v, want the stranded run %s", ids, bookID)
	}
}

// A failed run keeps its staging for the whole retry window, because
// Retry re-enqueues only the missing segments and every other one is
// already paid for (ADR-0028 §6).
func TestListStaleStagingKeepsAFreshFailedRun(t *testing.T) {
	d := repotest.New(t)
	bookID, audiobooks := audiobookFixture(t, d, 2)
	ctx := context.Background()

	if _, err := audiobooks.RecordSegment(ctx, bookID, 0, doneResult(0)); err != nil {
		t.Fatalf("RecordSegment 0: %v", err)
	}
	if _, err := audiobooks.RecordSegment(ctx, bookID, 1, model.SegmentResult{
		State: model.SegmentFailed, Error: "engine returned 500",
	}); err != nil {
		t.Fatalf("RecordSegment 1: %v", err)
	}
	age(t, d, bookID, 1)

	ids, err := audiobooks.ListStaleStaging(ctx, 7)
	if err != nil {
		t.Fatalf("ListStaleStaging: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("stale = %v, want a day-old failed run kept for its retry", ids)
	}
}

// The one case the sweep withholds. A run whose coverage is complete is a
// single finalize away from a finished book, so reclaiming its staging
// would turn something recoverable into audio that has to be bought
// again — and reconcile-on-read will have dispatched that finalize the
// moment anyone opened the page.
func TestListStaleStagingSparesACompleteRunAwaitingFinalize(t *testing.T) {
	d := repotest.New(t)
	bookID, audiobooks := audiobookFixture(t, d, 2)
	ctx := context.Background()

	for seq := range 2 {
		if _, err := audiobooks.RecordSegment(ctx, bookID, seq, doneResult(seq)); err != nil {
			t.Fatalf("RecordSegment %d: %v", seq, err)
		}
	}
	age(t, d, bookID, 30)

	ids, err := audiobooks.ListStaleStaging(ctx, 7)
	if err != nil {
		t.Fatalf("ListStaleStaging: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("stale = %v, want a complete run's staging spared for the finalize it still needs", ids)
	}
}

// A published run's staging is redundant — the audio is a files row in
// the book's own folder — so it is reclaimed like any other.
func TestListStaleStagingReclaimsAPublishedRun(t *testing.T) {
	d := repotest.New(t)
	bookID, audiobooks := audiobookFixture(t, d, 1)
	ctx := context.Background()

	if _, err := audiobooks.RecordSegment(ctx, bookID, 0, doneResult(0)); err != nil {
		t.Fatalf("RecordSegment: %v", err)
	}
	if err := audiobooks.SetState(ctx, bookID, model.AudiobookReady, ""); err != nil {
		t.Fatalf("set ready: %v", err)
	}
	age(t, d, bookID, 14)

	ids, err := audiobooks.ListStaleStaging(ctx, 7)
	if err != nil {
		t.Fatalf("ListStaleStaging: %v", err)
	}
	if len(ids) != 1 || ids[0] != bookID {
		t.Fatalf("stale = %v, want the published run's leftover staging reclaimed", ids)
	}
}
