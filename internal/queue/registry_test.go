// SPDX-License-Identifier: AGPL-3.0-or-later

package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/riverqueue/river"

	"github.com/blackforge/embookshelf/internal/jobs"
)

// probeArgs is a stand-in job type — the registry must work for any
// jobs.Args, not just the three this binary ships.
type probeArgs struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func (probeArgs) Kind() string { return "test.probe" }

func TestRegisterExposesTheArgsKind(t *testing.T) {
	t.Parallel()

	reg := register(func(context.Context, probeArgs) error { return nil })
	if reg.kind != "test.probe" {
		t.Fatalf("kind = %q, want test.probe", reg.kind)
	}
}

func TestRegisterProducesARiverWorker(t *testing.T) {
	t.Parallel()

	reg := register(func(context.Context, probeArgs) error { return nil })
	if reg.addToRiver == nil {
		t.Fatal("registration has no River worker builder")
	}
}

// The registry is the single place job types are declared. Every kind
// the binary ships must appear exactly once — a duplicate would mean two
// workers racing for the same jobs, and a missing one means jobs enqueue
// and never run.
func TestRegistryCoversEveryJobKindExactlyOnce(t *testing.T) {
	t.Parallel()

	want := []string{
		jobs.BookDropIngestArgs{}.Kind(),
		jobs.BookDropAutoEnrichArgs{}.Kind(),
		jobs.LibraryScanArgs{}.Kind(),
		jobs.SendToKindleArgs{}.Kind(),
		jobs.ReadingGuideArgs{}.Kind(),
		jobs.AudiobookSegmentArgs{}.Kind(),
		jobs.AudiobookFinalizeArgs{}.Kind(),
	}

	seen := map[string]int{}
	for _, reg := range registry(Deps{}) {
		seen[reg.kind]++
		if reg.addToRiver == nil {
			t.Errorf("kind %q registered without a River worker", reg.kind)
		}
	}

	for _, kind := range want {
		switch seen[kind] {
		case 1:
		case 0:
			t.Errorf("kind %q missing from the registry", kind)
		default:
			t.Errorf("kind %q registered %d times, want exactly 1", kind, seen[kind])
		}
	}
	if len(seen) != len(want) {
		t.Errorf("registry has %d kinds, want %d — a job was added without a test", len(seen), len(want))
	}
}

// A job routed to a queue the client does not poll sits forever with no
// error anywhere, so the queue a job declares has to be the queue the
// registry reports for it.
func TestRegistryRecordsEachJobsQueue(t *testing.T) {
	t.Parallel()

	want := map[string]string{
		jobs.BookDropIngestArgs{}.Kind():     "default",
		jobs.BookDropAutoEnrichArgs{}.Kind(): "default",
		jobs.LibraryScanArgs{}.Kind():        "default",
		jobs.SendToKindleArgs{}.Kind():       "default",
		jobs.ReadingGuideArgs{}.Kind():       "default",
		jobs.AudiobookSegmentArgs{}.Kind():   jobs.AudiobookQueue,
		jobs.AudiobookFinalizeArgs{}.Kind():  jobs.AudiobookQueue,
	}

	for _, reg := range registry(Deps{}) {
		if got := want[reg.kind]; got != reg.queue {
			t.Errorf("kind %q runs on queue %q, want %q", reg.kind, reg.queue, got)
		}
	}
}

// A worker that says its failure is permanent must actually stop River
// retrying it. ErrAudiobooksDisabled and ErrReadingGuidesDisabled both
// carried a comment claiming River treated them as permanent — "a
// disabled feature will still be disabled in thirty seconds" — while
// nothing in the tree mapped either to a cancel, so they were retried
// twenty-five times like any other error. The claim is now the
// mechanism: jobs.ErrDoNotRetry becomes a River JobCancelError (#185).
func TestRiverWorkerCancelsAJobThatWillNeverSucceed(t *testing.T) {
	t.Parallel()

	w := &riverWorker[jobs.AudiobookSegmentArgs]{
		work: func(context.Context, jobs.AudiobookSegmentArgs) error {
			return fmt.Errorf("audiobook generation is not enabled: %w", jobs.ErrDoNotRetry)
		},
	}

	err := w.Work(context.Background(), &river.Job[jobs.AudiobookSegmentArgs]{})

	var cancel *river.JobCancelError
	if !errors.As(err, &cancel) {
		t.Fatalf("err = %#v, want a river.JobCancelError so the job is not retried", err)
	}
	if !strings.Contains(err.Error(), "not enabled") {
		t.Errorf("err = %q, want it to keep the reason the work function gave", err.Error())
	}
}

// The ordinary case stays ordinary: a transient failure is River's to
// retry, and wrapping every error as a cancel would silently disable
// retries for the whole queue.
func TestRiverWorkerLeavesAnOrdinaryFailureRetryable(t *testing.T) {
	t.Parallel()

	w := &riverWorker[jobs.AudiobookSegmentArgs]{
		work: func(context.Context, jobs.AudiobookSegmentArgs) error {
			return errors.New("connection reset")
		},
	}

	err := w.Work(context.Background(), &river.Job[jobs.AudiobookSegmentArgs]{})

	var cancel *river.JobCancelError
	if errors.As(err, &cancel) {
		t.Fatalf("err = %v was turned into a cancel — River would never retry a transient failure", err)
	}
}
