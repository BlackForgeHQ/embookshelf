// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/storage"
)

// OrphanedKeysBatch caps the number of pending_orphans rows the
// sweeper drains per pass. Bounded work per tick keeps DB+S3 load
// predictable and lets transient backend errors retry next tick.
const OrphanedKeysBatch = 500

// OrphanedKeysDeps is the dependency surface for the sweeper. The
// Storage backend per library is resolved on each row via Resolver
// + LibraryRepo so a freshly-added backend is picked up without
// restarting the loop.
type OrphanedKeysDeps struct {
	Orphans  *repo.PendingOrphanRepo
	Libs     *repo.LibraryRepo
	Resolver storage.Resolver
}

// RunOrphanedKeysOnce drains one batch. Used by tests + the looping
// driver. Returns (deletedCount, error). A backend error on a single
// row is logged and skipped; the row stays for the next pass.
func RunOrphanedKeysOnce(ctx context.Context, deps OrphanedKeysDeps, now time.Time) (int, error) {
	if deps.Orphans == nil || deps.Libs == nil || deps.Resolver == nil {
		return 0, nil
	}
	due, err := deps.Orphans.SelectDue(ctx, now, OrphanedKeysBatch)
	if err != nil {
		return 0, err
	}
	if len(due) == 0 {
		return 0, nil
	}
	libCache := make(map[string]storage.Storage, 4)
	deleted := 0
	for _, po := range due {
		store, ok := libCache[po.LibraryID]
		if !ok {
			lib, lerr := deps.Libs.GetByID(ctx, po.LibraryID)
			if lerr != nil {
				slog.Warn("orphan sweeper: lookup library", "library_id", po.LibraryID, "err", lerr)
				continue
			}
			backendID := ""
			if lib.BackendID != nil {
				backendID = *lib.BackendID
			}
			s, rerr := deps.Resolver.Resolve(backendID)
			if rerr != nil {
				slog.Warn("orphan sweeper: resolve backend",
					"library_id", po.LibraryID, "backend_id", backendID, "err", rerr)
				continue
			}
			store = s
			libCache[po.LibraryID] = store
		}
		if err := store.Delete(ctx, po.Key); err != nil && !errors.Is(err, storage.ErrNotFound) {
			slog.Warn("orphan sweeper: delete key",
				"library_id", po.LibraryID, "key", po.Key, "err", err)
			continue
		}
		if err := deps.Orphans.Delete(ctx, po.ID); err != nil {
			slog.Warn("orphan sweeper: dequeue row",
				"id", po.ID, "key", po.Key, "err", err)
			continue
		}
		deleted++
	}
	return deleted, nil
}

// LoopOrphanedKeys runs RunOrphanedKeysOnce on a ticker until ctx
// is cancelled. Errors are logged but do not stop the loop.
func LoopOrphanedKeys(ctx context.Context, deps OrphanedKeysDeps, every time.Duration) {
	if every <= 0 {
		every = time.Hour
	}
	if deps.Orphans == nil || deps.Libs == nil || deps.Resolver == nil {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := RunOrphanedKeysOnce(ctx, deps, time.Now())
			if err != nil {
				slog.Warn("orphan sweeper", "err", err)
				continue
			}
			if n > 0 {
				slog.Info("orphan sweeper", "deleted", n)
			}
		}
	}
}
