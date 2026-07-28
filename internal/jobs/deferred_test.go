// SPDX-License-Identifier: AGPL-3.0-or-later

package jobs_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/blackforge/embookshelf/internal/jobs"
)

// probeArgs is a payload for exercising the seam itself.
type probeArgs struct {
	ID string `json:"id"`
}

func (probeArgs) Kind() string { return "test.probe" }

// recorder is an Enqueuer that keeps what it was handed.
type recorder struct {
	mu  sync.Mutex
	got []jobs.Args
	err error
}

func (r *recorder) Enqueue(_ context.Context, args jobs.Args) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.got = append(r.got, args)
	return nil
}

func (r *recorder) seen() []jobs.Args {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]jobs.Args(nil), r.got...)
}

// An unresolved Deferred refuses rather than dropping. A job that
// cannot be queued is a real failure the caller has to see — the old
// nil-holder returned silently, which is how a finished audiobook run
// lost its finalize job and sat at running forever.
func TestDeferredRefusesBeforeItIsResolved(t *testing.T) {
	var d jobs.Deferred

	err := d.Enqueue(context.Background(), probeArgs{ID: "a"})

	if !errors.Is(err, jobs.ErrNoQueue) {
		t.Fatalf("err = %v, want ErrNoQueue", err)
	}
}

func TestDeferredPassesThePayloadThroughOnceResolved(t *testing.T) {
	var d jobs.Deferred
	rec := &recorder{}
	d.Resolve(rec)

	if err := d.Enqueue(context.Background(), probeArgs{ID: "a"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	got := rec.seen()
	if len(got) != 1 {
		t.Fatalf("enqueued %d jobs, want 1", len(got))
	}
	p, ok := got[0].(probeArgs)
	if !ok {
		t.Fatalf("got %T, want the payload unchanged", got[0])
	}
	if p.ID != "a" {
		t.Errorf("id = %q, want a", p.ID)
	}
}

func TestDeferredSurfacesTheInnerError(t *testing.T) {
	var d jobs.Deferred
	sentinel := errors.New("queue down")
	d.Resolve(&recorder{err: sentinel})

	if err := d.Enqueue(context.Background(), probeArgs{}); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the inner error surfaced", err)
	}
}

// The regression pin for the race this refactor exists to close.
//
// Once queue.Client.Start has been called, worker goroutines read the
// resolved enqueuer concurrently with any call to Resolve. The old
// holder was a plain struct field written on one goroutine and read on
// others with no synchronization at all.
//
// This test must fail under -race against a mutex-free Deferred.
func TestDeferredResolvesSafelyWhileEnqueuesAreInFlight(t *testing.T) {
	var d jobs.Deferred
	rec := &recorder{}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for range 100 {
			// Either outcome is legitimate — before Resolve it refuses,
			// after it succeeds. What must not happen is a data race.
			_ = d.Enqueue(context.Background(), probeArgs{ID: "a"})
		}
	}()
	go func() {
		defer wg.Done()
		d.Resolve(rec)
	}()

	wg.Wait()
}
