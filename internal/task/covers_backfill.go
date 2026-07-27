// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"context"
	"log/slog"
	"time"

	"github.com/blackforge/embookshelf/internal/coverstore"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// CoversBackfillDeps groups the dependencies the cover migration needs.
type CoversBackfillDeps struct {
	Books  *repo.BookRepo
	Covers *coverstore.Store
	Sleep  time.Duration // pause between books; 0 → no pause
}

// RunCoversBackfill walks every book whose CoverHash is NULL and has
// HasCover=true, and re-keys its cover into the hash-keyed namespace.
//
// The bytes never pass through this file: coverstore.MigrateLegacy reads
// the legacy copy, hashes it and writes the hashed one, so the task owns
// only the ordering that matters — the DB write comes before the legacy
// sweep, because a cover whose only copy is deleted before cover_hash
// lands is a cover nobody can serve.
//
// Idempotent: ListMissingCoverHash returns no rows once all covers are
// backfilled, so subsequent calls are no-ops. Errors per-book are logged
// and skipped; the next boot retries. A book left mid-migration still
// serves, because coverstore.Open falls back to the legacy copy.
func RunCoversBackfill(ctx context.Context, deps CoversBackfillDeps) error {
	if deps.Books == nil || deps.Covers == nil {
		return nil
	}
	cfg := DrainConfig{
		Name:  "covers-hash",
		Sleep: deps.Sleep,
	}
	_, err := Drain(ctx, cfg,
		deps.Books.ListMissingCoverHash,
		func(b model.Book) string { return b.ID },
		func(ctx context.Context, b model.Book) error {
			sum, err := deps.Covers.MigrateLegacy(b)
			if err != nil {
				slog.Warn("covers backfill: migrate legacy", "book_id", b.ID, "err", err)
				return err
			}
			if err := deps.Books.SetCoverHash(ctx, b.ID, sum); err != nil {
				slog.Warn("covers backfill: set hash", "book_id", b.ID, "err", err)
				return err
			}
			if err := deps.Covers.DeleteBook(b.ID); err != nil {
				slog.Warn("covers backfill: delete legacy", "book_id", b.ID, "err", err)
				// non-fatal: the hashed copy is already the one that serves
			}
			return nil
		},
	)
	return err
}
