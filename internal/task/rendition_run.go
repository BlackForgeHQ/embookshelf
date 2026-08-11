// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"context"
	"errors"
	"fmt"

	"github.com/blackforge/embookshelf/internal/jobs"
)

// renditionFailedWriter is the one row write the wrapper owns. Both
// artifact repos satisfy it.
type renditionFailedWriter interface {
	MarkFailed(ctx context.Context, bookID, msg string) error
}

// renditionStep is one unit of a rendition worker's work. On failure it
// reports what the row should say (empty msg means the row is not
// written — a cascaded row, or a repo write that itself failed) and
// whether the failure is permanent.
type renditionStep func(ctx context.Context) (msg string, permanent bool, err error)

// renditionRun executes steps in order and owns the loud-failure
// choreography (ADR-0033 §5): a failing step's message is written to
// the tracking row *before* the error returns, and a permanent verdict
// carries jobs.ErrDoNotRetry so River cancels instead of retrying. The
// write-row-before-return invariant used to be a ritual restated nine
// times across the two workers and enforced only by reviewer
// discipline; here a step cannot return a failure without the row
// being written (#302).
//
// The MarkFailed error is deliberately dropped, as at every one of the
// nine sites it replaces: the step's own error is the one the queue
// acts on, and a row write refused by the lifecycle guard (a sealed
// ready row, #296) is a no-op by design.
func renditionRun(ctx context.Context, rows renditionFailedWriter, bookID string, steps ...renditionStep) error {
	for _, step := range steps {
		msg, permanent, err := step(ctx)
		if err == nil {
			continue
		}
		if msg != "" {
			_ = rows.MarkFailed(ctx, bookID, msg)
		}
		if permanent && !errors.Is(err, jobs.ErrDoNotRetry) {
			return fmt.Errorf("%w (%w)", err, jobs.ErrDoNotRetry)
		}
		return err
	}
	return nil
}
