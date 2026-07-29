// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/blackforge/embookshelf/internal/hashing"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// FilesBackfillDeps groups the dependencies the boot-time hashing
// pass needs.
type FilesBackfillDeps struct {
	Files    *repo.FileRepo
	LibStore service.LibraryStore
	// BatchSize and Sleep tune the Drain loop. Zero values use defaults
	// (BatchSize=100, no inter-batch pause).
	BatchSize int
	Sleep     time.Duration
}

// RunFilesBackfill drains files.content_hash IS NULL by streaming
// each file through sha256 and updating the row. Idempotent: missing
// files (Storage.Get → ErrNotFound) are skipped and logged; the row
// stays NULL so a future run retries when the file reappears.
//
// Returns nil when no rows remain pending or LibStore/Files is unwired.
// Caller invokes this once at startup; rerunning is safe.
func RunFilesBackfill(ctx context.Context, deps FilesBackfillDeps) error {
	if deps.Files == nil || deps.LibStore == nil {
		return nil // not yet wired
	}
	cfg := DrainConfig{
		Name:      "files-hash",
		BatchSize: deps.BatchSize,
		Sleep:     deps.Sleep,
	}
	_, err := Drain(ctx, cfg,
		deps.Files.ListPendingHash,
		func(f model.File) string { return f.ID },
		func(ctx context.Context, f model.File) error {
			handle, err := deps.LibStore.For(ctx, f.LibraryID)
			if err != nil {
				slog.Warn("files backfill: library lookup failed",
					"file_id", f.ID, "library_id", f.LibraryID, "err", err)
				return err
			}
			if handle.Storage == nil {
				slog.Warn("files backfill: no storage for library",
					"file_id", f.ID, "library_id", f.LibraryID)
				return errors.New("no storage for library")
			}
			// The library's own rule, not a copy of it: it passes a
			// backend-backed key and an already-absolute legacy location
			// through untouched, which joining the root cannot (#201).
			key := handle.StorageKey(f.Location)

			hash, size, err := hashing.HashFile(ctx, handle.Storage, key)
			if err != nil {
				slog.Warn("files backfill: hash failed",
					"file_id", f.ID, "key", key, "err", err)
				return err
			}
			info, headErr := handle.Storage.Head(ctx, key)
			mtime := time.Now()
			if headErr == nil {
				mtime = info.ModTime
			}

			if err := deps.Files.SetContentHash(ctx, f.ID, hash, size, mtime); err != nil {
				slog.Warn("files backfill: set hash failed",
					"file_id", f.ID, "err", err)
				return err
			}
			return nil
		},
	)
	return err
}
