// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
)

// advanceHarness wires the advancer against in-process collaborators.
// The whole point of the module is that a run's transitions can be
// driven without River and without Postgres, so every test here does.
type advanceHarness struct {
	store     *fakeAudiobookStore
	enq       *recordingEnqueuer
	svc       *AudiobookService
	published []string
}

func newAdvanceHarness(t *testing.T, run model.Audiobook, cov model.AudiobookCoverage) *advanceHarness {
	t.Helper()
	h := &advanceHarness{
		store: &fakeAudiobookStore{run: run, coverage: cov},
		enq:   &recordingEnqueuer{},
	}
	h.svc = NewAudiobookService(h.store, nil, h.enq).
		WithPublisher(func(bookID string) { h.published = append(h.published, bookID) })
	return h
}

func running(bookID string) model.Audiobook {
	return model.Audiobook{BookID: bookID, State: model.AudiobookRunning}
}

// Every segment landed: the run is one finalize away from a finished
// book, so the transition is a dispatch and nothing else. The state is
// deliberately not moved here — finalize moves it when it has the file.
func TestAdvanceDispatchesFinalizeWhenCoverageIsComplete(t *testing.T) {
	t.Parallel()

	h := newAdvanceHarness(t, running("b1"), model.AudiobookCoverage{Total: 3, Done: 3})

	if err := h.svc.Advance(context.Background(), "b1"); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	if got := h.enq.finalizes(); len(got) != 1 || got[0] != "b1" {
		t.Errorf("finalizes = %v, want one for b1", got)
	}
	if h.store.state != "" {
		t.Errorf("run moved to %q — finalize owns that transition, once it has the file", h.store.state)
	}
}

// Everything has settled and some of it failed. One writer, one message.
func TestAdvanceFailsTheRunWhenSegmentsSettleWithFailures(t *testing.T) {
	t.Parallel()

	cov := model.AudiobookCoverage{Total: 3, Done: 2, Failed: 1}
	h := newAdvanceHarness(t, running("b1"), cov)

	if err := h.svc.Advance(context.Background(), "b1"); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	if h.store.state != model.AudiobookFailed {
		t.Errorf("run state = %q, want failed", h.store.state)
	}
	if h.store.stateMsg != cov.FailureMessage() {
		t.Errorf("failure message = %q, want Coverage's own %q — the run's message is built one way",
			h.store.stateMsg, cov.FailureMessage())
	}
	if len(h.enq.finalizes()) != 0 {
		t.Error("a failed run dispatched finalize")
	}
	if len(h.published) != 1 {
		t.Errorf("published %d times, want one so the UI stops polling", len(h.published))
	}
}

// Segments still outstanding: nothing happens, and in particular nothing
// is enqueued. A finalize dispatched here would assemble a part-book.
func TestAdvanceLeavesARunWithOutstandingSegmentsAlone(t *testing.T) {
	t.Parallel()

	h := newAdvanceHarness(t, running("b1"), model.AudiobookCoverage{Total: 3, Done: 1})

	if err := h.svc.Advance(context.Background(), "b1"); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	if len(h.enq.finalizes()) != 0 || h.store.state != "" || len(h.published) != 0 {
		t.Errorf("advance acted on an unfinished run: finalizes=%v state=%q published=%v",
			h.enq.finalizes(), h.store.state, h.published)
	}
}

// Cancel is a decision a user made. Coverage saying "complete" must not
// resurrect it into a finished audiobook (ADR-0028 §6).
func TestAdvanceDoesNotResurrectACanceledRun(t *testing.T) {
	t.Parallel()

	run := running("b1")
	run.State = model.AudiobookCanceled
	h := newAdvanceHarness(t, run, model.AudiobookCoverage{Total: 3, Done: 3})

	if err := h.svc.Advance(context.Background(), "b1"); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	if len(h.enq.finalizes()) != 0 || h.store.state != "" {
		t.Errorf("a canceled run was advanced: finalizes=%v state=%q", h.enq.finalizes(), h.store.state)
	}
}

// The queue being down costs the dispatch, not the caller. A segment
// worker that had already staged and paid for its audio must not be
// handed an error that makes River retry it.
func TestAdvanceReportsADispatchFailureWithoutLosingTheWrite(t *testing.T) {
	t.Parallel()

	h := newAdvanceHarness(t, running("b1"), model.AudiobookCoverage{Total: 1, Done: 1})
	h.enq.err = errors.New("queue is down")

	err := h.svc.Advance(context.Background(), "b1")

	if err == nil {
		t.Fatal("Advance hid a dispatch failure")
	}
	if !errors.Is(err, h.enq.err) {
		t.Errorf("err = %v, want the queue's own error", err)
	}
}

// The segment write and the transition are one operation: the repo takes
// the run's row lock, writes the segment, reads Coverage under that lock
// and hands back what follows. The advancer applies it. Two workers
// landing at once therefore cannot both see themselves as last.
func TestAdvanceAfterSegmentAppliesWhatTheWriteDecided(t *testing.T) {
	t.Parallel()

	h := newAdvanceHarness(t, running("b1"), model.AudiobookCoverage{})
	h.store.outcome = model.AudiobookOutcome{
		Coverage: model.AudiobookCoverage{Total: 2, Done: 2},
		Next:     model.AudiobookNextFinalize,
	}

	res := model.SegmentResult{State: model.SegmentDone, StagedPath: "/tmp/seg-1.mp3", DurationMS: 1000}
	if err := h.svc.AdvanceAfterSegment(context.Background(), "b1", 1, res); err != nil {
		t.Fatalf("AdvanceAfterSegment: %v", err)
	}

	if len(h.store.recorded) != 1 || h.store.recorded[0].StagedPath != "/tmp/seg-1.mp3" {
		t.Fatalf("recorded %+v, want the segment result as given", h.store.recorded)
	}
	if got := h.enq.finalizes(); len(got) != 1 {
		t.Errorf("finalizes = %v, want the one the write's outcome asked for", got)
	}
	// The run's state is not re-read: the outcome came from under the
	// lock, and a second read could see a different one.
	if h.store.gets != 0 {
		t.Errorf("read the run %d times after a write that already decided", h.store.gets)
	}
}

// A failure recorded by the last outstanding segment fails the run
// through the same writer as the read path, with the same message.
func TestAdvanceAfterSegmentFailsThroughTheSameWriter(t *testing.T) {
	t.Parallel()

	cov := model.AudiobookCoverage{Total: 2, Done: 1, Failed: 1}
	h := newAdvanceHarness(t, running("b1"), model.AudiobookCoverage{})
	h.store.outcome = model.AudiobookOutcome{Coverage: cov, Next: model.AudiobookNextFail}

	err := h.svc.AdvanceAfterSegment(context.Background(), "b1", 1,
		model.SegmentResult{State: model.SegmentFailed, Error: "engine refused"})
	if err != nil {
		t.Fatalf("AdvanceAfterSegment: %v", err)
	}

	if h.store.state != model.AudiobookFailed || h.store.stateMsg != cov.FailureMessage() {
		t.Errorf("run failed as (%q, %q), want (failed, %q)", h.store.state, h.store.stateMsg, cov.FailureMessage())
	}
	if len(h.published) != 1 {
		t.Errorf("published %d times, want one", len(h.published))
	}
}
