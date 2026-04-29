package scan

import (
	"context"
	"errors"
	"time"

	"github.com/blackforge/embookshelf/internal/repo"
)

// MaybeReattach handles the rename case. After a Changed entry has
// been re-hashed, if the new hash matches another files row in the
// same library, the file was probably renamed: update that row's
// location instead of orphaning the old row. Returns (true, nil)
// when a reattach happened.
//
// The caller passes:
//   - the freshly computed hash for the moved file,
//   - the new location it now lives at,
//   - the OLD row's id (the row that was at the previous location).
//
// On reattach: the OTHER row (the one whose hash matches) takes
// over the new location, and the OLD row is marked missing so the
// purge sweeper deletes it after the TTL. This preserves book_id
// continuity when the user renames a file outside the app.
func MaybeReattach(ctx context.Context, files *repo.FileRepo, libraryID string, hash []byte, newLocation string, oldRowID string) (bool, error) {
	if len(hash) == 0 {
		return false, nil
	}
	matches, err := files.GetByContentHash(ctx, hash)
	if err != nil {
		return false, err
	}
	for _, m := range matches {
		if m.LibraryID != libraryID {
			continue
		}
		if m.ID == oldRowID {
			continue
		}
		// Reattach: m takes over newLocation; old row goes missing.
		if err := files.UpdateLocation(ctx, m.ID, newLocation); err != nil {
			if errors.Is(err, repo.ErrFileLocationTaken) {
				return false, err
			}
			return false, err
		}
		if err := files.MarkMissing(ctx, oldRowID, time.Now()); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}
