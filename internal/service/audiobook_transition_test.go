// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
)

// The run's state had five writers and only one of them guarded its
// write. These are the three things that split cost, each of which the
// module named as owning transitions could not express (#210).

// A finalize job already in flight when the user cancels used to mark
// the run ready anyway: SetReady was an unconditional UPDATE, so it
// overwrote canceled with ready and the user was billed for a run they
// stopped, then handed the audiobook.
//
// Not the latent race — the cancel window here is the whole of finalize,
// which assembles hundreds of megabytes.
func TestNarrationAssembledDoesNotResurrectACanceledRun(t *testing.T) {
	t.Parallel()

	run := model.Audiobook{BookID: "b1", State: model.AudiobookCanceled}
	h := newAdvanceHarness(t, run, model.AudiobookCoverage{Total: 3, Done: 3})

	err := h.svc.NarrationAssembled(context.Background(), "b1", "file-1", 90_000)
	if err != nil {
		t.Fatalf("NarrationAssembled: %v", err)
	}

	if h.store.state == model.AudiobookReady {
		t.Error("a canceled run was marked ready by a finalize already in flight (ADR-0028 §6)")
	}
	if len(h.published) != 0 {
		t.Errorf("published %v for a transition that did not happen", h.published)
	}
}

// The write that did move the run publishes, and does so from the module
// that made the move rather than from whoever called it.
func TestNarrationAssembledPublishesTheRunItCompleted(t *testing.T) {
	t.Parallel()

	h := newAdvanceHarness(t, running("b1"), model.AudiobookCoverage{Total: 3, Done: 3})

	if err := h.svc.NarrationAssembled(context.Background(), "b1", "file-1", 90_000); err != nil {
		t.Fatalf("NarrationAssembled: %v", err)
	}

	if h.store.state != model.AudiobookReady {
		t.Fatalf("state = %q, want ready", h.store.state)
	}
	if len(h.published) != 1 {
		t.Errorf("published %d times, want one so open pages stop polling", len(h.published))
	}
}

// Cancel performed the transition and swept staging but did not publish;
// the HTTP handler did, from memory. A second caller of Cancel therefore
// leaves every open page polling a run that has already stopped.
func TestCancelPublishesWithoutTheCallerRememberingTo(t *testing.T) {
	t.Parallel()

	h := newAdvanceHarness(t, running("b1"), model.AudiobookCoverage{Total: 3, Done: 1})

	if err := h.svc.Cancel(context.Background(), "b1"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	if h.store.state != model.AudiobookCanceled {
		t.Fatalf("state = %q, want canceled", h.store.state)
	}
	if len(h.published) != 1 {
		t.Errorf("published %d times, want one from the module that made the transition", len(h.published))
	}
}

// The latent race the issue names: Start dispatched every segment and
// then wrote `running` as its last statement. A segment that landed
// inside that window — and with a warm queue and a short book, one can —
// drove the run forward and the trailing write put it back.
//
// Asserted as an ordering rather than by racing goroutines: the write
// that cannot be undone is the one that happens before the dispatch.
func TestStartWritesRunningBeforeItDispatches(t *testing.T) {
	t.Parallel()

	h := newAdvanceHarness(t, model.Audiobook{}, model.AudiobookCoverage{})
	var order []string
	h.store.onSetState = func(model.AudiobookState) { order = append(order, "state") }
	h.enq.onEnqueue = func() { order = append(order, "dispatch") }

	book := narratableBook()
	svc := h.svc
	svc.d.Books = &epubOpener{src: buildTestEPUB(t, "One sentence here.")}
	if err := svc.Start(context.Background(), book, testOptions()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if len(order) == 0 {
		t.Fatal("neither the state write nor a dispatch was observed")
	}
	if order[0] != "state" {
		t.Errorf("order = %v, want the running write before the first dispatch — "+
			"a segment landing mid-dispatch must not be overwritten", order)
	}
}
