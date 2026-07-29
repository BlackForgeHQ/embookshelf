// SPDX-License-Identifier: AGPL-3.0-or-later

package task_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/blackforge/embookshelf/internal/audio/audiotest"
	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/task"
	"github.com/blackforge/embookshelf/internal/tts"
)

// recordingEnqueuer captures the jobs a segment worker hands to the
// pool, so a test can count finalize dispatches without a real queue.
type recordingEnqueuer struct {
	mu   sync.Mutex
	args []jobs.Args
}

func (r *recordingEnqueuer) Enqueue(_ context.Context, a jobs.Args) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.args = append(r.args, a)
	return nil
}

// finalizes returns the BookID of every finalize job queued. A count
// alone would pass a finalize dispatched for the wrong book; the payload
// this whole seam exists to carry is the point of the assertion.
func (r *recordingEnqueuer) finalizes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ids []string
	for _, a := range r.args {
		if f, ok := a.(jobs.AudiobookFinalizeArgs); ok {
			ids = append(ids, f.BookID)
		}
	}
	return ids
}

// ---------------------------------------------------------------------------
// Fakes local to the segment worker
// ---------------------------------------------------------------------------

// fakeSegmentRuns is the slice of the run store the segment worker uses.
//
// onGet exists for one case the worker was built around: cancel is
// re-read before every engine call, so a test proving a mid-run cancel
// stops the spend has to change the answer between reads.
type fakeSegmentRuns struct {
	run model.Audiobook
	// plan is what the run was started with, the rows the planner wrote.
	// A worker verifies its re-extraction against these, so a test can
	// move one to stand for a book edited under a live run.
	plan     []model.AudiobookSegment
	getErr   error
	gets     int
	onGet    func(n int) model.Audiobook
	claim    bool
	claimErr error
	claims   int
	recorded []model.SegmentResult
	outcome  model.AudiobookNext
}

func (f *fakeSegmentRuns) GetByBookID(context.Context, string) (model.Audiobook, error) {
	f.gets++
	if f.getErr != nil {
		return model.Audiobook{}, f.getErr
	}
	if f.onGet != nil {
		return f.onGet(f.gets), nil
	}
	return f.run, nil
}

func (f *fakeSegmentRuns) GetSegment(_ context.Context, _ string, seq int) (model.AudiobookSegment, error) {
	if seq < 0 || seq >= len(f.plan) {
		return model.AudiobookSegment{}, repo.ErrNotFound
	}
	return f.plan[seq], nil
}

func (f *fakeSegmentRuns) MarkSegmentRunning(context.Context, string, int) (bool, error) {
	f.claims++
	return f.claim, f.claimErr
}

func (f *fakeSegmentRuns) RecordSegment(
	_ context.Context, _ string, _ int, res model.SegmentResult,
) (model.AudiobookOutcome, error) {
	f.recorded = append(f.recorded, res)
	return model.AudiobookOutcome{Next: f.outcome}, nil
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

type segmentHarness struct {
	deps      task.SegmentDeps
	runs      *fakeSegmentRuns
	books     *fakeBooks
	engine    *fakeEngine
	enq       *recordingEnqueuer
	published int
	engineErr error
	dataPath  string
}

// harnessSegmentChars is the cap the harness's run was planned at. Large
// enough that the fixture's chapters are one segment each.
const harnessSegmentChars = 1000

// planSegments builds the rows the planner would have written, through
// the same module the worker re-extracts with — which is the point: a
// plan assembled by hand here would not catch the two sides drifting.
func planSegments(t *testing.T, src storage.Source, maxChars int) []model.AudiobookSegment {
	t.Helper()
	segs, err := service.SegmentBook(context.Background(), src, maxChars)
	if err != nil {
		t.Fatalf("SegmentBook: %v", err)
	}
	out := make([]model.AudiobookSegment, 0, len(segs))
	for _, s := range segs {
		out = append(out, model.AudiobookSegment{
			BookID: "b1", Seq: s.Seq, ChapterIndex: s.ChapterIndex, ChapterTitle: s.ChapterTitle,
			CharStart: s.CharStart, CharEnd: s.CharEnd, State: model.SegmentPending,
		})
	}
	return out
}

// newSegmentHarness wires a worker whose every collaborator is in
// process: a claimable run, an EPUB of two short chapters, and an engine
// that returns four frames of silence.
func newSegmentHarness(t *testing.T) *segmentHarness {
	t.Helper()
	h := &segmentHarness{
		runs: &fakeSegmentRuns{
			run: model.Audiobook{
				BookID: "b1", State: model.AudiobookRunning, Engine: "openai", Voice: "alloy",
				SegmentChars: harnessSegmentChars,
			},
			claim: true,
		},
		books: &fakeBooks{book: model.Book{
			ID: "b1", LibraryID: "lib1", Title: "Dune", Author: "Frank Herbert", Format: "EPUB",
		}},
		engine:   &fakeEngine{reply: audiotest.Frames(4)},
		enq:      &recordingEnqueuer{},
		dataPath: t.TempDir(),
	}
	src := epubWithChapters(t, "One sentence. Another sentence. A third one.", "Second chapter here.")
	h.runs.plan = planSegments(t, src, harnessSegmentChars)
	h.deps = task.SegmentDeps{
		// No SegmentChars: the split comes from the run's own pinned cap,
		// and a worker that reached for this one would produce a segment
		// the plan never described.
		Config: func(context.Context) (repo.AudiobookConfig, error) {
			return repo.AudiobookConfig{Enabled: true, Engine: "openai"}, nil
		},
		Engine: func(repo.AudiobookConfig) (repo.ConfiguredEngine, error) {
			if h.engineErr != nil {
				return repo.ConfiguredEngine{}, h.engineErr
			}
			return repo.ConfiguredEngine{
				ID:     tts.EngineOpenAI,
				Engine: h.engine,
			}, nil
		},
		Runs:  h.runs,
		Books: h.books,
		Open: func(context.Context, model.Book) (storage.Source, error) {
			return src, nil
		},
		Enqueue:  h.enq,
		Publish:  func(string) { h.published++ },
		DataPath: h.dataPath,
	}
	return h
}

func (h *segmentHarness) run(t *testing.T, seq int) error {
	t.Helper()
	return task.AudiobookSegment(context.Background(), jobs.AudiobookSegmentArgs{BookID: "b1", Seq: seq}, h.deps)
}

func (h *segmentHarness) staged(seq int) bool {
	_, err := os.Stat(filepath.Join(task.StagingDir(h.dataPath, "b1"), "seg-"+strconv.Itoa(seq)+".mp3"))
	return err == nil
}

// ---------------------------------------------------------------------------
// Refusals — nothing is bought
// ---------------------------------------------------------------------------

// A disabled feature is permanent, not transient: it will still be
// disabled in thirty seconds. The worker returns the sentinel and never
// reaches the engine — what River does with the error is River's own
// retry policy, not this worker's concern.
func TestSegmentRefusesWhenTheFeatureIsDisabled(t *testing.T) {
	h := newSegmentHarness(t)
	h.deps.Config = func(context.Context) (repo.AudiobookConfig, error) {
		return repo.AudiobookConfig{Enabled: false}, nil
	}

	err := h.run(t, 0)

	if !errors.Is(err, task.ErrAudiobooksDisabled) {
		t.Fatalf("err = %v, want ErrAudiobooksDisabled", err)
	}
	if h.engine.calls != 0 {
		t.Errorf("engine called %d times with the feature off", h.engine.calls)
	}
}

// A store error reading the run is a load failure, not a narration
// outcome — nothing has been claimed yet, so there is nothing to record
// it against.
func TestSegmentSurfacesARunLoadFailure(t *testing.T) {
	h := newSegmentHarness(t)
	h.runs.getErr = errors.New("db unavailable")

	err := h.run(t, 0)

	if err == nil {
		t.Fatal("AudiobookSegment returned nil for a run the store could not load")
	}
	if !strings.Contains(err.Error(), "b1") {
		t.Errorf("err = %q, want it to name the book", err.Error())
	}
	if h.runs.claims != 0 || h.engine.calls != 0 {
		t.Errorf("claims=%d engine=%d, want neither before the run even loaded", h.runs.claims, h.engine.calls)
	}
}

// Cancel is the only stop-loss on a run that may be a hundred and
// seventy dollars. A segment picked up after one must not claim.
func TestSegmentSkipsACanceledRunWithoutClaiming(t *testing.T) {
	h := newSegmentHarness(t)
	h.runs.run.State = model.AudiobookCanceled

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment: %v", err)
	}
	if h.runs.claims != 0 {
		t.Errorf("claimed %d times on a canceled run", h.runs.claims)
	}
	if h.engine.calls != 0 {
		t.Errorf("engine called %d times on a canceled run", h.engine.calls)
	}
}

func TestSegmentSkipsARunThatIsAlreadyReady(t *testing.T) {
	h := newSegmentHarness(t)
	h.runs.run.State = model.AudiobookReady

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment: %v", err)
	}
	if h.runs.claims != 0 || h.engine.calls != 0 {
		t.Errorf("claims=%d engine=%d, want neither on a finished run", h.runs.claims, h.engine.calls)
	}
}

// A segment that lost the claim is a segment somebody else finished.
// Re-synthesizing would buy the same audio twice — the whole reason
// segments are rows rather than a counter (ADR-0028 §6).
func TestSegmentDoesNotResynthesizeWhatItCouldNotClaim(t *testing.T) {
	h := newSegmentHarness(t)
	h.runs.claim = false

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment: %v", err)
	}
	if h.engine.calls != 0 {
		t.Errorf("engine called %d times for an unclaimed segment", h.engine.calls)
	}
}

// A claim error is the store's own failure, distinct from losing the
// claim race (claimed=false, nil error): River has to retry a store that
// could not even attempt the claim, not treat it as someone else having
// finished the segment first.
func TestSegmentSurfacesAClaimFailure(t *testing.T) {
	h := newSegmentHarness(t)
	h.runs.claimErr = errors.New("advisory lock unavailable")

	err := h.run(t, 0)

	if err == nil {
		t.Fatal("AudiobookSegment returned nil for a claim the store could not attempt")
	}
	if h.engine.calls != 0 {
		t.Errorf("engine called %d times despite the claim failing", h.engine.calls)
	}
}

// ---------------------------------------------------------------------------
// The run's own choice wins over the current setting
// ---------------------------------------------------------------------------

// An admin switching engines mid-run must not produce a book narrated
// half in one voice and half in another.
func TestSegmentRefusesWhenTheRunUsesADifferentEngine(t *testing.T) {
	h := newSegmentHarness(t)
	h.runs.run.Engine = "elevenlabs"

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment returned %v, want nil for a permanent failure", err)
	}
	if h.engine.calls != 0 {
		t.Errorf("engine called %d times despite the engine mismatch", h.engine.calls)
	}
	if len(h.runs.recorded) != 1 || h.runs.recorded[0].State != model.SegmentFailed {
		t.Fatalf("recorded %+v, want one failed segment", h.runs.recorded)
	}
}

func TestSegmentRefusesABookThatIsNotNarratable(t *testing.T) {
	h := newSegmentHarness(t)
	h.books.book.Format = "PDF"

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment returned %v, want nil for a permanent failure", err)
	}
	if len(h.runs.recorded) != 1 || h.runs.recorded[0].State != model.SegmentFailed {
		t.Fatalf("recorded %+v, want one failed segment", h.runs.recorded)
	}
	if h.engine.calls != 0 {
		t.Errorf("engine called %d times for a PDF", h.engine.calls)
	}
}

func TestSegmentSurfacesAMissingBook(t *testing.T) {
	h := newSegmentHarness(t)
	h.books.err = repo.ErrNotFound

	err := h.run(t, 0)

	if err == nil {
		t.Fatal("AudiobookSegment returned nil for a deleted book")
	}
	if !strings.Contains(err.Error(), "b1") {
		t.Errorf("err = %q, want it to name the book that vanished", err.Error())
	}
	if errors.Is(err, tts.ErrPermanent) || errors.Is(err, service.ErrNotNarratable) {
		t.Errorf("err = %v classified as permanent — a missing book is a load failure, not a narration refusal", err)
	}
	if h.staged(0) {
		t.Error("a segment was staged for a book that no longer exists")
	}
}

// ---------------------------------------------------------------------------
// Engine outcomes
// ---------------------------------------------------------------------------

// deps.Engine failing — a bad key, an unknown engine id, anything that
// keeps the engine from being built at all — is a different permanent
// route from the run/current-engine mismatch above: there the engine
// builds fine and the run's own choice loses; here there is no engine to
// choose between.
func TestSegmentTreatsAnEngineBuildFailureAsPermanent(t *testing.T) {
	h := newSegmentHarness(t)
	h.engineErr = errors.New("no api key configured")

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment returned %v, want nil for a permanent failure", err)
	}
	if len(h.runs.recorded) != 1 || h.runs.recorded[0].State != model.SegmentFailed {
		t.Fatalf("recorded %+v, want one failed segment", h.runs.recorded)
	}
	if h.engine.calls != 0 {
		t.Errorf("engine called %d times when it could not even be built", h.engine.calls)
	}
	if h.published != 1 {
		t.Errorf("published %d times, want exactly one terminal event", h.published)
	}
}

// A permanent engine error stops River retrying something that cannot
// improve; the failed row and its message carry the outcome.
func TestSegmentRecordsAPermanentEngineFailureAndStopsRetrying(t *testing.T) {
	h := newSegmentHarness(t)
	h.engine.err = tts.ErrPermanent

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment returned %v, want nil so River stops", err)
	}
	if len(h.runs.recorded) != 1 || h.runs.recorded[0].State != model.SegmentFailed {
		t.Fatalf("recorded %+v, want one failed segment", h.runs.recorded)
	}
	if h.published != 1 {
		t.Errorf("published %d times, want exactly one terminal event", h.published)
	}
}

// A transient error is River's to retry, so the worker has to return it.
// This is also where the chunk-count asymmetry Task 5 used to pin at this
// layer now lives in spirit: unusable audio tagged permanent at one chunk
// but wrapped untagged, and so retried forever, at several, moved into
// internal/tts with the chunking loop itself (Task 4) and is pinned there
// by TestChunkedJoinWrapsMultiChunkFailureUntagged in
// internal/tts/chunking_test.go (see #185). What survives at this layer
// is this: whatever untagged error the engine reports, for whatever
// reason, the worker returns it for River to retry, records exactly one
// failed segment, and never publishes — it never promotes a bare error
// to permanent on its own say-so.
func TestSegmentReturnsATransientEngineFailureForRiver(t *testing.T) {
	h := newSegmentHarness(t)
	h.engine.err = errors.New("connection reset")

	err := h.run(t, 0)

	if err == nil {
		t.Fatal("AudiobookSegment returned nil for a transient failure — River would never retry")
	}
	if len(h.runs.recorded) != 1 || h.runs.recorded[0].State != model.SegmentFailed {
		t.Fatalf("recorded %+v, want one failed segment", h.runs.recorded)
	}
	if h.published != 0 {
		t.Errorf("published %d times, want zero — this path never reaches the permanent-failure publish", h.published)
	}
}

// Audio the frame parser cannot read is not something a retry improves.
// The worker sees whatever bytes its one Synthesize call returns and
// runs them through its own audio.Payload check — how many engine calls
// (if any) the adapter split that call into internally is invisible
// here, and is internal/tts's business, not the worker's (see
// TestSegmentReturnsATransientEngineFailureForRiver above for the
// sibling case: a failure the engine itself reports, rather than one the
// worker's own decode catches).
func TestSegmentRecordsUnusableAudioAsAPermanentFailure(t *testing.T) {
	h := newSegmentHarness(t)
	h.engine.reply = []byte("this is not an mp3")

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment returned %v, want nil", err)
	}
	if len(h.runs.recorded) != 1 || h.runs.recorded[0].State != model.SegmentFailed {
		t.Fatalf("recorded %+v, want one failed segment", h.runs.recorded)
	}
	if h.published != 1 {
		t.Errorf("published %d times, want exactly one", h.published)
	}
}

// A 40k segment is a dozen engine calls over several minutes. A cancel
// that only took effect between segments would keep spending for most of
// that (ADR-0028 §6), so it travels with the request as BeforeChunk and
// is checked before every simulated piece — chunks: 3 makes the fake
// stand in for whatever count the real adapter would pick.
func TestSegmentStopsSpendingWhenCancelLandsBetweenChunks(t *testing.T) {
	h := newSegmentHarness(t)
	h.engine.chunks = 3
	running := model.Audiobook{BookID: "b1", State: model.AudiobookRunning, Engine: "openai", Voice: "alloy"}
	canceled := running
	canceled.State = model.AudiobookCanceled
	// Reads: 1 is the worker's own state check, 2 guards the first chunk,
	// 3 guards the second. The cancel lands after one chunk was bought.
	h.runs.onGet = func(n int) model.Audiobook {
		if n >= 3 {
			return canceled
		}
		return running
	}

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment returned %v, want nil for an abandoned segment", err)
	}
	if h.engine.calls != 1 {
		t.Errorf("engine called %d times, want the run to stop after the first chunk", h.engine.calls)
	}
	if len(h.runs.recorded) != 0 {
		t.Errorf("recorded %+v — a canceled run is already in its final state and must not be overwritten",
			h.runs.recorded)
	}
	if h.staged(0) {
		t.Error("an abandoned segment was staged")
	}
}

// The plan and the file disagreeing means the file changed under the
// run. Narrating segment 12 of a different book is worse than failing.
func TestSegmentRefusesASeqThePlanNoLongerHas(t *testing.T) {
	h := newSegmentHarness(t)

	if err := h.run(t, 99); err != nil {
		t.Fatalf("AudiobookSegment returned %v, want nil for a permanent failure", err)
	}
	if len(h.runs.recorded) != 1 || h.runs.recorded[0].State != model.SegmentFailed {
		t.Fatalf("recorded %+v, want one failed segment", h.runs.recorded)
	}
	if h.engine.calls != 0 {
		t.Errorf("engine called %d times for a segment that does not exist", h.engine.calls)
	}
}

// The cap the run was planned with wins over the live settings row, the
// same way its engine and voice do. An admin editing the setting while a
// run is in flight used to re-split the book for every remaining segment
// and fail all of them permanently — after the money for the earlier ones
// was already spent (#189).
func TestSegmentSplitsAtTheRunsOwnCapNotTheLiveSetting(t *testing.T) {
	h := newSegmentHarness(t)
	h.deps.Config = func(context.Context) (repo.AudiobookConfig, error) {
		return repo.AudiobookConfig{Enabled: true, Engine: "openai", SegmentChars: 20}, nil
	}

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment: %v", err)
	}

	for _, rec := range h.runs.recorded {
		if rec.State == model.SegmentFailed {
			t.Fatalf("the segment failed after the cap was edited mid-run: %s", rec.Error)
		}
	}
	if h.engine.calls == 0 {
		t.Fatal("engine was never called")
	}
	if got := h.engine.requests[0].Text; !strings.Contains(got, "A third one.") {
		t.Errorf("engine got %q, want the whole first chapter the run planned", got)
	}
}

// The verification a count comparison could not make: a book re-uploaded
// with a paragraph added keeps its segment count and moves every offset
// after the edit. The stored character range is what notices, and the
// message has to name the drift — an operator told "count mismatch" would
// go looking for a chapter that is still there.
func TestSegmentRefusesASegmentWhoseTextMovedUnderTheRun(t *testing.T) {
	h := newSegmentHarness(t)
	h.runs.plan[0].CharStart += 7
	h.runs.plan[0].CharEnd += 7

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment returned %v, want nil for a permanent failure", err)
	}
	if len(h.runs.recorded) != 1 || h.runs.recorded[0].State != model.SegmentFailed {
		t.Fatalf("recorded %+v, want one failed segment", h.runs.recorded)
	}
	if msg := h.runs.recorded[0].Error; !strings.Contains(msg, "planned") {
		t.Errorf("failure says %q, want it to name the range that drifted", msg)
	}
	if h.engine.calls != 0 {
		t.Errorf("engine called %d times for text the run never planned", h.engine.calls)
	}
}

// A book that lost a chapter under a live run still has the plan row the
// job addresses — the plan is in Postgres, the chapter was in the file.
// Re-extraction is where that is noticed, and narrating segment 12 of a
// different book is worse than failing.
func TestSegmentRefusesASegmentTheFileNoLongerHas(t *testing.T) {
	h := newSegmentHarness(t)
	shorter := epubWithChapters(t, "One sentence. Another sentence. A third one.")
	h.deps.Open = func(context.Context, model.Book) (storage.Source, error) { return shorter, nil }

	if err := h.run(t, 1); err != nil {
		t.Fatalf("AudiobookSegment returned %v, want nil for a permanent failure", err)
	}
	if len(h.runs.recorded) != 1 || h.runs.recorded[0].State != model.SegmentFailed {
		t.Fatalf("recorded %+v, want one failed segment", h.runs.recorded)
	}
	if h.engine.calls != 0 {
		t.Errorf("engine called %d times for a chapter the file no longer has", h.engine.calls)
	}
}

// ---------------------------------------------------------------------------
// Completion
// ---------------------------------------------------------------------------

func TestSegmentStagesItsAudioAndRecordsTheDuration(t *testing.T) {
	h := newSegmentHarness(t)
	h.runs.run.Model = "tts-1"

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment: %v", err)
	}
	if len(h.runs.recorded) != 1 {
		t.Fatalf("recorded %d results, want 1", len(h.runs.recorded))
	}
	got := h.runs.recorded[0]
	if got.State != model.SegmentDone {
		t.Errorf("state = %q, want done", got.State)
	}
	if got.DurationMS <= 0 {
		t.Errorf("duration = %dms, want it measured from the frames", got.DurationMS)
	}
	if !h.staged(0) {
		t.Errorf("no file at %s", got.StagedPath)
	}
	// The run pins its own voice and model rather than deferring to
	// whatever is currently configured — that pin is worthless if it
	// never actually reaches the engine call.
	if len(h.engine.requests) == 0 {
		t.Fatal("engine was never called")
	}
	for i, req := range h.engine.requests {
		if req.Voice != h.runs.run.Voice || req.Model != h.runs.run.Model {
			t.Errorf("request %d voice/model = %q/%q, want the run's own %q/%q",
				i, req.Voice, req.Model, h.runs.run.Voice, h.runs.run.Model)
		}
	}
}

// The segment that completes last is what turns a run into a book. A
// dispatch that fires twice would assemble the same file twice.
func TestSegmentDispatchesFinalizeExactlyOnceWhenTheRunCompletes(t *testing.T) {
	h := newSegmentHarness(t)
	h.runs.outcome = model.AudiobookNextFinalize

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment: %v", err)
	}
	if ids := h.enq.finalizes(); len(ids) != 1 || ids[0] != h.runs.run.BookID {
		t.Fatalf("finalize dispatched %v, want exactly one for %q", ids, h.runs.run.BookID)
	}
}

// A run the write decided has failed publishes so the UI stops polling.
func TestSegmentPublishesOnceWhenTheWriteFailsTheRun(t *testing.T) {
	h := newSegmentHarness(t)
	h.runs.outcome = model.AudiobookNextFail

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment: %v", err)
	}
	if h.published != 1 {
		t.Fatalf("published %d times, want exactly 1", h.published)
	}
	if ids := h.enq.finalizes(); len(ids) != 0 {
		t.Errorf("finalize dispatched %v for a failed run", ids)
	}
}
