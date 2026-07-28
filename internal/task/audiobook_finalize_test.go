// SPDX-License-Identifier: AGPL-3.0-or-later

package task_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
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

func (f *fakeFinalizeRuns) SetReady(_ context.Context, _, fileID string, totalMS int64) error {
	f.ready = &readyWrite{FileID: fileID, TotalMS: totalMS}
	return nil
}

type fakeFiles struct {
	existing  *model.File
	inserted  []model.File
	hashSets  int
	nextID    string
	lookupErr error
}

func (f *fakeFiles) GetByLocation(context.Context, string, string) (model.File, error) {
	if f.existing != nil {
		return *f.existing, nil
	}
	if f.lookupErr != nil {
		return model.File{}, f.lookupErr
	}
	return model.File{}, repo.ErrNotFound
}

func (f *fakeFiles) SetContentHash(context.Context, string, []byte, int64, time.Time) error {
	f.hashSets++
	return nil
}

func (f *fakeFiles) Insert(_ context.Context, file model.File) (model.File, error) {
	if f.nextID != "" {
		file.ID = f.nextID
	}
	f.inserted = append(f.inserted, file)
	return file, nil
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

type finalizeHarness struct {
	deps      task.FinalizeDeps
	runs      *fakeFinalizeRuns
	books     *fakeBooks
	files     *fakeFiles
	dataPath  string
	published int
	placed    int
	placeErr  error
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
		}},
		files:    &fakeFiles{nextID: "file-1"},
		dataPath: t.TempDir(),
	}

	dir := task.StagingDir(h.dataPath, "b1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create staging: %v", err)
	}
	for seq := range 2 {
		path := filepath.Join(dir, "seg-"+strconv.Itoa(seq)+".mp3")
		if err := os.WriteFile(path, mp3Frames(4), 0o600); err != nil {
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
		Files: h.files,
		Place: func(_ context.Context, _ model.Book, _ string) (service.PlaceResult, error) {
			h.placed++
			if h.placeErr != nil {
				return service.PlaceResult{}, h.placeErr
			}
			return service.PlaceResult{
				Location: "Dune/Dune.mp3", Size: 1234, Mtime: time.Unix(0, 0).UTC(),
			}, nil
		},
		Publish:  func(string) { h.published++ },
		DataPath: h.dataPath,
	}
	return h
}

func (h *finalizeHarness) run() error {
	return task.AudiobookFinalize(context.Background(), task.AudiobookFinalizeArgs{BookID: "b1"}, h.deps)
}

func (h *finalizeHarness) stagingExists() bool {
	_, err := os.Stat(task.StagingDir(h.dataPath, "b1"))
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
	if len(h.files.inserted) != 0 {
		t.Errorf("inserted %d files rows for a canceled run", len(h.files.inserted))
	}
}

func TestFinalizeIsANoOpForAReadyRun(t *testing.T) {
	h := newFinalizeHarness(t)
	h.runs.run.State = model.AudiobookReady

	if err := h.run(); err != nil {
		t.Fatalf("AudiobookFinalize: %v", err)
	}
	if h.placed != 0 || h.published != 0 {
		t.Errorf("placed=%d published=%d, want neither on a finished run", h.placed, h.published)
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
	if h.placed != 0 {
		t.Errorf("placed %d times with a segment still outstanding", h.placed)
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
// paid-for segment has to survive a failed finalize.
func TestFinalizeRetainsStagingWhenPlacementFails(t *testing.T) {
	h := newFinalizeHarness(t)
	h.placeErr = errors.New("backend refused the upload")

	if err := h.run(); err != nil {
		t.Fatalf("AudiobookFinalize returned %v, want nil so River stops", err)
	}
	if len(h.runs.states) != 1 || h.runs.states[0].State != model.AudiobookFailed {
		t.Fatalf("states = %+v, want one failed write", h.runs.states)
	}
	if !h.stagingExists() {
		t.Error("a failed finalize reclaimed the staging — Retry would have to buy it again")
	}
	if h.published != 1 {
		t.Errorf("published %d times, want exactly one terminal event", h.published)
	}
}

// ---------------------------------------------------------------------------
// Completion
// ---------------------------------------------------------------------------

func TestFinalizePublishesTheNarrationAndSweeps(t *testing.T) {
	h := newFinalizeHarness(t)

	if err := h.run(); err != nil {
		t.Fatalf("AudiobookFinalize: %v", err)
	}
	if len(h.files.inserted) != 1 {
		t.Fatalf("inserted %d files rows, want 1", len(h.files.inserted))
	}
	got := h.files.inserted[0]
	if got.Format != "MP3" {
		t.Errorf("format = %q, want MP3", got.Format)
	}
	if len(got.ContentHash) == 0 {
		t.Error("content_hash is empty — the rename safety net a library scan runs is blind to it")
	}
	if h.runs.ready == nil {
		t.Fatal("the run was never marked ready")
	}
	if h.runs.ready.FileID != "file-1" {
		t.Errorf("file id = %q, want the inserted row's", h.runs.ready.FileID)
	}
	if h.runs.ready.TotalMS <= 0 {
		t.Errorf("total = %dms, want it measured from the frames", h.runs.ready.TotalMS)
	}
	if h.books.audio == nil || h.books.audio.DurationSeconds == nil {
		t.Fatal("the book row's duration was never written")
	}
	if len(h.books.audio.Chapters) != 2 {
		t.Errorf("wrote %d chapters, want one per chapter index", len(h.books.audio.Chapters))
	}
	if h.books.audio.Narrator != "" {
		t.Errorf("narrator = %q — it means what the file's tags said, and a synthesized voice is not that",
			h.books.audio.Narrator)
	}
	if h.stagingExists() {
		t.Error("staging survived a successful finalize")
	}
	if h.published != 1 {
		t.Errorf("published %d times, want exactly 1", h.published)
	}
	if len(h.runs.starts) != 2 {
		t.Errorf("recorded %d segment starts, want the alignment map completed", len(h.runs.starts))
	}
}

// Regeneration lands on the same key. Inserting unconditionally would
// violate files' UNIQUE(library_id, location), and on a backend it would
// leave the old row pointing at bytes the new upload overwrote.
func TestFinalizeUpdatesTheRowAPreviousRenditionLeft(t *testing.T) {
	h := newFinalizeHarness(t)
	h.files.existing = &model.File{ID: "old-file", LibraryID: "lib1", BookID: "b1", Location: "Dune/Dune.mp3"}

	if err := h.run(); err != nil {
		t.Fatalf("AudiobookFinalize: %v", err)
	}
	if len(h.files.inserted) != 0 {
		t.Errorf("inserted %d rows, want the existing one reused", len(h.files.inserted))
	}
	if h.files.hashSets != 1 {
		t.Errorf("set the hash %d times, want 1", h.files.hashSets)
	}
	if h.runs.ready == nil || h.runs.ready.FileID != "old-file" {
		t.Errorf("ready points at %+v, want the reused row", h.runs.ready)
	}
}

// A narration without embedded art is still a good narration.
func TestFinalizeSucceedsWithNoCoverSource(t *testing.T) {
	h := newFinalizeHarness(t)
	h.deps.Cover = nil
	h.books.book.HasCover = true

	if err := h.run(); err != nil {
		t.Fatalf("AudiobookFinalize: %v", err)
	}
	if h.runs.ready == nil {
		t.Error("the run was not marked ready without a cover source")
	}
}
