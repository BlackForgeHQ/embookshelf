// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
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
	if err := h.svc.AdvanceAfterSegment(context.Background(), "b1", 1, 1, res); err != nil {
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

// replannedStore is a store whose segment write refuses everything: the
// repo's answer for a result addressed to a plan that is not the one that
// exists — a seq the run does not have, or a generation a regeneration
// has moved past. Only RecordSegment differs from the fake the other
// tests use, so a test can assert what the advancer does with the refusal
// and nothing else.
type replannedStore struct {
	*fakeAudiobookStore
}

func (s *replannedStore) RecordSegment(
	context.Context, string, int, int, model.SegmentResult,
) (model.AudiobookOutcome, error) {
	return model.AudiobookOutcome{}, repo.ErrNotFound
}

// A refused segment write is permanent and decides nothing. The outcome
// it comes back with is the zero one, and advancing on that would read
// "0 of 0 segments" as a finished run and dispatch finalize for a book
// with no audio — so the refusal has to stop the advance outright, with
// the sentinel intact for a caller that has to classify it (#220).
func TestAdvanceAfterSegmentDerivesNothingFromARefusedWrite(t *testing.T) {
	t.Parallel()

	h := newAdvanceHarness(t, running("b1"), model.AudiobookCoverage{})
	h.svc.d.Store = &replannedStore{fakeAudiobookStore: h.store}

	err := h.svc.AdvanceAfterSegment(context.Background(), "b1", 9, 1,
		model.SegmentResult{State: model.SegmentDone, StagedPath: "/tmp/seg-9.mp3", DurationMS: 1000})
	if !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound to survive to the caller", err)
	}
	if got := h.enq.finalizes(); len(got) != 0 {
		t.Errorf("finalizes = %v, want none — the write decided nothing", got)
	}
	if h.store.state != "" {
		t.Errorf("run moved to %q, want it left where it was", h.store.state)
	}
	if len(h.published) != 0 {
		t.Errorf("published %d times, want none", len(h.published))
	}
}

// A failure recorded by the last outstanding segment fails the run
// through the same writer as the read path, with the same message.
func TestAdvanceAfterSegmentFailsThroughTheSameWriter(t *testing.T) {
	t.Parallel()

	cov := model.AudiobookCoverage{Total: 2, Done: 1, Failed: 1}
	h := newAdvanceHarness(t, running("b1"), model.AudiobookCoverage{})
	h.store.outcome = model.AudiobookOutcome{Coverage: cov, Next: model.AudiobookNextFail}

	err := h.svc.AdvanceAfterSegment(context.Background(), "b1", 1, 1,
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
