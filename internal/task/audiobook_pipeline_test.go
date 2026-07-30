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
	"path/filepath"
	"strconv"
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
	t        *testing.T
	db       *db.DB
	runs     *repo.BookAudiobookRepo
	files    *repo.FileRepo
	svc      *service.AudiobookService
	engine   *scriptedEngine
	deps     task.SegmentDeps
	book     model.Book
	dataPath config.DataRoot
	epub     []byte

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
		t:        t,
		db:       d,
		runs:     repo.NewBookAudiobookRepo(d),
		files:    repo.NewFileRepo(d),
		engine:   &scriptedEngine{},
		book:     book,
		dataPath: dataRoot(t),
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
		Runs:     p.runs,
		Advance:  p.svc,
		Books:    books,
		Open:     pipelineOpener{p}.Open,
		Publish:  func(string) { p.workerPublishes++ },
		DataPath: p.dataPath,
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
}

// runSegment executes one segment job with the engine currently
// scripted, and returns what the worker handed back to River.
func (p *narrationPipeline) runSegment(seq int, reply func(tts.Request) ([]byte, error)) error {
	p.t.Helper()
	p.engine.reply = reply
	return task.AudiobookSegment(context.Background(),
		jobs.AudiobookSegmentArgs{BookID: p.book.ID, Seq: seq}, p.deps)
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
	dir, err := task.StagingDir(p.dataPath, p.book.ID)
	if err != nil {
		p.t.Fatalf("StagingDir: %v", err)
	}
	_, err = os.Stat(filepath.Join(dir, "seg-"+strconv.Itoa(seq)+".mp3"))
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
	n, err := task.SweepAudiobookStaging(context.Background(), p.runs, p.dataPath)
	if err != nil {
		p.t.Fatalf("SweepAudiobookStaging: %v", err)
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
