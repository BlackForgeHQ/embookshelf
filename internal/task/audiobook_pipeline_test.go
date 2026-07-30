// SPDX-License-Identifier: AGPL-3.0-or-later

package task_test

// One narration run, driven end to end.
//
// Everything else that tests this subsystem stops at a seam. The segment
// worker's tests hand their result to a fake AdvanceAfterSegment and
// assert on what was handed over; the run service's tests start from a
// fake RecordSegment returning a canned model.AudiobookOutcome. The
// outcome itself — what the repo's locked write derived from the
// coverage it observed — is therefore asserted twice against two
// different doubles and never once against the thing that produces it.
// Answering "what happens when a run fails at segment N" meant reading
// eleven files and trusting that the two fakes agreed (#248).
//
// So: the real repo against a real Postgres, the real AudiobookService,
// the real segment worker, and exactly one fake — the TTS engine, which
// is the one collaborator that costs money and is not ours. River is
// stood in for by driving the jobs the service enqueued, in the order
// the test states, because what River does with a returned error is
// River's retry policy and belongs to River's own configuration.
//
// The fakes in generation_fakes_test.go are deliberately not reused for
// the engine: fakeEngine models chunked's BeforeChunk ordering and says
// in its own comment not to lean on the rest, and these tests need a
// reply that changes between one call and the next.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/audio/audiotest"
	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/task"
	"github.com/blackforge/embookshelf/internal/tts"
)

// pipelineSegmentChars is the cap the run is planned at — large enough
// that each chapter of the fixture is one segment, so "segment 1 failed"
// and "chapter 2 failed" are the same sentence.
const pipelineSegmentChars = 1000

// ---------------------------------------------------------------------------
// The one fake
// ---------------------------------------------------------------------------

// scriptedEngine is a tts.Engine whose answer the test changes between
// calls, which is what a transient failure is: the same request, a
// different reply the second time.
type scriptedEngine struct {
	reply func(req tts.Request) ([]byte, error)
	calls int
	texts []string
}

func (e *scriptedEngine) Synthesize(ctx context.Context, req tts.Request) ([]byte, error) {
	// Mirrors chunked.Synthesize: the callback runs before the engine is
	// reached, so the cancel check the worker installs is live here as it
	// is in production.
	if req.BeforeChunk != nil {
		if err := req.BeforeChunk(ctx); err != nil {
			return nil, err
		}
	}
	e.calls++
	e.texts = append(e.texts, req.Text)
	return e.reply(req)
}

func (e *scriptedEngine) ListVoices(context.Context) ([]tts.Voice, error) { return nil, nil }

var _ tts.Engine = (*scriptedEngine)(nil)

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

// narrationPipeline is a book, a run service and a segment worker wired
// to one Postgres schema, plus the recorders for the three things that
// leave the process: the queue, the SSE publish, and the staging sweep.
type narrationPipeline struct {
	t       *testing.T
	db      *db.DB
	runs    *repo.BookAudiobookRepo
	files   *repo.FileRepo
	svc     *service.AudiobookService
	engine  *scriptedEngine
	deps    task.SegmentDeps
	book    model.Book
	staging task.Staging
	epub    []byte
	// gen is the generation of the run start() most recently installed:
	// the plan the jobs below belong to. A test that wants a superseded
	// job says so by driving one with an older value.
	gen int

	// queued is every job the service handed the queue, in order. Nothing
	// runs it: each test drives the jobs it wants, in the order it wants,
	// which is how a River retry is expressed without a River.
	queued []jobs.Args
	// publishes counts the run service's SSE emissions — the ones the
	// guarded write decided actually moved the row.
	publishes int
	// workerPublishes counts the segment worker's own emissions.
	workerPublishes int
	// sweeps records SweepStaging calls. Failure must never appear here:
	// a failed run keeps every paid-for segment (ADR-0028 §6).
	sweeps []string
}

// pipelineOpener yields the fixture's bytes, a fresh reader each time,
// because the run is planned from the EPUB and every segment job
// re-extracts it.
type pipelineOpener struct{ p *narrationPipeline }

func (o pipelineOpener) Open(context.Context, model.Book) (storage.Source, error) {
	return memSrc{Reader: bytes.NewReader(o.p.epub), size: int64(len(o.p.epub))}, nil
}

func (p *narrationPipeline) Enqueue(_ context.Context, args jobs.Args) error {
	p.queued = append(p.queued, args)
	return nil
}

func newNarrationPipeline(t *testing.T) *narrationPipeline {
	t.Helper()
	ctx := context.Background()

	d := repotest.New(t)
	lib, err := repo.NewLibraryRepo(d).CreateLibrary(ctx, "Narration", "narration", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	books := repo.NewBookRepo(d)
	book, err := books.Create(ctx, model.Book{
		LibraryID: lib.ID, Title: "Dune", Author: "Frank Herbert",
		Format: "EPUB", Path: "dune.epub",
	})
	if err != nil {
		t.Fatalf("create book: %v", err)
	}

	p := &narrationPipeline{
		t:       t,
		db:      d,
		runs:    repo.NewBookAudiobookRepo(d),
		files:   repo.NewFileRepo(d),
		engine:  &scriptedEngine{},
		book:    book,
		staging: task.NewStaging(dataRoot(t)),
		epub: drainSource(t, epubWithChapters(t,
			"Muad'Dib learned rapidly because his first training was in how to learn.",
			"A beginning is the time for taking the most delicate care.",
		)),
	}

	p.svc = service.NewAudiobookService(service.AudiobookDeps{
		Store:   p.runs,
		Enqueue: p,
		Books:   pipelineOpener{p},
		Settings: func(context.Context) (repo.AudiobookConfig, error) {
			return repo.AudiobookConfig{Enabled: true, Engine: "openai"}, nil
		},
		Publish:      func(string) { p.publishes++ },
		SweepStaging: func(bookID string) { p.sweeps = append(p.sweeps, bookID) },
	})

	p.deps = task.SegmentDeps{
		Config: func(context.Context) (repo.AudiobookConfig, error) {
			return repo.AudiobookConfig{Enabled: true, Engine: "openai"}, nil
		},
		Engine: func(repo.AudiobookConfig) (repo.ConfiguredEngine, error) {
			return repo.ConfiguredEngine{ID: tts.EngineOpenAI, Engine: p.engine}, nil
		},
		// The real repo on both seams, and the real service between them:
		// the segment worker's write goes through AdvanceAfterSegment into
		// RecordSegment's locked transaction and comes back out as a
		// transition applied to this same row.
		Runs:    p.runs,
		Advance: p.svc,
		Books:   books,
		Open:    pipelineOpener{p}.Open,
		Publish: func(string) { p.workerPublishes++ },
		Staging: p.staging,
	}
	return p
}

// dataRoot is the staging root for one test, resolved through the type
// that owns what an unset root means (config.DataRoot) rather than a
// bare string.
func dataRoot(t *testing.T) config.DataRoot {
	t.Helper()
	root, err := config.NewDataRoot(t.TempDir())
	if err != nil {
		t.Fatalf("NewDataRoot: %v", err)
	}
	return root
}

// drainSource reads a fixture into bytes so every Open can hand out a
// fresh reader over the same archive.
func drainSource(t *testing.T, src storage.Source) []byte {
	t.Helper()
	b, err := io.ReadAll(io.NewSectionReader(src, 0, src.Size()))
	if err != nil {
		t.Fatalf("read epub fixture: %v", err)
	}
	return b
}

// start plans and persists the run through the service, which is also
// what enqueues the segment jobs the tests then drive.
func (p *narrationPipeline) start() {
	p.t.Helper()
	err := p.svc.Start(context.Background(), p.book, service.AudiobookOptions{
		Engine: "openai", Voice: "alloy", Model: "tts-1", SegmentChars: pipelineSegmentChars,
	})
	if err != nil {
		p.t.Fatalf("Start: %v", err)
	}
	p.gen = p.run().Generation
}

// runSegment executes one segment job of the current run with the engine
// currently scripted, and returns what the worker handed back to River.
func (p *narrationPipeline) runSegment(seq int, reply func(tts.Request) ([]byte, error)) error {
	p.t.Helper()
	return p.runSegmentOf(p.gen, seq, reply)
}

// runSegmentOf is the same, for a job that names its generation — which
// is how a job left over from a plan that has been replaced is expressed
// without a River to hold it.
func (p *narrationPipeline) runSegmentOf(gen, seq int, reply func(tts.Request) ([]byte, error)) error {
	p.t.Helper()
	p.engine.reply = reply
	return task.AudiobookSegment(context.Background(),
		jobs.AudiobookSegmentArgs{BookID: p.book.ID, Seq: seq, Generation: gen}, p.deps)
}

// speaks is a successful engine reply: four frames of real MPEG audio,
// so audio.Payload accepts it and the duration on the row is measured
// rather than invented.
func speaks() ([]byte, error) { return audiotest.Frames(4), nil }

func (p *narrationPipeline) run() model.Audiobook {
	p.t.Helper()
	run, err := p.runs.GetByBookID(context.Background(), p.book.ID)
	if err != nil {
		p.t.Fatalf("GetByBookID: %v", err)
	}
	return run
}

func (p *narrationPipeline) segment(seq int) model.AudiobookSegment {
	p.t.Helper()
	seg, err := p.runs.GetSegment(context.Background(), p.book.ID, seq)
	if err != nil {
		p.t.Fatalf("GetSegment %d: %v", seq, err)
	}
	return seg
}

func (p *narrationPipeline) stagedExists(seq int) bool {
	p.t.Helper()
	return p.stagedExistsOf(p.gen, seq)
}

func (p *narrationPipeline) stagedExistsOf(gen, seq int) bool {
	p.t.Helper()
	path, err := p.staging.SegmentPath(p.book.ID, gen, seq)
	if err != nil {
		p.t.Fatalf("Staging.SegmentPath: %v", err)
	}
	_, err = os.Stat(path)
	return err == nil
}

// queuedSegments is the sequence numbers the service asked the queue for.
func (p *narrationPipeline) queuedSegments() []int {
	var out []int
	for _, a := range p.queued {
		if seg, ok := a.(jobs.AudiobookSegmentArgs); ok {
			out = append(out, seg.Seq)
		}
	}
	return out
}

func (p *narrationPipeline) queuedFinalizes() int {
	n := 0
	for _, a := range p.queued {
		if _, ok := a.(jobs.AudiobookFinalizeArgs); ok {
			n++
		}
	}
	return n
}

// age backdates the run so the staging sweeper's TTL comparison has
// something to compare against. Raw SQL because nothing in production
// moves updated_at backwards, and a test that slept seven days would be
// a different kind of unusable.
func (p *narrationPipeline) age(days int) {
	p.t.Helper()
	_, err := p.db.SQL.ExecContext(context.Background(),
		`UPDATE book_audiobooks SET updated_at = now() - make_interval(days => $2) WHERE book_id = $1`,
		p.book.ID, days)
	if err != nil {
		p.t.Fatalf("age run: %v", err)
	}
}

func (p *narrationPipeline) sweepStaging() int {
	p.t.Helper()
	n, err := p.staging.Sweep(context.Background(), p.runs)
	if err != nil {
		p.t.Fatalf("Staging.Sweep: %v", err)
	}
	return n
}

// ---------------------------------------------------------------------------
// The two runs
// ---------------------------------------------------------------------------

// A two-segment run where the second segment cannot be bought at any
// price: the whole path, worker to repo, for the question the issue
// asks.
//
// What this holds that no single-tier test can. The worker records a
// failure; the repo's locked write observes the coverage that failure
// produced and derives the transition from it; the service applies that
// transition through the one guarded write; and the run ends up failed,
// carrying the message the coverage phrased, with the first segment's
// audio still on disk because a retry must not have to buy it again.
// Break the guard, the derivation, or the sweep policy and this fails.
func TestNarrationRunLandsFailedWhenASegmentFailsPermanently(t *testing.T) {
	p := newNarrationPipeline(t)
	p.start()

	if got := p.queuedSegments(); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("Start queued segments %v, want [0 1] — the fixture is meant to plan two", got)
	}
	if p.run().State != model.AudiobookRunning {
		t.Fatalf("run state after Start = %q, want running", p.run().State)
	}

	if err := p.runSegment(0, func(tts.Request) ([]byte, error) { return speaks() }); err != nil {
		t.Fatalf("segment 0: %v", err)
	}

	// A bad key is permanent: no amount of retrying buys this segment.
	badKey := fmt.Errorf("%w: engine returned 401: invalid api key", tts.ErrPermanent)
	if err := p.runSegment(1, func(tts.Request) ([]byte, error) { return nil, badKey }); err != nil {
		t.Fatalf("segment 1 returned %v, want nil — a permanent failure must not ask River to retry", err)
	}

	// The run landed, and the message is Coverage's phrasing rather than
	// the engine's: the user is told how much of their book is missing,
	// and the segment row keeps why.
	run := p.run()
	if run.State != model.AudiobookFailed {
		t.Fatalf("run state = %q, want failed", run.State)
	}
	if run.Error != "1 of 2 segments failed" {
		t.Errorf("run error = %q, want %q", run.Error, "1 of 2 segments failed")
	}
	if got := p.segment(1); got.State != model.SegmentFailed || !strings.Contains(got.Error, "invalid api key") {
		t.Errorf("segment 1 = %q/%q, want failed carrying the engine's reason", got.State, got.Error)
	}
	if got := p.segment(0); got.State != model.SegmentDone || got.DurationMS == 0 || got.StagedPath == "" {
		t.Errorf("segment 0 = %+v, want done with a measured duration and a staged path", got)
	}
	// Each job bought its own chapter, once. The engine is the only thing
	// here that costs money, so what it was asked for is worth stating:
	// two calls, two different pieces of prose, re-extracted from the
	// EPUB against the ranges the planner stored.
	if p.engine.calls != 2 {
		t.Errorf("engine called %d times for a two-segment run", p.engine.calls)
	}
	if len(p.engine.texts) == 2 && p.engine.texts[0] == p.engine.texts[1] {
		t.Errorf("both segments were narrated from the same text %q", p.engine.texts[0])
	}
	// The worker publishes the permanent failure itself, so a page open on
	// the run learns without waiting for its next poll.
	if p.workerPublishes != 1 {
		t.Errorf("worker published %d times on a permanent failure, want 1", p.workerPublishes)
	}

	// Staging survives the failure. This is the half of ADR-0028 §6 that
	// distinguishes failure from cancel, and it is worth an assertion at
	// both levels: the service did not sweep, and the sweeper that runs
	// hourly declines a run this fresh.
	if !p.stagedExists(0) {
		t.Error("segment 0's audio was discarded by the failure — Retry would have to buy it again")
	}
	if len(p.sweeps) != 0 {
		t.Errorf("SweepStaging called %v on a failed run; failure keeps the work, cancel does not", p.sweeps)
	}
	if n := p.sweepStaging(); n != 0 {
		t.Errorf("the staging sweep reclaimed %d run(s) that failed moments ago, want 0", n)
	}
	if !p.stagedExists(0) {
		t.Error("the staging sweep deleted a fresh failed run's audio")
	}

	// Nothing was finalized, and the publish that tells open pages to stop
	// polling fired once, for the write that actually moved the row.
	if p.queuedFinalizes() != 0 {
		t.Errorf("finalize was queued %d time(s) for a run missing a segment", p.queuedFinalizes())
	}
	if p.publishes != 2 {
		t.Errorf("service published %d times, want 2: running at Start and failed at the landing", p.publishes)
	}

	// Reconcile-on-read is stable on a failed run. Status is hit on every
	// book-detail load, and a rule that re-derived "fail" here would
	// re-publish and re-write forever (#206).
	before := p.publishes
	got, cov, err := p.svc.Status(context.Background(), p.book.ID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.State != model.AudiobookFailed || got.Error != "1 of 2 segments failed" {
		t.Errorf("Status = %q/%q, want the failed run and its message", got.State, got.Error)
	}
	if cov.Total != 2 || cov.Done != 1 || cov.Failed != 1 {
		t.Errorf("coverage = %+v, want 2 total / 1 done / 1 failed", cov)
	}
	if p.publishes != before || p.queuedFinalizes() != 0 {
		t.Errorf("a status read on a failed run published %d more time(s) and queued %d finalize(s), want neither",
			p.publishes-before, p.queuedFinalizes())
	}

	// And once the run is old enough, the same sweeper does reclaim it —
	// the other half of the policy, which an assertion on the fresh case
	// alone would let anyone delete.
	p.age(int(task.StaleStagingTTL/(24*time.Hour)) + 1)
	if n := p.sweepStaging(); n != 1 {
		t.Errorf("the staging sweep reclaimed %d run(s) past the TTL, want 1", n)
	}
	if p.stagedExists(0) {
		t.Error("staging survived a sweep that reported reclaiming it")
	}
}

// The same path, for the failure that was worth retrying.
//
// A 503 is not a verdict on the request, so the worker hands River the
// error and the run stays running — the coverage it produced is not
// settled, and a run marked failed here would strand a retry that was
// always going to succeed. The retry then lands, the last segment lands,
// and the run reaches ready through the guarded → ready write.
//
// The interleaving is stated rather than incidental: River retries the
// failed segment before the other one finishes. The opposite order — the
// sibling landing first — settles the coverage with a failure in it and
// concludes the run, which is the behaviour the permanent case above
// covers.
func TestNarrationRunReachesReadyWhenATransientFailureIsRetried(t *testing.T) {
	p := newNarrationPipeline(t)
	p.start()

	flaky := errors.New("openai: 503 service unavailable")
	err := p.runSegment(0, func(tts.Request) ([]byte, error) { return nil, flaky })
	if err == nil {
		t.Fatal("segment 0 returned nil for a transient failure — River would never retry it")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("err = %v, want the engine's reason so the retry is attributable", err)
	}
	if got := p.segment(0).State; got != model.SegmentFailed {
		t.Errorf("segment 0 = %q, want failed so Retry re-enqueues it", got)
	}
	// One failure out of two segments is not a settled run: the other
	// segment has not reported, so there is nothing to conclude yet.
	if got := p.run().State; got != model.AudiobookRunning {
		t.Fatalf("run state = %q after one transient failure, want running", got)
	}

	// River's retry.
	if err := p.runSegment(0, func(tts.Request) ([]byte, error) { return speaks() }); err != nil {
		t.Fatalf("segment 0 retry: %v", err)
	}
	if got := p.segment(0); got.State != model.SegmentDone || got.Error != "" {
		t.Errorf("segment 0 after the retry = %q/%q, want done with the stale message cleared",
			got.State, got.Error)
	}
	if p.queuedFinalizes() != 0 {
		t.Fatal("finalize was queued while a segment was still outstanding")
	}

	if err := p.runSegment(1, func(tts.Request) ([]byte, error) { return speaks() }); err != nil {
		t.Fatalf("segment 1: %v", err)
	}
	// Three engine calls for two segments: the retry is the extra one, and
	// the segment that already succeeded is never bought twice.
	if p.engine.calls != 3 {
		t.Errorf("engine called %d times, want 3 — one failure, its retry, and the other segment", p.engine.calls)
	}
	if p.queuedFinalizes() != 1 {
		t.Fatalf("finalize queued %d times once every segment landed, want exactly 1", p.queuedFinalizes())
	}
	// The run is deliberately still running: finalize sets ready when it
	// has the file, and a run marked ready without one is a book the UI
	// offers and the player cannot open.
	if got := p.run().State; got != model.AudiobookRunning {
		t.Errorf("run state = %q with finalize queued, want running until the file exists", got)
	}

	// Stand in for the finalize worker's report. Assembling the file is
	// that worker's own tested job; what this test is here for is the
	// transition it triggers, which is the service's and the repo's.
	fileID := p.narrationFile()
	if err := p.svc.NarrationAssembled(context.Background(), p.book.ID, fileID, 4_000); err != nil {
		t.Fatalf("NarrationAssembled: %v", err)
	}

	run := p.run()
	if run.State != model.AudiobookReady {
		t.Fatalf("run state = %q, want ready", run.State)
	}
	if run.FileID == nil || *run.FileID != fileID {
		t.Errorf("run file_id = %v, want the narration's files row %s", run.FileID, fileID)
	}
	if run.DurationMS != 4_000 || run.Error != "" {
		t.Errorf("run = %dms/%q, want the measured duration and no error", run.DurationMS, run.Error)
	}

	before := p.publishes
	got, cov, err := p.svc.Status(context.Background(), p.book.ID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.State != model.AudiobookReady {
		t.Errorf("Status = %q, want ready", got.State)
	}
	if !cov.Complete() || cov.Failed != 0 {
		t.Errorf("coverage = %+v, want complete with no failures", cov)
	}
	// A ready run has its file: reconcile-on-read must not re-finalize it.
	if p.queuedFinalizes() != 1 || p.publishes != before {
		t.Errorf("a status read on a ready run queued %d finalize(s) and published %d more time(s), want neither",
			p.queuedFinalizes()-1, p.publishes-before)
	}
}

// ---------------------------------------------------------------------------
// The run that was replaced while it was still working
// ---------------------------------------------------------------------------

// regenerate is what a user pressing Generate on a book that already has
// a run does: stop the current one, then start a fresh plan over it.
//
// Cancel first because Start refuses over a live run — the segments it
// would wipe are audio already paid for, and cancel is the stop-loss
// ADR-0028 §6 puts in front of that.
func (p *narrationPipeline) regenerate() {
	p.t.Helper()
	if err := p.svc.Cancel(context.Background(), p.book.ID); err != nil {
		p.t.Fatalf("Cancel: %v", err)
	}
	p.start()
}

// A segment job whose run was replaced while it was inside the engine
// call must land nowhere.
//
// The window this is about is the whole of a synthesis. A job claims its
// segment before the engine call (audiobook.go), the call takes minutes,
// and a regeneration in between wipes the plan and installs another one.
// Book-plus-sequence addresses both plans identically, so without a
// generation the stale result writes into the live run's row — and the
// interleaving here is the expensive version of that: the new run has
// every other segment done, so a stale write for the missing one settles
// its coverage, dispatches finalize, and publishes a book assembled half
// from audio nobody asked for.
//
// Everything is real here for the reason this file exists: the refusal
// has to hold in the repo's locked write, and a test that stopped at the
// service seam would be asserting against a double's copy of it.
func TestASupersededSegmentJobDoesNotTouchTheRunThatReplacedIt(t *testing.T) {
	p := newNarrationPipeline(t)
	p.start()

	if err := p.runSegment(0, func(tts.Request) ([]byte, error) { return speaks() }); err != nil {
		t.Fatalf("segment 0 of the first run: %v", err)
	}

	publishesAtRegen, finalizesAtRegen := 0, 0
	// The regeneration lands while segment 1 is inside the engine call —
	// the claim is already made, the audio is already being bought, and
	// nothing has told this job its plan no longer exists.
	err := p.runSegment(1, func(tts.Request) ([]byte, error) {
		p.regenerate()
		// The new run then completes every segment of its own plan except
		// this one, so a stale write for seq 1 would be the write that
		// settles it.
		if err := p.runSegment(0, func(tts.Request) ([]byte, error) { return speaks() }); err != nil {
			t.Fatalf("segment 0 of the second run: %v", err)
		}
		publishesAtRegen, finalizesAtRegen = p.publishes, p.queuedFinalizes()
		return speaks()
	})
	if err != nil {
		t.Fatalf("the superseded segment job returned %v, want nil — there is nothing for River to retry", err)
	}

	// The plan the stale job addressed is not the plan that exists.
	if got := p.segment(1); got.State != model.SegmentPending || got.StagedPath != "" || got.DurationMS != 0 {
		t.Errorf("segment 1 of the new run = %q/%q/%dms, want an untouched pending row — "+
			"a superseded job wrote into the plan that replaced it",
			got.State, got.StagedPath, got.DurationMS)
	}
	// And no transition was derived from it. Coverage read after that write
	// would have said 2 of 2.
	if got := p.queuedFinalizes(); got != finalizesAtRegen {
		t.Errorf("finalize was queued %d more time(s) by a superseded job, want 0", got-finalizesAtRegen)
	}
	if p.publishes != publishesAtRegen {
		t.Errorf("a superseded job published %d state change(s), want 0", p.publishes-publishesAtRegen)
	}
	if got := p.run().State; got != model.AudiobookRunning {
		t.Errorf("run state = %q, want running — the new run is still missing a segment", got)
	}
	// The bytes went to the superseded run's own directory, so the live
	// run's segment 1 has no file at all. Staging is scoped by generation
	// because os.WriteFile is not atomic and because the two plans can
	// differ — a stale write at the live path could leave a truncated
	// file, or the right length of the wrong voice.
	if p.stagedExistsOf(p.gen, 1) {
		t.Error("a superseded job's audio landed at the live run's staging path")
	}
	if !p.stagedExistsOf(p.gen-1, 1) {
		t.Error("the superseded job's audio went nowhere; the test is no longer exercising a stale write")
	}
}

// The other half of the same guard, and the cheaper half: a job that has
// not started synthesizing yet must not even claim.
//
// Distinct from the case above rather than a subset of it. This one is
// refused by MarkSegmentRunning before any money is spent; that one is
// refused by RecordSegment, minutes later, with the audio already bought.
// A guard on the claim alone would pass this test and fail that one.
func TestASupersededSegmentJobCannotClaimTheRunThatReplacedIt(t *testing.T) {
	p := newNarrationPipeline(t)
	p.start()
	stale := p.gen

	p.regenerate()

	before := p.publishes
	if err := p.runSegmentOf(stale, 1, func(tts.Request) ([]byte, error) { return speaks() }); err != nil {
		t.Fatalf("the superseded segment job returned %v, want nil", err)
	}

	if p.engine.calls != 0 {
		t.Errorf("the engine was called %d time(s) for a job whose plan no longer exists — that is billable",
			p.engine.calls)
	}
	if got := p.segment(1).State; got != model.SegmentPending {
		t.Errorf("segment 1 of the new run = %q, want pending — a superseded job claimed it", got)
	}
	if p.publishes != before || p.queuedFinalizes() != 0 {
		t.Errorf("a superseded job published %d time(s) and queued %d finalize(s), want neither",
			p.publishes-before, p.queuedFinalizes())
	}
}

// A segment job enqueued before generations existed still claims its
// segment.
//
// The deploy story, and it rests on a zero value: such a job's args have
// no generation field, so Go decodes 0, and 0 is also what the migration
// left on every existing row. A row is only still at 0 if nothing has
// restarted it since the deploy, so the job addressing it genuinely is
// the current one. Written out because "it works because both sides
// happen to be zero" is precisely the reasoning that gets lost, and the
// alternative — a NULL generation, or a pointer in the args — would have
// failed every in-flight job of an upgraded deployment instead.
//
// The row is put back to 0 by hand because nothing in production moves a
// generation backwards; that is the state a mid-run upgrade leaves, not a
// state this code can reach.
func TestAJobEnqueuedBeforeGenerationsExistedStillClaimsItsSegment(t *testing.T) {
	p := newNarrationPipeline(t)
	p.start()

	_, err := p.db.SQL.ExecContext(context.Background(),
		`UPDATE book_audiobooks SET generation = 0 WHERE book_id = $1`, p.book.ID)
	if err != nil {
		t.Fatalf("reset generation: %v", err)
	}
	p.gen = 0

	// Args as River stored them before the field existed: the zero value
	// is what json.Unmarshal leaves behind, so this is that job.
	if err := p.runSegmentOf(0, 0, func(tts.Request) ([]byte, error) { return speaks() }); err != nil {
		t.Fatalf("a job from before the column existed: %v", err)
	}

	if got := p.segment(0); got.State != model.SegmentDone || got.StagedPath == "" {
		t.Errorf("segment 0 = %q/%q, want done — the upgrade silenced a job that was still current",
			got.State, got.StagedPath)
	}
	// And the first start after the deploy moves the row past it, which is
	// what makes genuinely stale jobs go quiet.
	p.regenerate()
	if p.run().Generation == 0 {
		t.Fatal("a start left the run at generation 0; every pre-deploy job would stay live forever")
	}
	if err := p.runSegmentOf(0, 0, func(tts.Request) ([]byte, error) { return speaks() }); err != nil {
		t.Fatalf("the same job after a restart: %v", err)
	}
	if got := p.segment(0).State; got != model.SegmentPending {
		t.Errorf("segment 0 of the restarted run = %q, want pending", got)
	}
}

// Generate over a run that is still working refuses, and refuses without
// touching it.
//
// The first already-running refusal on this path. What a second start
// would delete is not a stale record: it is a plan whose completed
// segments are audio that has been paid for, while the jobs still working
// through it carry on spending. Cancel is the stop-loss ADR-0028 §6 puts
// in front of that, and this makes a user take it deliberately.
func TestGenerateRefusesOverARunThatIsStillWorking(t *testing.T) {
	p := newNarrationPipeline(t)
	p.start()
	if err := p.runSegment(0, func(tts.Request) ([]byte, error) { return speaks() }); err != nil {
		t.Fatalf("segment 0: %v", err)
	}

	gen, queued := p.gen, len(p.queued)
	err := p.svc.Generate(context.Background(), p.book, service.GenerateOverride{})
	if !errors.Is(err, repo.ErrRunInProgress) {
		t.Fatalf("Generate over a running run returned %v, want ErrRunInProgress", err)
	}

	if got := p.run().Generation; got != gen {
		t.Errorf("generation moved to %d on a refused start, want %d", got, gen)
	}
	if got := p.segment(0); got.State != model.SegmentDone || got.StagedPath == "" {
		t.Errorf("segment 0 = %q, want the paid-for row untouched by a refused start", got.State)
	}
	if len(p.queued) != queued {
		t.Errorf("a refused start queued %d job(s)", len(p.queued)-queued)
	}

	// Cancel is the way through, and after it the same call succeeds.
	if err := p.svc.Cancel(context.Background(), p.book.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := p.svc.Generate(context.Background(), p.book, service.GenerateOverride{}); err != nil {
		t.Fatalf("Generate after a cancel: %v", err)
	}
	if got := p.run().Generation; got != gen+1 {
		t.Errorf("generation = %d after the regeneration, want %d", got, gen+1)
	}
}

// narrationFile inserts the files row finalize would have created, so
// the → ready write has a real foreign key to point at.
func (p *narrationPipeline) narrationFile() string {
	p.t.Helper()
	f, err := p.files.Insert(context.Background(), model.File{
		LibraryID: p.book.LibraryID,
		BookID:    p.book.ID,
		Location:  "dune.mp3",
		Size:      int64(len(audiotest.Frames(4))),
		Mtime:     time.Now().UTC(),
		Format:    "MP3",
	})
	if err != nil {
		p.t.Fatalf("insert narration file: %v", err)
	}
	return f.ID
}
