// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
)

// advanceHarness wires the write side against in-process collaborators.
// The whole point of the module is that a run's transitions can be
// driven without River and without Postgres, so every test here does.
//
// The read side is exercised through Status, which is what production
// calls: the reconcile that used to have its own exported entry point
// reached exactly the same code, and every scenario it covered has a
// Status test of the same name (#214).
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
	h.svc = NewAudiobookService(AudiobookDeps{Store: h.store, Enqueue: h.enq, Publish: func(bookID string) { h.published = append(h.published, bookID) }})
	return h
}

func running(bookID string) model.Audiobook {
	return model.Audiobook{BookID: bookID, State: model.AudiobookRunning}
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
