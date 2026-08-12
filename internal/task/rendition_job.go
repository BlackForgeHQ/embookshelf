// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"context"
	"errors"
	"fmt"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// ErrConverterNotConfigured is the loud "extension not configured"
// answer (ADR-0033 §5) — distinct from a conversion that failed. Wraps
// ErrDoNotRetry: a disabled extension will still be disabled in thirty
// seconds. Declared with the shared prelude because the gate that
// raises it lives here — it used to sit in one worker's file and be
// reached across files by the other (#309).
var ErrConverterNotConfigured = fmt.Errorf(repo.MsgConverterNotConfigured+": %w", jobs.ErrDoNotRetry)

// renditionRowWriter is the row slice the prelude drives: MarkRunning
// as its last gate, MarkFailed through renditionRun's choreography.
// Both artifact repos satisfy it.
type renditionRowWriter interface {
	renditionFailedWriter
	MarkRunning(ctx context.Context, bookID string) error
}

// renditionJob is the shared prelude every converter worker starts
// with (#309): load the book, gate on the Convertible set, gate on the
// converter being configured, then — strictly last, so a refused job
// never shows as running — mark the row running. The two workers used
// to restate these ~35 lines near-verbatim, and the sequence had
// already leaked: the epub worker reached this file's sentinel across
// files because the prelude had no module to live in.
type renditionJob struct {
	Rows  renditionRowWriter
	Books bookReader
	// Config is read per job so an admin pointing the CONVERTER row at
	// a new URL takes effect on the next job, not the next restart.
	Config func(context.Context) (repo.ConverterConfig, error)
	// Refusal renders the artifact's own message for a book outside the
	// Convertible set — the one string the two workers genuinely differ by.
	Refusal func(format string) string
	// Wired is the artifact's is-everything-wired gate, run after
	// Configured and before MarkRunning — the epub worker's markdown-feed
	// check. nil means the artifact needs nothing beyond the converter.
	Wired func() (msg string, err error)
}

// Prepare runs the gates in order and hands back the facts the artifact
// steps need, or the already-recorded refusal. Failure semantics are
// renditionRun's (ADR-0033 §5): the row is written before the error
// returns, permanent verdicts carry ErrDoNotRetry.
func (j renditionJob) Prepare(ctx context.Context, bookID string) (model.Book, repo.ConverterConfig, error) {
	var (
		book model.Book
		cfg  repo.ConverterConfig
	)
	err := renditionRun(ctx, j.Rows, bookID,
		func(ctx context.Context) (string, bool, error) {
			var err error
			book, err = j.Books.GetByID(ctx, "", bookID)
			if err != nil {
				// A deleted book cascades its rendition row; nothing to record.
				return "", true, fmt.Errorf("load book %s: %w", bookID, err)
			}
			return "", false, nil
		},
		func(context.Context) (string, bool, error) {
			if model.Convertible(book.Format) {
				return "", false, nil
			}
			msg := j.Refusal(book.Format)
			return msg, true, errors.New(msg)
		},
		func(ctx context.Context) (string, bool, error) {
			var err error
			cfg, err = j.Config(ctx)
			if err != nil {
				return "read converter settings: " + err.Error(), false, fmt.Errorf("read converter settings: %w", err)
			}
			if !cfg.Configured() {
				return repo.MsgConverterNotConfigured, true, ErrConverterNotConfigured
			}
			if j.Wired != nil {
				if msg, err := j.Wired(); err != nil {
					return msg, true, err
				}
			}
			return "", false, nil
		},
		func(ctx context.Context) (string, bool, error) {
			if err := j.Rows.MarkRunning(ctx, bookID); err != nil {
				return "", false, fmt.Errorf("mark running: %w", err)
			}
			return "", false, nil
		},
	)
	return book, cfg, err
}
