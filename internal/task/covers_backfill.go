package task

import (
	"context"
	"crypto/sha256"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/blackforge/embookshelf/internal/coverstore"
	"github.com/blackforge/embookshelf/internal/repo"
)

// CoversBackfillDeps groups the dependencies the cover migration needs.
type CoversBackfillDeps struct {
	Library *repo.LibraryRepo
	Covers  *coverstore.Store
	Sleep   time.Duration // pause between books; 0 → no pause
}

// RunCoversBackfill walks every book whose CoverHash is NULL and has
// HasCover=true. For each: read the legacy book-id-keyed file, hash
// it, save under the hash path, write CoverHash to the DB, delete
// the legacy file.
//
// Idempotent: ListBooksMissingCoverHash returns no rows once all covers
// are backfilled, so subsequent calls are no-ops. Errors per-book are
// logged and skipped; the next boot retries.
func RunCoversBackfill(ctx context.Context, deps CoversBackfillDeps) error {
	if deps.Library == nil || deps.Covers == nil {
		return nil
	}
	books, err := deps.Library.ListBooksMissingCoverHash(ctx)
	if err != nil {
		return err
	}
	migrated := 0
	for _, b := range books {
		if err := ctx.Err(); err != nil {
			return err
		}
		legacy := deps.Covers.BookPath(b.ID)
		f, err := os.Open(legacy)
		if err != nil {
			slog.Warn("covers backfill: open legacy", "book_id", b.ID, "err", err)
			continue
		}

		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			_ = f.Close()
			slog.Warn("covers backfill: hash", "book_id", b.ID, "err", err)
			continue
		}
		_ = f.Close()
		sum := h.Sum(nil)

		// Re-read to write to the hashed path.
		data, err := os.ReadFile(legacy)
		if err != nil {
			slog.Warn("covers backfill: re-read", "book_id", b.ID, "err", err)
			continue
		}
		if err := deps.Covers.SaveBookHashed(sum, b.CoverMime, data); err != nil {
			slog.Warn("covers backfill: save hashed", "book_id", b.ID, "err", err)
			continue
		}
		if err := deps.Library.SetCoverHash(ctx, b.ID, sum); err != nil {
			slog.Warn("covers backfill: set hash", "book_id", b.ID, "err", err)
			continue
		}
		if err := deps.Covers.DeleteBook(b.ID); err != nil {
			slog.Warn("covers backfill: delete legacy", "book_id", b.ID, "err", err)
			// non-fatal: the file still serves through legacy fallback
		}
		migrated++
		if deps.Sleep > 0 {
			select {
			case <-time.After(deps.Sleep):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	if migrated > 0 {
		slog.Info("covers backfill done", "migrated", migrated)
	}
	return nil
}
