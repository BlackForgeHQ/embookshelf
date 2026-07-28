// SPDX-License-Identifier: AGPL-3.0-or-later

package task_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/task"
	"github.com/blackforge/embookshelf/internal/tts"
)

// ---------------------------------------------------------------------------
// Fakes local to the segment worker
// ---------------------------------------------------------------------------

// fakeSegmentRuns is the slice of the run store the segment worker uses.
//
// onGet exists for one case the worker was built around: cancel is
// re-read before every engine call, so a test proving a mid-run cancel
// stops the spend has to change the answer between reads.
type fakeSegmentRuns struct {
	run      model.Audiobook
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
	deps       task.SegmentDeps
	runs       *fakeSegmentRuns
	books      *fakeBooks
	engine     *fakeEngine
	published  int
	finalized  int
	engineErr  error
	dataPath   string
	configCall int
}

// newSegmentHarness wires a worker whose every collaborator is in
// process: a claimable run, an EPUB of two short chapters, and an engine
// that returns four frames of silence.
func newSegmentHarness(t *testing.T) *segmentHarness {
	t.Helper()
	h := &segmentHarness{
		runs: &fakeSegmentRuns{
			run:   model.Audiobook{BookID: "b1", State: model.AudiobookRunning, Engine: "openai", Voice: "alloy"},
			claim: true,
		},
		books: &fakeBooks{book: model.Book{
			ID: "b1", LibraryID: "lib1", Title: "Dune", Author: "Frank Herbert", Format: "EPUB",
		}},
		engine:   &fakeEngine{reply: mp3Frames(4)},
		dataPath: t.TempDir(),
	}
	src := epubWithChapters(t, "One sentence. Another sentence. A third one.", "Second chapter here.")
	h.deps = task.SegmentDeps{
		Config: func(context.Context) (repo.AudiobookConfig, error) {
			h.configCall++
			return repo.AudiobookConfig{Enabled: true, Engine: "openai", SegmentChars: 1000}, nil
		},
		Engine: func(repo.AudiobookConfig) (repo.ConfiguredEngine, error) {
			if h.engineErr != nil {
				return repo.ConfiguredEngine{}, h.engineErr
			}
			return repo.ConfiguredEngine{
				ID:     tts.EngineOpenAI,
				Info:   tts.Info{ID: tts.EngineOpenAI, MaxRequestChars: 20},
				Engine: h.engine,
			}, nil
		},
		Runs:  h.runs,
		Books: h.books,
		Open: func(context.Context, model.Book) (storage.Source, error) {
			return src, nil
		},
		Finalize: func(context.Context, string) error { h.finalized++; return nil },
		Publish:  func(string) { h.published++ },
		DataPath: h.dataPath,
	}
	return h
}

func (h *segmentHarness) run(t *testing.T, seq int) error {
	t.Helper()
	return task.AudiobookSegment(context.Background(), task.AudiobookSegmentArgs{BookID: "b1", Seq: seq}, h.deps)
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
}

// Audio the frame parser cannot read is not something a retry improves —
// when it is caught. This raises MaxRequestChars so segment 0's 45
// characters stay in one chunk, which routes the bad bytes through
// AudiobookSegment's own audio.Payload check rather than joinParts' (see
// the sibling test below for what happens when they don't fit in one).
func TestSegmentRecordsUnusableAudioAsAPermanentFailure(t *testing.T) {
	h := newSegmentHarness(t)
	h.engine.reply = []byte("this is not an mp3")
	h.deps.Engine = func(repo.AudiobookConfig) (repo.ConfiguredEngine, error) {
		return repo.ConfiguredEngine{
			ID:     tts.EngineOpenAI,
			Info:   tts.Info{ID: tts.EngineOpenAI, MaxRequestChars: 1000},
			Engine: h.engine,
		}, nil
	}

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

// Pins a known inconsistency rather than endorsing it: the harness default
// (MaxRequestChars: 20) splits segment 0 into three chunks, same as every
// real engine would on a full-size segment. Unusable audio there is caught
// by joinParts, not by AudiobookSegment's own check, and joinParts wraps
// the failure as a plain "chunk %d: %w" — untagged with tts.ErrPermanent —
// so the worker returns it and River retries forever, unlike the
// single-chunk case above, which the worker marks permanent and publishes.
// Fixing that gap is out of scope here; this test exists so the gap has to
// be noticed, and broken deliberately, before anyone closes it silently.
func TestSegmentTreatsMultiChunkUnusableAudioAsRetryableUnlikeTheSingleChunkCase(t *testing.T) {
	h := newSegmentHarness(t)
	h.engine.reply = []byte("this is not an mp3")

	err := h.run(t, 0)

	if err == nil {
		t.Fatal("AudiobookSegment returned nil — want the chunk-join error, since it isn't tagged permanent")
	}
	if len(h.runs.recorded) != 1 || h.runs.recorded[0].State != model.SegmentFailed {
		t.Fatalf("recorded %+v, want one failed segment", h.runs.recorded)
	}
	if h.published != 0 {
		t.Errorf("published %d times, want zero — this path never reaches the permanent-failure publish", h.published)
	}
}

// A 40k segment is a dozen engine calls over several minutes. A cancel
// that only took effect between segments would keep spending for most of
// that (ADR-0028 §6), so it is checked before every call.
func TestSegmentStopsSpendingWhenCancelLandsBetweenChunks(t *testing.T) {
	h := newSegmentHarness(t)
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

// ---------------------------------------------------------------------------
// Completion
// ---------------------------------------------------------------------------

func TestSegmentStagesItsAudioAndRecordsTheDuration(t *testing.T) {
	h := newSegmentHarness(t)

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
}

// The segment that completes last is what turns a run into a book. A
// dispatch that fires twice would assemble the same file twice.
func TestSegmentDispatchesFinalizeExactlyOnceWhenTheRunCompletes(t *testing.T) {
	h := newSegmentHarness(t)
	h.runs.outcome = model.AudiobookNextFinalize

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment: %v", err)
	}
	if h.finalized != 1 {
		t.Fatalf("finalize dispatched %d times, want exactly 1", h.finalized)
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
	if h.finalized != 0 {
		t.Errorf("finalize dispatched %d times for a failed run", h.finalized)
	}
}
