// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"fmt"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/repo"
)

// conversionCandidateStore is the slice of BookMarkdownRenditionRepo the
// runner touches. What a bulk run may convert is the candidate query's
// decision, not this package's — same split as the guide runner.
type conversionCandidateStore interface {
	ListConversionCandidates(ctx context.Context) ([]repo.ConversionCandidate, error)
	CountConversionCoverage(ctx context.Context) (repo.ConversionCoverage, error)
	Start(ctx context.Context, bookID string) error
}

// ConversionRunner starts and sizes bulk Markdown conversion from the
// converter settings card (ADR-0033). Unlike the guide runner there is
// no token estimate — conversion costs sidecar CPU, not a metered API —
// so the pre-flight is just the coverage counts.
type ConversionRunner struct {
	renditions conversionCandidateStore
	enq        jobs.Enqueuer
}

func NewConversionRunner(renditions conversionCandidateStore, enq jobs.Enqueuer) *ConversionRunner {
	return &ConversionRunner{renditions: renditions, enq: enq}
}

// Coverage answers the settings card's numbers — the same call serves
// the pre-flight ("N books would convert") and the progress poll.
func (r *ConversionRunner) Coverage(ctx context.Context) (repo.ConversionCoverage, error) {
	return r.renditions.CountConversionCoverage(ctx)
}

// Start queues one conversion per candidate and reports how many went.
// Each row goes pending before its enqueue, so the coverage poll counts
// the whole run as converting from the first answer.
//
// Returns the count queued so far alongside the error when the queue
// refuses partway: those jobs are already running, and reporting zero
// would misrepresent what the admin's click did.
func (r *ConversionRunner) Start(ctx context.Context) (int, error) {
	candidates, err := r.renditions.ListConversionCandidates(ctx)
	if err != nil {
		return 0, fmt.Errorf("list conversion candidates: %w", err)
	}
	queued := 0
	for _, c := range candidates {
		if err := r.renditions.Start(ctx, c.BookID); err != nil {
			return queued, fmt.Errorf("start rendition row for %s: %w", c.BookID, err)
		}
		if err := r.enq.Enqueue(ctx, jobs.MarkdownRenditionArgs{BookID: c.BookID}); err != nil {
			return queued, fmt.Errorf("queue conversion for %s: %w", c.BookID, err)
		}
		queued++
	}
	return queued, nil
}
