// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/blackforge/embookshelf/internal/repo"
)

// guideCandidateLister is the slice of BookReadingGuideRepo the runner
// reads. The exclusion of hand-edited guides lives in that query, not
// here — one place decides what a run may overwrite.
type guideCandidateLister interface {
	ListGuideCandidates(ctx context.Context) ([]repo.GuideCandidate, error)
}

// GuideDispatcher hands one book to the worker pool. A function rather
// than a queue.Client because internal/queue imports this package.
type GuideDispatcher func(ctx context.Context, bookID string) error

// GuideEstimate is the pre-flight shown before a run starts. ADR-0024 §4
// requires cost to follow visibly from an explicit action, and a number
// nobody sees until the bill arrives does not qualify.
type GuideEstimate struct {
	// Books is how many would be processed.
	Books int
	// FullTextBooks is how many send book text. Only EPUB does today, so
	// this is also "how many carry the per-book cap".
	FullTextBooks int
	// MaxInputTokens is a ceiling, not a prediction. It assumes every
	// full-text book fills the cap, which most real books do — a
	// 300-page EPUB extracts to roughly nine times it.
	MaxInputTokens int
}

// metadataPromptTokens is the rough cost of a metadata-only prompt:
// title, author, blurb, genres and the instructions.
const metadataPromptTokens = 400

// charsPerToken is the usual English approximation. Precision is not the
// point — the estimate exists to tell an admin whether they are about to
// spend cents or hundreds of dollars.
const charsPerToken = 4

// GuideRunner starts and sizes bulk guide generation (ADR-0024 §4).
type GuideRunner struct {
	candidates guideCandidateLister
	dispatch   GuideDispatcher
	textCap    int64
}

func NewGuideRunner(c guideCandidateLister, d GuideDispatcher, textCap int64) *GuideRunner {
	if textCap <= 0 {
		textCap = DefaultGuideTextCap
	}
	return &GuideRunner{candidates: c, dispatch: d, textCap: textCap}
}

// Estimate sizes a run without reading a single book.
//
// Counting exactly would mean extracting every EPUB in the library to
// answer "should I start?", which on an S3-backed library is a full
// download of everything. The format column is already in the row, so
// the ceiling is free — and since the cap binds for nearly every real
// book, the ceiling is close to the truth for the books that dominate
// the bill.
func (r *GuideRunner) Estimate(ctx context.Context) (GuideEstimate, error) {
	rows, err := r.candidates.ListGuideCandidates(ctx)
	if err != nil {
		return GuideEstimate{}, fmt.Errorf("list guide candidates: %w", err)
	}
	est := GuideEstimate{Books: len(rows)}
	capTokens := int(r.textCap / charsPerToken)
	for _, c := range rows {
		if strings.EqualFold(c.Format, "EPUB") {
			est.FullTextBooks++
			est.MaxInputTokens += capTokens + metadataPromptTokens
			continue
		}
		est.MaxInputTokens += metadataPromptTokens
	}
	return est, nil
}

// Start queues one job per candidate and reports how many went.
//
// Returns the count queued so far alongside the error when the queue
// refuses partway: those jobs are already running, and reporting zero
// would misrepresent what the admin's click actually did.
func (r *GuideRunner) Start(ctx context.Context) (int, error) {
	rows, err := r.candidates.ListGuideCandidates(ctx)
	if err != nil {
		return 0, fmt.Errorf("list guide candidates: %w", err)
	}
	queued := 0
	for _, c := range rows {
		if err := r.dispatch(ctx, c.BookID); err != nil {
			return queued, fmt.Errorf("queue guide for %s: %w", c.BookID, err)
		}
		queued++
	}
	return queued, nil
}
