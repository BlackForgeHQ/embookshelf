// SPDX-License-Identifier: AGPL-3.0-or-later

package queue

import (
	"context"
	"testing"

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
