// SPDX-License-Identifier: AGPL-3.0-or-later

package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// requestRows records the Start / MarkFailed traffic One drives.
type requestRows struct {
	started    []string
	startErr   error
	failed     map[string]string // bookID → recorded message
	markErr    error
	candidates []repo.ConversionCandidate
	listErr    error
}

func (r *requestRows) Start(_ context.Context, bookID string) error {
	if r.startErr != nil {
		return r.startErr
	}
	r.started = append(r.started, bookID)
	return nil
}

func (r *requestRows) MarkFailed(_ context.Context, bookID, msg string) error {
	if r.markErr != nil {
		return r.markErr
	}
	if r.failed == nil {
		r.failed = map[string]string{}
	}
	r.failed[bookID] = msg
	return nil
}

func (r *requestRows) ListConversionCandidates(context.Context) ([]repo.ConversionCandidate, error) {
	return r.candidates, r.listErr
}

// countingEnqueuer refuses from the Nth call on (0 = never).
type countingEnqueuer struct {
	calls   []jobs.Args
	failOn  int // 1-based call number that starts refusing; 0 = never
	failErr error
}

func (e *countingEnqueuer) Enqueue(_ context.Context, a jobs.Args) error {
	if e.failOn > 0 && len(e.calls)+1 >= e.failOn {
		return e.failErr
	}
	e.calls = append(e.calls, a)
	return nil
}

// TestRenditionRequestsOneStartsThenEnqueues — the ordering invariant:
// the row goes pending before the enqueue, so the status poll has an
// answer the instant the button is pressed.
func TestRenditionRequestsOneStartsThenEnqueues(t *testing.T) {
	rows := &requestRows{}
	enq := &countingEnqueuer{}
	r := service.NewMarkdownRequests(rows, enq)

	if err := r.One(context.Background(), "b1"); err != nil {
		t.Fatalf("One: %v", err)
	}
	if len(rows.started) != 1 || rows.started[0] != "b1" {
		t.Fatalf("started = %v, want [b1]", rows.started)
	}
	if len(enq.calls) != 1 {
		t.Fatalf("enqueued %d jobs, want 1", len(enq.calls))
	}
	if args, ok := enq.calls[0].(jobs.MarkdownRenditionArgs); !ok || args.BookID != "b1" {
		t.Fatalf("args = %#v, want MarkdownRenditionArgs for b1", enq.calls[0])
	}
}

// TestRenditionRequestsOneCompensatesARefusedEnqueue — the #317 gap: a
// refused enqueue must land on the row rather than leaving a phantom
// pending nothing will ever move.
func TestRenditionRequestsOneCompensatesARefusedEnqueue(t *testing.T) {
	rows := &requestRows{}
	enq := &countingEnqueuer{failOn: 1, failErr: errors.New("queue is closed")}
	r := service.NewMarkdownRequests(rows, enq)

	err := r.One(context.Background(), "b1")
	if err == nil || !strings.Contains(err.Error(), "queue is closed") {
		t.Fatalf("err = %v, want the refusal surfaced", err)
	}
	msg, ok := rows.failed["b1"]
	if !ok {
		t.Fatal("the refused enqueue left the row pending — the phantom #317 closes")
	}
	if !strings.Contains(msg, "queue is closed") {
		t.Errorf("row message = %q, want it to carry the refusal", msg)
	}
}

// TestRenditionRequestsOneReturnsTheEnqueueErrorWhenCompensationFails —
// the compensation is best effort; the caller still hears the original
// refusal, not the compensation's.
func TestRenditionRequestsOneReturnsTheEnqueueErrorWhenCompensationFails(t *testing.T) {
	rows := &requestRows{markErr: errors.New("db down too")}
	enq := &countingEnqueuer{failOn: 1, failErr: errors.New("queue is closed")}
	r := service.NewMarkdownRequests(rows, enq)

	err := r.One(context.Background(), "b1")
	if err == nil || !strings.Contains(err.Error(), "queue is closed") {
		t.Fatalf("err = %v, want the original refusal", err)
	}
}

// TestRenditionRequestsOneStopsOnAStartFailure — no job for a row that
// never went pending.
func TestRenditionRequestsOneStopsOnAStartFailure(t *testing.T) {
	rows := &requestRows{startErr: errors.New("insert refused")}
	enq := &countingEnqueuer{}
	r := service.NewMarkdownRequests(rows, enq)

	if err := r.One(context.Background(), "b1"); err == nil || !strings.Contains(err.Error(), "insert refused") {
		t.Fatalf("err = %v, want the start failure", err)
	}
	if len(enq.calls) != 0 {
		t.Fatalf("enqueued %d jobs for a row that never went pending", len(enq.calls))
	}
}

// TestRenditionRequestsBulkQueuesEveryCandidate — the bulk shape over
// One: each candidate goes pending before its enqueue.
func TestRenditionRequestsBulkQueuesEveryCandidate(t *testing.T) {
	rows := &requestRows{candidates: []repo.ConversionCandidate{
		{BookID: "b1"}, {BookID: "b2"}, {BookID: "b3"},
	}}
	enq := &countingEnqueuer{}
	r := service.NewMarkdownRequests(rows, enq)

	queued, err := r.Bulk(context.Background())
	if err != nil {
		t.Fatalf("Bulk: %v", err)
	}
	if queued != 3 || len(enq.calls) != 3 || len(rows.started) != 3 {
		t.Fatalf("queued=%d enqueued=%d started=%d, want 3 each", queued, len(enq.calls), len(rows.started))
	}
}

// TestRenditionRequestsBulkReportsPartialProgress — jobs already queued
// are running; reporting zero would misrepresent what the click did.
func TestRenditionRequestsBulkReportsPartialProgress(t *testing.T) {
	rows := &requestRows{candidates: []repo.ConversionCandidate{
		{BookID: "b1"}, {BookID: "b2"}, {BookID: "b3"},
	}}
	enq := &countingEnqueuer{failOn: 3, failErr: errors.New("queue is closed")}
	r := service.NewMarkdownRequests(rows, enq)

	queued, err := r.Bulk(context.Background())
	if err == nil {
		t.Fatal("want the refusal surfaced")
	}
	if queued != 2 {
		t.Fatalf("queued = %d, want the 2 that went before the refusal", queued)
	}
	// The book whose enqueue was refused is compensated like any One.
	if _, ok := rows.failed["b3"]; !ok {
		t.Error("the refused candidate's row stayed pending")
	}
}

// TestRenditionRequestsBulkListFailure — nothing queued, error out.
func TestRenditionRequestsBulkListFailure(t *testing.T) {
	rows := &requestRows{listErr: errors.New("db down")}
	r := service.NewMarkdownRequests(rows, &countingEnqueuer{})

	queued, err := r.Bulk(context.Background())
	if err == nil || queued != 0 {
		t.Fatalf("queued=%d err=%v, want 0 and the listing failure", queued, err)
	}
}

// TestEpubRequestsCarryEpubArgs — the artifact-specific assembly (which
// args go with which rows) is the constructor's, stated once.
func TestEpubRequestsCarryEpubArgs(t *testing.T) {
	rows := &requestRows{}
	enq := &countingEnqueuer{}
	r := service.NewEpubRequests(rows, enq)

	if err := r.One(context.Background(), "b9"); err != nil {
		t.Fatalf("One: %v", err)
	}
	if args, ok := enq.calls[0].(jobs.EpubRenderArgs); !ok || args.BookID != "b9" {
		t.Fatalf("args = %#v, want EpubRenderArgs for b9", enq.calls[0])
	}
}
