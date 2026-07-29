// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/blackforge/embookshelf/internal/model"
)

// AdvanceAfterSegment records a segment's result and applies whatever
// that landing decided.
//
// The write side. The repo takes the run's row lock, writes the segment,
// reads Coverage under that lock and returns the transition, so two
// workers finishing at the same moment cannot each read a snapshot
// missing the other's write and both conclude they were last. This
// applies that verdict rather than deriving its own — re-reading here
// would throw away exactly the serialisation the lock bought.
//
// The enqueue stays outside the transaction because River is a different
// system no transaction spans. A crash in between still loses the
// finalize *job*, but not the *fact*: the segment rows say the run is
// complete, and Advance re-derives the same transition on the next read.
func (s *AudiobookService) AdvanceAfterSegment(
	ctx context.Context,
	bookID string,
	seq int,
	res model.SegmentResult,
) error {
	outcome, err := s.d.Store.RecordSegment(ctx, bookID, seq, res)
	if err != nil {
		return fmt.Errorf("record segment %d: %w", seq, err)
	}
	_, err = s.applyNext(ctx, bookID, outcome.Next, outcome.Coverage)
	return err
}

// advance derives the transition and applies it, returning the run state
// as it stands afterwards.
func (s *AudiobookService) advance(
	ctx context.Context,
	bookID string,
	state model.AudiobookState,
	cov model.AudiobookCoverage,
) (model.AudiobookState, error) {
	return s.applyNext(ctx, bookID, model.NextForRun(state, cov), cov)
}

// applyNext is the only place in the codebase that switches on
// model.AudiobookNext.
//
// The decision is model.NextForRun's, taken once and purely from
// Coverage. Carrying it out was written three times before this — inside
// the repo's transaction, in the segment worker, and in the status read —
// each covering a different slice of the enum, with "mark the run
// failed" having four writers and two ways of building its message
// (#190).
func (s *AudiobookService) applyNext(
	ctx context.Context,
	bookID string,
	next model.AudiobookNext,
	cov model.AudiobookCoverage,
) (model.AudiobookState, error) {
	switch next {
	case model.AudiobookNextFinalize:
		// The run's state is deliberately not moved here. Finalize sets
		// ready when it has the file; a run marked ready without one is a
		// book the UI offers and the player cannot open.
		if err := s.dispatchFinalize(ctx, bookID); err != nil {
			return "", err
		}
		return "", nil

	case model.AudiobookNextFail:
		// Staging is deliberately untouched. Retry re-enqueues only the
		// segments that never finished, so every paid-for one has to
		// survive the failure that stopped the run (ADR-0028 §6) —
		// failure keeps the work, cancel does not.
		if err := s.FailRun(ctx, bookID, cov.FailureMessage()); err != nil {
			return "", err
		}
		return model.AudiobookFailed, nil

	case model.AudiobookNextNothing:
		return "", nil
	}
	return "", nil
}

// FailRun is the one place a run is marked failed.
//
// Four writers reached this outcome before: the repo inside its
// transaction, the status read's reconcile, the dispatch loop when the
// queue rejected a segment, and the finalize worker when assembly blew
// up. Two derived the message from Coverage and two built it by hand,
// and only some of them published, so an identical failure notified open
// clients or did not depending on which path found it (#190).
//
// The message stays the caller's: "3 of 40 segments failed" and "could
// not queue segment 7" are different facts. What is not the caller's is
// the write, the publish, or whether either happens twice — FailRun is
// idempotent, and only a run that actually moved is published.
//
// The error is logged rather than returned. Every caller is somewhere
// that cannot act on it: a status read still has to answer, and a
// segment worker handed an error would make River retry audio it has
// already staged and paid for.
func (s *AudiobookService) FailRun(ctx context.Context, bookID, msg string) error {
	moved, err := s.d.Store.FailRun(ctx, bookID, msg)
	if err != nil {
		slog.Warn("audiobook: mark run failed", "book", bookID, "err", err)
		return nil
	}
	if moved {
		s.d.Publish(bookID)
	}
	return nil
}
