// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/fileproc"
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
	if _, err := audiobooks.Transition(ctx, b.ID, model.Transition{
		To: model.AudiobookRunning, From: []model.AudiobookState{model.AudiobookPending, model.AudiobookRunning},
	}); err != nil {
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

// A job from a run that has since been replanned carries a seq the
// current plan does not have. Its write matches no row, so there is no
// segment whose landing could have moved the run — and a verdict derived
// from coverage this result never entered would be a transition for a
// plan it never wrote to. The call is refused with the same sentinel a
// missing run row gets (#220).
func TestRecordSegmentRefusesASeqThatIsNotInThePlan(t *testing.T) {
	bookID, audiobooks := audiobookFixture(t, repotest.New(t), 2)
	ctx := context.Background()

	if _, err := audiobooks.RecordSegment(ctx, bookID, 9, doneResult(1)); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound for a seq the plan does not have", err)
	}

	run, err := audiobooks.GetByBookID(ctx, bookID)
	if err != nil {
		t.Fatalf("GetByBookID: %v", err)
	}
	if run.State != model.AudiobookRunning {
		t.Errorf("run state = %q, want running — a refused write leaves the run where it was", run.State)
	}

	cov, err := audiobooks.Coverage(ctx, bookID)
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	if cov != (model.AudiobookCoverage{Total: 2}) {
		t.Errorf("coverage = %+v, want 0 of 2 — nothing was written", cov)
	}
}

// ADR-0028 §6: when the last outstanding segment gives up, the write
// reports that the run must fail — and reports it from under the run
// row's lock, so exactly one landing reaches that verdict.
//
// Carrying it out is AudiobookService's, which is what made "mark the run
// failed" one writer instead of four (#190). What this asserts is the
// verdict and what survives it.
func TestRecordSegmentReportsFailWhenTheLastSegmentGivesUp(t *testing.T) {
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

	if !strings.Contains(out.Coverage.FailureMessage(), "1 of 3") {
		t.Errorf("coverage message = %q, want it to say how much was lost",
			out.Coverage.FailureMessage())
	}

	// Still running: the transition is the caller's to apply, and a
	// crash before it does is what reconcile-on-read exists for.
	run, err := audiobooks.GetByBookID(ctx, bookID)
	if err != nil {
		t.Fatalf("GetByBookID: %v", err)
	}
	if run.State != model.AudiobookRunning {
		t.Errorf("run state = %q, want running — RecordSegment reports the transition, "+
			"AudiobookService applies it", run.State)
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

	// The caller applies that verdict. The transition reports that it
	// moved the run, which is what publishes exactly once.
	moved, err := audiobooks.Transition(ctx, bookID, model.Transition{
		To: model.AudiobookFailed, From: []model.AudiobookState{model.AudiobookPending, model.AudiobookRunning, model.AudiobookReady, model.AudiobookCanceled},
		Error: out.Coverage.FailureMessage(),
	})
	if err != nil {
		t.Fatalf("Transition to failed: %v", err)
	}
	if !moved {
		t.Fatal("the transition reported no change on a running run")
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

	// A second FailRun is a no-op, so the retry cannot re-publish a
	// failure the user was already told about.
	moved, err = audiobooks.Transition(ctx, bookID, model.Transition{
		To: model.AudiobookFailed, From: []model.AudiobookState{model.AudiobookPending, model.AudiobookRunning, model.AudiobookReady, model.AudiobookCanceled},
		Error: "something else",
	})
	if err != nil {
		t.Fatalf("FailRun again: %v", err)
	}
	if moved {
		t.Error("FailRun moved an already-failed run — the UI would be notified twice")
	}
	run, err := audiobooks.GetByBookID(ctx, bookID)
	if err != nil {
		t.Fatalf("GetByBookID: %v", err)
	}
	if run.Error == "something else" {
		t.Error("the second failure overwrote the reason the run actually failed for")
	}
}

// A cancel taken while a segment was in flight outranks the segment's own
// result: the user said stop, and finalizing would publish the partial
// they asked not to have (ADR-0028 §6).
func TestRecordSegmentDoesNotFinalizeACanceledRun(t *testing.T) {
	bookID, audiobooks := audiobookFixture(t, repotest.New(t), 1)
	ctx := context.Background()

	if _, err := audiobooks.Transition(ctx, bookID, model.Transition{
		To: model.AudiobookCanceled, From: []model.AudiobookState{model.AudiobookPending, model.AudiobookRunning},
	}); err != nil {
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
	if _, err := audiobooks.Transition(ctx, bookID, model.Transition{
		To: model.AudiobookReady, From: []model.AudiobookState{model.AudiobookPending, model.AudiobookRunning},
	}); err != nil {
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

// The cap the plan was split at is part of what the run pins, and a
// segment job hours later reads it back to reproduce that split. Losing
// it in the round-trip would put the worker back on the live settings
// value, which is the bug the column exists to close (#189).
func TestStartRoundTripsTheSegmentationCap(t *testing.T) {
	d := repotest.New(t)
	ctx := context.Background()

	lib, err := repo.NewLibraryRepo(d).CreateLibrary(ctx, "Narration", "narration-cap", "/tmp/narration-cap", nil)
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	b, err := repo.NewBookRepo(d).Create(ctx, model.Book{
		LibraryID: lib.ID, Title: "Dune", Author: "Frank Herbert", Format: "EPUB", Path: "dune-cap.epub",
	})
	if err != nil {
		t.Fatalf("create book: %v", err)
	}

	audiobooks := repo.NewBookAudiobookRepo(d)
	if err := audiobooks.Start(ctx, model.Audiobook{
		BookID: b.ID, Engine: "openai", Voice: "alloy", Model: "tts-1", SegmentChars: 12_345,
	}, []model.AudiobookSegment{
		{BookID: b.ID, Seq: 0, ChapterTitle: "Chapter", State: model.SegmentPending, CharStart: 0, CharEnd: 100},
	}); err != nil {
		t.Fatalf("start run: %v", err)
	}

	run, err := audiobooks.GetByBookID(ctx, b.ID)
	if err != nil {
		t.Fatalf("GetByBookID: %v", err)
	}
	if run.SegmentChars != 12_345 {
		t.Errorf("SegmentChars = %d, want the 12345 the plan was split at", run.SegmentChars)
	}
}

// The migration's backfill value and the splitter's default are the same
// number written twice, once in Go and once in SQL that cannot reference
// it. They mean "the cap every run before the column existed was planned
// at", so a change to one that misses the other silently mis-describes
// every old run.
func TestSegmentCharsDefaultMatchesTheSplittersDefault(t *testing.T) {
	d := repotest.New(t)

	var backfill int
	err := d.SQL.QueryRowContext(context.Background(),
		`SELECT column_default::int FROM information_schema.columns
		 WHERE table_name = 'book_audiobooks' AND column_name = 'segment_chars'`).Scan(&backfill)
	if err != nil {
		t.Fatalf("read column default: %v", err)
	}

	if backfill != fileproc.DefaultSegmentChars {
		t.Errorf("migration backfills segment_chars with %d, but the splitter defaults to %d",
			backfill, fileproc.DefaultSegmentChars)
	}
}

// RecordSegment must take the run row's lock before it reads coverage.
//
// This is the property the FOR UPDATE clause exists for: without it, two
// segments completing at the same instant each read a coverage snapshot
// missing the other's uncommitted write, both conclude the run is
// unfinished, and neither dispatches finalize — a run stuck at 0% with
// all its audio bought and staged.
//
// Racing two goroutines does not test it: whichever transaction commits
// first is visible to the second under READ COMMITTED anyway, so that
// test passes with the lock deleted. What does test it is holding the
// row lock and watching RecordSegment wait for it.
func TestRecordSegmentWaitsForTheRunRowLock(t *testing.T) {
	d := repotest.New(t)
	bookID, audiobooks := audiobookFixture(t, d, 2)
	ctx := context.Background()

	// A second connection of its own, because repotest's pool is capped
	// at one so that SET search_path applies to everything it runs. Take
	// the holder from that pool and RecordSegment blocks waiting for a
	// free connection rather than for the lock — a test that passes with
	// the lock deleted, which is worse than no test.
	holder := secondSession(t, d)
	defer func() { _ = holder.Rollback() }()
	// FOR KEY SHARE, not FOR UPDATE, and the choice is the whole test.
	// Writing a segment row takes a KEY SHARE lock on its parent by
	// itself, to stop the referenced key moving — so a holder using FOR
	// UPDATE blocks RecordSegment whether or not it locks the run row,
	// and the test passes with the clause deleted. KEY SHARE conflicts
	// with FOR UPDATE and with nothing the foreign key needs, so what
	// blocks here is the explicit lock and only the explicit lock.
	var state string
	if err := holder.QueryRowContext(ctx,
		`SELECT state FROM book_audiobooks WHERE book_id = $1 FOR KEY SHARE`, bookID).Scan(&state); err != nil {
		t.Fatalf("hold the run row: %v", err)
	}

	landed := make(chan error, 1)
	go func() { _, err := audiobooks.RecordSegment(ctx, bookID, 0, doneResult(0)); landed <- err }()

	select {
	case err := <-landed:
		t.Fatalf("RecordSegment completed (err=%v) while another transaction held the run row — "+
			"it is not taking the lock, so concurrent segments can each miss the other's write", err)
	case <-time.After(300 * time.Millisecond):
		// Blocked, which is the point.
	}

	if err := holder.Commit(); err != nil {
		t.Fatalf("release the lock: %v", err)
	}
	select {
	case err := <-landed:
		if err != nil {
			t.Fatalf("RecordSegment after the lock was released: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RecordSegment never completed after the lock was released")
	}
}

// secondSession opens an independent connection into the same test
// schema and begins a transaction on it.
func secondSession(t *testing.T, d *db.DB) *sql.Tx {
	t.Helper()
	ctx := context.Background()

	var schema string
	if err := d.SQL.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatalf("read the test schema: %v", err)
	}

	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("second pool: %v", err)
	}
	other := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = other.Close(); pool.Close() })
	// search_path is per connection, so this one has to be told too.
	other.SetMaxOpenConns(1)
	if _, err := other.ExecContext(ctx, `SET search_path TO `+pq(schema)); err != nil {
		t.Fatalf("second session search_path: %v", err)
	}

	tx, err := other.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("second session begin: %v", err)
	}
	return tx
}

// pq quotes an identifier for interpolation, which a schema name cannot
// be parameterised into.
func pq(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

// The write this refactor exists for. SetReady was an unconditional
// UPDATE, so a finalize job already assembling when the user cancelled
// marked the run ready anyway — the user was billed for a run they
// stopped and then handed the audiobook (#210, ADR-0028 §6).
//
// At this layer rather than only against the service's fake: the guard
// is a WHERE clause, and a fake that reimplements it proves nothing
// about the SQL.
func TestTransitionRefusesToMoveAConcludedRun(t *testing.T) {
	bookID, audiobooks := audiobookFixture(t, repotest.New(t), 2)
	ctx := context.Background()

	moved, err := audiobooks.Transition(ctx, bookID, model.Transition{
		To: model.AudiobookCanceled, From: model.LiveStates(),
	})
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !moved {
		t.Fatal("a running run refused a cancel")
	}

	fileID, totalMS := "11111111-1111-1111-1111-111111111111", int64(90_000)
	moved, err = audiobooks.Transition(ctx, bookID, model.Transition{
		To: model.AudiobookReady, From: model.LiveStates(),
		FileID: &fileID, DurationMS: &totalMS,
	})
	if err != nil {
		t.Fatalf("finalize after cancel: %v", err)
	}
	if moved {
		t.Error("a finalize in flight moved a canceled run to ready")
	}

	run, err := audiobooks.GetByBookID(ctx, bookID)
	if err != nil {
		t.Fatalf("GetByBookID: %v", err)
	}
	if run.State != model.AudiobookCanceled {
		t.Errorf("state = %q, want canceled to have survived the late write", run.State)
	}
	if run.FileID != nil {
		t.Errorf("file_id = %q — a refused transition wrote its payload anyway", *run.FileID)
	}
}

// The moved flag is the other half of the guard: it is what keeps the
// SSE publish to one when two segments settle a run at the same instant.
func TestTransitionReportsWhetherTheRowMoved(t *testing.T) {
	bookID, audiobooks := audiobookFixture(t, repotest.New(t), 2)
	ctx := context.Background()

	fail := model.Transition{
		To: model.AudiobookFailed,
		From: []model.AudiobookState{
			model.AudiobookPending, model.AudiobookRunning,
			model.AudiobookReady, model.AudiobookCanceled,
		},
		Error: "1 of 2 segments failed",
	}

	first, err := audiobooks.Transition(ctx, bookID, fail)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := audiobooks.Transition(ctx, bookID, fail)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !first || second {
		t.Errorf("moved = (%v, %v), want (true, false) so the UI is told once", first, second)
	}
}
