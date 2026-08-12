// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/repo"
)

// RenditionRequestRows is the tracking-row slice a request drives: the
// row goes pending before the enqueue, and a refused enqueue is
// recorded on it. Nil on an artifact that tracks no row (the reading
// guide) — then a request is just the enqueue.
type RenditionRequestRows interface {
	Start(ctx context.Context, bookID string) error
	MarkFailed(ctx context.Context, bookID, msg string) error
}

// RenditionRequests owns "ask for a rendition" (#317): the
// Start-before-Enqueue ordering and the compensation on a refused
// enqueue, previously restated in three tiers — the generate endpoint,
// the queue registry's feed closure, and the bulk ConversionRunner —
// none of which compensated, so a queue refusal left a phantom pending
// row nothing would ever move.
type RenditionRequests struct {
	rows       RenditionRequestRows
	enq        jobs.Enqueuer
	args       func(bookID string) jobs.Args
	candidates func(ctx context.Context) ([]string, error)
}

// markdownRequestRows adds the bulk candidate query to the request
// slice — what a bulk run may convert stays the query's decision.
type markdownRequestRows interface {
	RenditionRequestRows
	ListConversionCandidates(ctx context.Context) ([]repo.ConversionCandidate, error)
}

// NewMarkdownRequests binds the markdown artifact's assembly — its rows,
// its job args, its bulk candidates — in one place, so the registry and
// the handler cannot pair the rows with the wrong job.
func NewMarkdownRequests(rows markdownRequestRows, enq jobs.Enqueuer) *RenditionRequests {
	return &RenditionRequests{
		rows: rows,
		enq:  enq,
		args: func(bookID string) jobs.Args { return jobs.MarkdownRenditionArgs{BookID: bookID} },
		candidates: func(ctx context.Context) ([]string, error) {
			cands, err := rows.ListConversionCandidates(ctx)
			if err != nil {
				return nil, err
			}
			ids := make([]string, 0, len(cands))
			for _, c := range cands {
				ids = append(ids, c.BookID)
			}
			return ids, nil
		},
	}
}

// NewEpubRequests binds the generated-EPUB artifact's assembly. No bulk:
// the settings card runs the markdown stage; EPUBs are per-book.
func NewEpubRequests(rows RenditionRequestRows, enq jobs.Enqueuer) *RenditionRequests {
	return &RenditionRequests{
		rows: rows,
		enq:  enq,
		args: func(bookID string) jobs.Args { return jobs.EpubRenderArgs{BookID: bookID} },
	}
}

// One asks for one book's rendition: the row goes pending first, so the
// status poll has an answer the instant the button is pressed; a
// refused enqueue lands on the row rather than leaving a phantom
// pending. The compensation is best effort — the caller hears the
// refusal either way, and a compensation that also failed is logged,
// not substituted.
func (r *RenditionRequests) One(ctx context.Context, bookID string) error {
	if r.rows != nil {
		if err := r.rows.Start(ctx, bookID); err != nil {
			return fmt.Errorf("start rendition row for %s: %w", bookID, err)
		}
	}
	if err := r.enq.Enqueue(ctx, r.args(bookID)); err != nil {
		if r.rows != nil {
			if ferr := r.rows.MarkFailed(ctx, bookID, "could not queue the job: "+err.Error()); ferr != nil {
				slog.Warn("rendition request: record refused enqueue", "book", bookID, "err", ferr)
			}
		}
		return fmt.Errorf("queue rendition for %s: %w", bookID, err)
	}
	return nil
}

// Bulk asks for every candidate, One per row.
//
// Returns the count queued so far alongside the error when the queue
// refuses partway: those jobs are already running, and reporting zero
// would misrepresent what the admin's click did.
func (r *RenditionRequests) Bulk(ctx context.Context) (int, error) {
	if r.candidates == nil {
		return 0, fmt.Errorf("bulk requests are not supported for this artifact")
	}
	ids, err := r.candidates(ctx)
	if err != nil {
		return 0, fmt.Errorf("list candidates: %w", err)
	}
	queued := 0
	for _, id := range ids {
		if err := r.One(ctx, id); err != nil {
			return queued, err
		}
		queued++
	}
	return queued, nil
}
