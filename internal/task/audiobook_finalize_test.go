// SPDX-License-Identifier: AGPL-3.0-or-later

package task_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/audio"
	"github.com/blackforge/embookshelf/internal/audio/audiotest"
	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/task"
)

// ---------------------------------------------------------------------------
// Fakes local to finalize
// ---------------------------------------------------------------------------

type stateWrite struct {
	State model.AudiobookState
	Msg   string
}

type fakeFinalizeRuns struct {
	run      model.Audiobook
	segments []model.AudiobookSegment
	states   []stateWrite
	starts   map[int]int64
	ready    *readyWrite
}

type readyWrite struct {
	FileID  string
	TotalMS int64
}

func (f *fakeFinalizeRuns) GetByBookID(context.Context, string) (model.Audiobook, error) {
	return f.run, nil
}

func (f *fakeFinalizeRuns) ListSegments(context.Context, string) ([]model.AudiobookSegment, error) {
	return f.segments, nil
}

func (f *fakeFinalizeRuns) SetSegmentStart(_ context.Context, _ string, seq int, startMS int64) error {
	if f.starts == nil {
		f.starts = map[int]int64{}
	}
	f.starts[seq] = startMS
	return nil
}

func (f *fakeFinalizeRuns) SetState(_ context.Context, _ string, state model.AudiobookState, msg string) error {
	f.states = append(f.states, stateWrite{State: state, Msg: msg})
	return nil
}

// NarrationAssembled stands in for AudiobookService: the worker reports
// that it has the file and the service decides the state (#210).
func (f *fakeFinalizeRuns) NarrationAssembled(_ context.Context, _, fileID string, totalMS int64) error {
	f.ready = &readyWrite{FileID: fileID, TotalMS: totalMS}
	return nil
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

type finalizeHarness struct {
	deps      task.FinalizeDeps
	runs      *fakeFinalizeRuns
	books     *fakeBooks
	staging   task.Staging
	published int
	recorded  int
	recordErr error
}

// newFinalizeHarness stages two done segments on disk, which is the
// state a run is in when the last segment dispatches finalize.
func newFinalizeHarness(t *testing.T) *finalizeHarness {
	t.Helper()
	h := &finalizeHarness{
		runs: &fakeFinalizeRuns{
			run: model.Audiobook{BookID: "b1", State: model.AudiobookRunning, Engine: "openai"},
		},
		books: &fakeBooks{book: model.Book{
			ID: "b1", LibraryID: "lib1", Title: "Dune", Author: "Frank Herbert", Format: "EPUB",
			Narrator: "Ada Lovelace",
		}},
		staging: tempStaging(t),
	}

	dir := stagingDir(t, h.staging, "b1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create staging: %v", err)
	}
	for seq := range 2 {
		path := filepath.Join(dir, "seg-"+strconv.Itoa(seq)+".mp3")
		if err := os.WriteFile(path, audiotest.Frames(4), 0o600); err != nil {
			t.Fatalf("stage segment %d: %v", seq, err)
		}
		h.runs.segments = append(h.runs.segments, model.AudiobookSegment{
			BookID: "b1", Seq: seq, ChapterIndex: seq, ChapterTitle: "Chapter",
			State: model.SegmentDone, StagedPath: path, DurationMS: 100,
		})
	}

	h.deps = task.FinalizeDeps{
		Runs:  h.runs,
		Books: h.books,
		Record: func(_ context.Context, _ model.Book, srcPath string) (service.DerivedRecord, error) {
			h.recorded++
			if h.recordErr != nil {
				return service.DerivedRecord{}, h.recordErr
			}
			// Recording consumes the source file in production (placement
			// is inside it); the fake mirrors that.
			_ = os.Remove(srcPath)
			return service.DerivedRecord{
				FileID: "file-1", Location: "Dune/Dune.mp3", Size: 1234, Mtime: time.Unix(0, 0).UTC(),
			}, nil
		},
		// The one module that marks a run failed also publishes, so the
		// worker no longer does either itself (#190).
		Fail: func(_ context.Context, _ string, msg string) error {
			h.runs.states = append(h.runs.states, stateWrite{State: model.AudiobookFailed, Msg: msg})
			h.published++
			return nil
		},
		Report:  h.runs,
		Staging: h.staging,
	}
	return h
}

func (h *finalizeHarness) run() error {
	return task.AudiobookFinalize(context.Background(), jobs.AudiobookFinalizeArgs{BookID: "b1"}, h.deps)
}

func (h *finalizeHarness) stagingExists() bool {
	dir, err := h.staging.Dir("b1")
	if err != nil {
		return false
	}
	_, err = os.Stat(dir)
	return err == nil
}

// ---------------------------------------------------------------------------
// Refusals
// ---------------------------------------------------------------------------

// Cancelled between the last segment landing and this job being picked
// up. Sweep rather than publish — a cancel means the user does not want
// the partial, and here the partial happens to be complete.
func TestFinalizeSweepsACanceledRunWithoutPublishing(t *testing.T) {
	h := newFinalizeHarness(t)
	h.runs.run.State = model.AudiobookCanceled

	if err := h.run(); err != nil {
		t.Fatalf("AudiobookFinalize: %v", err)
	}
	if h.stagingExists() {
		t.Error("a canceled run's staging survived")
	}
	if h.published != 0 {
		t.Errorf("published %d times for a canceled run", h.published)
	}
	if h.recorded != 0 {
		t.Errorf("recorded %d files for a canceled run", h.recorded)
	}
}

func TestFinalizeIsANoOpForAReadyRun(t *testing.T) {
	h := newFinalizeHarness(t)
	h.runs.run.State = model.AudiobookReady

	if err := h.run(); err != nil {
		t.Fatalf("AudiobookFinalize: %v", err)
	}
	if h.recorded != 0 || h.published != 0 {
		t.Errorf("recorded=%d published=%d, want neither on a finished run", h.recorded, h.published)
	}
}

// Reached before the run actually finished. Not an error — the segment
// that completes last dispatches this again.
func TestFinalizeDefersWhileASegmentIsOutstanding(t *testing.T) {
	h := newFinalizeHarness(t)
	h.runs.segments[1].State = model.SegmentPending

	if err := h.run(); err != nil {
		t.Fatalf("AudiobookFinalize: %v", err)
	}
	if h.recorded != 0 {
		t.Errorf("recorded %d times with a segment still outstanding", h.recorded)
	}
	if h.published != 0 {
		t.Errorf("published %d times before the run finished", h.published)
	}
	if !h.stagingExists() {
		t.Error("staging was swept while a segment was still outstanding")
	}
}

func TestFinalizeFailsARunWithNoSegments(t *testing.T) {
	h := newFinalizeHarness(t)
	h.runs.segments = nil

	if err := h.run(); err != nil {
		t.Fatalf("AudiobookFinalize returned %v, want nil so River stops", err)
	}
	if len(h.runs.states) != 1 || h.runs.states[0].State != model.AudiobookFailed {
		t.Fatalf("states = %+v, want one failed write", h.runs.states)
	}
}

// ---------------------------------------------------------------------------
// ADR-0028 §6 — failure keeps the paid-for work
// ---------------------------------------------------------------------------

// Retry re-enqueues only the segments that never finished, so every
// paid-for segment has to survive a failed finalize. The refusal can be
// the placement or the files row — both are inside Record now, and both
// fail the run the same way, naming the step.
func TestFinalizeRetainsStagingWhenRecordingFails(t *testing.T) {
	h := newFinalizeHarness(t)
	h.recordErr = errors.New("backend refused the upload")

	if err := h.run(); err != nil {
		t.Fatalf("AudiobookFinalize returned %v, want nil so River stops", err)
	}
	if len(h.runs.states) != 1 || h.runs.states[0].State != model.AudiobookFailed {
		t.Fatalf("states = %+v, want one failed write", h.runs.states)
	}
	if !strings.Contains(h.runs.states[0].Msg, "record narration") {
		t.Errorf("failure message = %q, want it to name the failing step", h.runs.states[0].Msg)
	}
	if !h.stagingExists() {
		t.Error("a failed finalize reclaimed the staging — Retry would have to buy it again")
	}
	if h.recorded != 1 {
		t.Errorf("recorded %d times, want exactly one attempt before the failure", h.recorded)
	}
	if h.published != 1 {
		t.Errorf("published %d times, want exactly one terminal event", h.published)
	}
}

// ---------------------------------------------------------------------------
// Completion
// ---------------------------------------------------------------------------

// The worker reports the assembled file and sweeps. It does not publish
// and does not decide the state: the run service does both, from the one
// guarded write, so a run cancelled mid-assembly is neither marked ready
// nor announced (#210).
func TestFinalizeReportsTheNarrationAndSweeps(t *testing.T) {
	h := newFinalizeHarness(t)

	if err := h.run(); err != nil {
		t.Fatalf("AudiobookFinalize: %v", err)
	}
	// The files row's shape — format, content hash, update-don't-duplicate
	// — is RecordDerived's, pinned by the service tests (#307). What is
	// this worker's: one record of the assembled file, and the run marked
	// ready with the id it answered.
	if h.recorded != 1 {
		t.Fatalf("recorded %d times, want 1", h.recorded)
	}
	if h.runs.ready == nil {
		t.Fatal("the run was never marked ready")
	}
	if h.runs.ready.FileID != "file-1" {
		t.Errorf("file id = %q, want the recorded row's", h.runs.ready.FileID)
	}
	_, perSegMS, err := audio.Payload(audiotest.Frames(4))
	if err != nil {
		t.Fatalf("measure fixture duration: %v", err)
	}
	if want := perSegMS * 2; h.runs.ready.TotalMS != want {
		t.Errorf("total = %dms, want %dms measured from the frames, not the stored 100ms-per-segment stand-in",
			h.runs.ready.TotalMS, want)
	}
	if h.books.audio == nil || h.books.audio.DurationSeconds == nil {
		t.Fatal("the book row's duration was never written")
	}
	if len(h.books.audio.Chapters) != 2 {
		t.Errorf("wrote %d chapters, want one per chapter index", len(h.books.audio.Chapters))
	}
	if h.books.audio.Narrator != h.books.book.Narrator {
		t.Errorf("narrator = %q, want %q passed through unchanged — it means what the file's tags said, "+
			"and a synthesized voice is not that", h.books.audio.Narrator, h.books.book.Narrator)
	}
	if h.stagingExists() {
		t.Error("staging survived a successful finalize")
	}
	// Not the worker's: the service publishes from the write that moved
	// the run, so a cancelled run gets neither the state nor the event.
	if h.published != 0 {
		t.Errorf("published %d times from the worker, want none — the transition emits it (#210)", h.published)
	}
	if got, want := h.runs.starts[0], int64(0); got != want {
		t.Errorf("segment 0 starts at %dms, want %d", got, want)
	}
	if h.runs.starts[1] <= 0 {
		t.Errorf("segment 1 starts at %dms, want it measured after segment 0's duration", h.runs.starts[1])
	}
}

// A narration without embedded art is still a good narration.
func TestFinalizeSucceedsWithNoCoverSource(t *testing.T) {
	h := newFinalizeHarness(t)
	h.deps.Cover = nil // already nil on the harness; naming it here is the point under test
	h.books.book.HasCover = true

	if err := h.run(); err != nil {
		t.Fatalf("AudiobookFinalize: %v", err)
	}
	if h.runs.ready == nil {
		t.Error("the run was not marked ready without a cover source")
	}
}

// A supplied cover is not just read without erroring — it has to actually
// land in the finished file's tag, or a narration with HasCover true would
// silently ship art-less anyway.
func TestFinalizeEmbedsTheSuppliedCover(t *testing.T) {
	h := newFinalizeHarness(t)
	h.books.book.HasCover = true
	h.books.book.CoverMime = "image/jpeg"
	const coverBytes = "cover-bytes-marker"
	h.deps.Cover = func(model.Book) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(coverBytes)), nil
	}

	var tag []byte
	h.deps.Record = func(_ context.Context, _ model.Book, srcPath string) (service.DerivedRecord, error) {
		h.recorded++
		b, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatalf("read assembled file: %v", err)
		}
		tag = b
		_ = os.Remove(srcPath)
		return service.DerivedRecord{
			FileID: "file-1", Location: "Dune/Dune.mp3", Size: 1234, Mtime: time.Unix(0, 0).UTC(),
		}, nil
	}

	if err := h.run(); err != nil {
		t.Fatalf("AudiobookFinalize: %v", err)
	}
	if !bytes.Contains(tag, []byte("APIC")) || !bytes.Contains(tag, []byte(coverBytes)) {
		t.Error("the cover never reached the finished file's ID3 tag")
	}
}
