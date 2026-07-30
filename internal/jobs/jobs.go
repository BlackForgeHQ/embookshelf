// SPDX-License-Identifier: AGPL-3.0-or-later

// Package jobs is the vocabulary both the service tier and the queue
// tier speak: what a job payload is, and what it means to enqueue one.
//
// It exists to be a leaf. internal/queue imports internal/service, so
// the service tier cannot name a queue client — which is why four
// separate modules each grew their own function-typed dispatcher and
// the same comment explaining it. Declaring the seam somewhere both
// tiers can depend on turns those into one ordinary argument.
package jobs

import (
	"context"
	"errors"
)

// ErrDoNotRetry marks a failure that no amount of retrying improves, so
// the queue tier stops the job instead of working through its attempts.
//
// Declared here because a worker cannot say this any other way: the task
// packages deliberately do not import River, so a sentinel in the leaf
// both tiers already speak is what lets internal/queue turn the claim
// into a JobCancel. Before it existed, ErrAudiobooksDisabled and
// ErrReadingGuidesDisabled each carried a comment asserting River treated
// them as permanent — "a disabled feature will still be disabled in
// thirty seconds" — and nothing implemented it (#185).
//
// Wrap it only for a genuinely closed outcome. An error that a retry
// could plausibly clear is River's to keep.
var ErrDoNotRetry = errors.New("job will not succeed on retry")

// Args is one job's payload: a JSON-serializable struct that names its
// own kind.
//
// The kind is a stored value, not a derived one — River persists it
// alongside the encoded args — so renaming a Go type here is safe and
// changing a Kind() string orphans every in-flight job of that type.
type Args interface {
	Kind() string
}

// Queued is the optional half of Args: a job that names a queue runs
// there instead of the default one. Declared as an interface rather
// than a field so the name travels with the payload exactly as Kind
// does.
type Queued interface {
	Queue() string
}

// Enqueuer hands a job to the worker pool.
//
// One method for every job type: the kind travels with the payload, so
// adding a job does not widen this interface.
type Enqueuer interface {
	Enqueue(ctx context.Context, args Args) error
}

// AudiobookQueue is the River queue narration runs on.
//
// Its own queue rather than the default one because a run is tens of
// long jobs per book: sharing the four default workers would stall
// BookDrop ingest and Library scan for as long as the run lasts
// (ADR-0028 §3).
const AudiobookQueue = "audiobook"

// BookDropIngestArgs addresses one staged upload.
type BookDropIngestArgs struct {
	ItemID string `json:"item_id"`
}

// Kind is the job name. Must be stable — changing it orphans in-flight
// jobs.
func (BookDropIngestArgs) Kind() string { return "bookdrop.ingest" }

// BookDropAutoEnrichArgs is the payload for the gap-fill Approve
// requests once a book leaves BookDrop. BookID only — the worker
// re-reads the row, so an edit made between approve and dispatch reaches
// the providers rather than a snapshot taken at enqueue time.
type BookDropAutoEnrichArgs struct {
	BookID string `json:"book_id"`
}

// Kind is the stable job name. Must not change — renaming it orphans
// in-flight jobs.
func (BookDropAutoEnrichArgs) Kind() string { return "bookdrop.auto_enrich" }

// LibraryScanArgs is the payload for walking a library's filesystem
// root. The library id also names the scan — each library owns
// exactly one path since migration 000018.
type LibraryScanArgs struct {
	LibraryID string `json:"library_id"`
}

func (LibraryScanArgs) Kind() string { return "library.scan" }

// SendToKindleArgs is the payload for one Send-to-Kindle delivery.
// BookID + UserID are the only inputs — Notifier re-fetches both
// rows so a stale snapshot can't ship the wrong attachment.
type SendToKindleArgs struct {
	BookID string `json:"book_id"`
	UserID string `json:"user_id"`
}

// Kind is the stable job name.
func (SendToKindleArgs) Kind() string { return "kindle.send" }

// ReadingGuideArgs is the payload for generating one book's guide.
// BookID only — the worker re-reads the row, so a metadata edit between
// enqueue and dispatch is reflected rather than baked into the payload.
type ReadingGuideArgs struct {
	BookID string `json:"book_id"`
}

// Kind is the stable job name.
func (ReadingGuideArgs) Kind() string { return "guide.generate" }

// AudiobookSegmentArgs addresses one unit of synthesis.
//
// Book and seq rather than the segment's own id, because that pair is
// what the plan is keyed on and what a Retry re-enqueues; carrying a row
// id would mean a retry could address a row a regeneration has replaced.
//
// Generation says *which* plan, because book-and-seq alone has the same
// property one level up: a regeneration wipes the plan and installs
// another, and sequence 12 of each is one address. The run's two segment
// writes refuse a generation that is not the run's, so a job left over
// from a superseded plan is a no-op rather than a result landing in
// somebody else's row (ADR-0031).
type AudiobookSegmentArgs struct {
	BookID string `json:"book_id"`
	Seq    int    `json:"seq"`
	// Generation is deliberately allowed to be absent. A job enqueued
	// before this field existed has no generation in its args, so Go
	// decodes the zero value — and zero matches a run row still at the
	// column's default, which is a run nothing has restarted since the
	// deploy, so that job genuinely is current. The first start after the
	// deploy bumps the row to 1 and every genuinely stale job goes quiet.
	// TestAJobEnqueuedBeforeGenerationsExistedStillClaimsItsSegment holds
	// this, because it rests on a zero value and would otherwise look like
	// an oversight.
	Generation int `json:"generation"`
}

func (AudiobookSegmentArgs) Kind() string  { return "audiobook.segment" }
func (AudiobookSegmentArgs) Queue() string { return AudiobookQueue }

// AudiobookFinalizeArgs addresses the concatenation of a finished run.
type AudiobookFinalizeArgs struct {
	BookID string `json:"book_id"`
}

func (AudiobookFinalizeArgs) Kind() string  { return "audiobook.finalize" }
func (AudiobookFinalizeArgs) Queue() string { return AudiobookQueue }
