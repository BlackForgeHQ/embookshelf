// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"log/slog"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// A folder rename moves a Book's bytes so its location matches its
// metadata, and it is its own concern rather than a step of the write
// pipeline.
//
// It lived inside the pipeline, which called it and kept a boolean and a
// string, under a comment explaining that a false meant either "declined"
// or "broke" and the two could not be told apart at that seam. That is a
// module saying its return type is wrong, and it is why an edit that
// renamed nothing at all looked exactly like an edit whose rename failed
// (#211, #212).
//
// Two arms, and they share only the word. Local: the adapter's move is
// one atomic rename(2), there is nothing to reclaim, and the DB
// catch-up is best-effort (ADR-0003 §6.5). Backend (S3): the adapter
// copies and hands back the live sources, and this module wraps a
// single-tx DB swap plus sweeper-deferred deletes around them
// (ADR-0005).
//
// On any failure the on-disk / backend state is left consistent with the
// pre-rename world; the DB metadata is already committed (the sidecar
// carries it on S3) and the caller can retry.

// RenameOutcome is what a rename attempt did.
//
// Three answers, not two. Declined is the ordinary case — nothing to
// move, the target is already correct, a degraded deployment with no
// orphan queue — and it is not a failure. Err is a rename that broke:
// the bytes are where they were, the DB says what it said, and the book
// is still readable at its old path.
type RenameOutcome struct {
	// Done reports that the bytes moved and the DB agrees.
	Done bool
	// Folder is where the book ended up. Set only when Done, and it can
	// differ from the folder that was asked for: a collision with
	// another Book sharing Author+Title gets a numbered suffix.
	Folder string
	// Declined says why nothing was attempted, for a log line that is
	// not a warning. Empty when something was attempted.
	Declined string
	// Err is a rename that broke partway. Nil when nothing broke.
	Err error
}

// renameDone reports a completed rename and where it landed.
func renameDone(folder string) RenameOutcome {
	return RenameOutcome{Done: true, Folder: folder}
}

// renameDeclined reports that nothing was attempted, and why.
func renameDeclined(why string) RenameOutcome {
	return RenameOutcome{Declined: why}
}

// renameBroke reports a rename that started and failed. The specific
// cause is already logged where it happened; this carries it to the
// caller so a decline and a break are not the same answer.
func renameBroke(err error) RenameOutcome {
	return RenameOutcome{Err: err}
}

// renameFolder dispatches the post-DB rename step by backend kind.
// Both arms move the bytes through Storage.MovePrefix; what differs is
// the policy wrapped around it, which is why the two arms still exist.
// Local: the adapter's move is atomic, there is nothing to reclaim, and
// the DB catch-up is best-effort (ADR-0003 §6.5). Backend (S3): the
// adapter copies and hands back the live sources, and this module wraps
// a single-tx DB swap plus sweeper-deferred deletes around them
// (ADR-0005).
//
// On any failure the on-disk / backend state is left consistent
// with the pre-rename world; the DB metadata is already committed
// (sidecar carries it on S3) and the caller can retry.
func (w *MetadataWriter) renameFolder(
	ctx context.Context,
	b model.Book,
	handle *LibraryHandle,
	oldFolder, newFolder string,
) RenameOutcome {
	// A Book with no folder has nothing to rename — it has never been in
	// one. Moving it into the layout is a migration, and the pipeline
	// calls that separately (ADR-0003 §5).
	if oldFolder == "" {
		return renameDeclined("book is not in the folder layout yet")
	}
	if handle.IsObjectStore() {
		return w.renameFolderBackend(ctx, b, handle, oldFolder, newFolder)
	}
	return w.renameFolderLocal(ctx, b, handle, oldFolder, newFolder)
}

// renameFolderLocal is the local-fs arm. The Book owns a folder and it
// moves whole, through Storage.MovePrefix — one atomic rename(2) inside
// the local adapter, carrying the sidecar and any companion files for
// free.
//
// The flat-layout case that used to be this function's other half is
// not here: a book with no folder is being migrated into the layout,
// not renamed, and the two shared this function and nothing else.
func (w *MetadataWriter) renameFolderLocal(
	ctx context.Context,
	b model.Book,
	handle *LibraryHandle,
	oldFolder, newFolder string,
) RenameOutcome {
	// libRoot deliberately reads Library.Path, not localRoot()'s
	// Root-then-Path preference. The two columns disagree and ADR-0030
	// files that as its own problem; switching which one the rename
	// reads under cover of this refactor would move books for reasons
	// nobody asked for.
	libRoot := strings.TrimRight(handle.Library.Path, "/")
	if libRoot == "" {
		return renameDeclined("library has no root configured")
	}
	if handle.Storage == nil {
		return renameDeclined("library has no storage")
	}

	// Build a unique destination dir if the target already exists —
	// a collision with another Book that happens to share Author+Title.
	// Collision probing is policy and stays here; only the move itself
	// is the adapter's.
	oldAbs := filepath.Join(libRoot, oldFolder)
	finalAbs := uniqueDirectoryUnless(filepath.Join(libRoot, newFolder), oldAbs)
	finalFolder := strings.TrimPrefix(finalAbs, libRoot+"/")

	// Through the adapter, not os.Rename: a local library's LocalFS is
	// rooted at "/" (ADR-0030 §1), so these absolute paths are exactly
	// the keys it answers to. The MoveResult is empty by contract for an
	// atomic backend — nothing written to reclaim, no source left to
	// schedule — which is why this arm has no rollback and the backend
	// one does.
	if _, err := handle.Storage.MovePrefix(ctx, oldAbs, finalAbs); err != nil {
		slog.Warn("metadata writer: rename folder",
			"book_id", b.ID, "from", oldAbs, "to", finalAbs, "err", err)
		return renameBroke(err)
	}

	if err := w.persistRename(ctx, b, finalFolder); err != nil {
		// Persist failed after the on-disk move. The DB still says "old
		// folder", reality is "new folder". A known soft-failure; scan
		// reattach corrects it by content hash on the next pass.
		return renameBroke(err)
	}
	return renameDone(finalFolder)
}

// persistRename updates files.location for every files row of the
// Book and books.folder_path + books.path so the DB reflects the
// post-rename layout. Best-effort: per-file failures log and
// continue; the books-row update is the last write so a crash
// midway leaves only file rows pointing at stale locations, which
// scan reattach corrects on next pass.
func (w *MetadataWriter) persistRename(
	ctx context.Context,
	b model.Book,
	newFolder string,
) error {
	if w.deps.Files != nil {
		files, err := w.deps.Files.ListByBook(ctx, b.ID)
		if err != nil {
			slog.Warn("metadata writer: list files post-rename",
				"book_id", b.ID, "err", err)
		}
		for _, f := range files {
			newLoc := path.Join(newFolder, filepath.Base(f.Location))
			if err := w.deps.Files.UpdateLocation(ctx, f.ID, newLoc); err != nil {
				slog.Warn("metadata writer: update files.location",
					"book_id", b.ID, "file_id", f.ID, "err", err)
			}
		}
	}
	newBookPath := path.Join(newFolder, filepath.Base(b.Path))
	if err := w.deps.Books.SetFolderPath(ctx, b.ID, newFolder, newBookPath); err != nil {
		slog.Warn("metadata writer: set folder_path",
			"book_id", b.ID, "folder", newFolder, "err", err)
		return err
	}
	return nil
}

// renameFolderBackend is the S3 (any non-local Storage) arm, per
// ADR-0005. Pipeline:
//
//  1. Resolve a non-colliding new prefix via uniqueBackendFolder.
//  2. Storage.MovePrefix the old folder onto it. The adapter owns the
//     list, the per-key copy and its retry budget; it hands back the
//     destinations it wrote and the sources it left alive.
//  3. On failure, schedule MoveResult.Written with RenameRollbackGrace
//     and bail.
//  4. Compute the per-files location updates for the rows DB knows
//     about (others ride along under the new prefix without a row).
//  5. RenameFolderTx: single transaction wraps files updates +
//     books folder_path/path + INSERT pending_orphans
//     (MoveResult.Reclaim, RenameGrace).
//
// Inline phase-2 deletes do not happen here — the sweeper drains
// pending_orphans after the grace window. This keeps already-issued
// presigned URLs valid for at least 2× PresignTTL.
func (w *MetadataWriter) renameFolderBackend(
	ctx context.Context,
	b model.Book,
	handle *LibraryHandle,
	oldFolder, newFolder string,
) RenameOutcome {
	if handle.Storage == nil {
		return renameDeclined("library has no storage")
	}
	if w.deps.Orphans == nil {
		// Without an orphan queue we cannot defer the source delete
		// safely. Fail closed — sidecar full-mirror still carries the
		// edit per ADR-0001's S3 fallback.
		return renameDeclined("no orphan queue; ADR-0005 rename is fail-closed")
	}

	finalFolder := uniqueBackendFolder(ctx, handle.Storage, newFolder)
	oldPrefix := oldFolder + "/"
	newPrefix := finalFolder + "/"

	moved, err := handle.Storage.MovePrefix(ctx, oldPrefix, newPrefix)
	if err != nil {
		slog.Warn("metadata writer: backend rename move prefix",
			"book_id", b.ID, "from", oldPrefix, "to", newPrefix, "err", err)
		// Written is populated even on error — a copy loop that broke
		// halfway still created objects, and this module owns the
		// decision to reclaim them.
		w.scheduleOrphans(ctx, handle.Library.ID, moved.Written, RenameRollbackGrace, b.ID)
		return renameBroke(err)
	}

	fileUpdates, err := w.collectFileLocationUpdates(ctx, b, oldPrefix, newPrefix)
	if err != nil {
		slog.Warn("metadata writer: backend rename file enumeration",
			"book_id", b.ID, "err", err)
		w.scheduleOrphans(ctx, handle.Library.ID, moved.Written, RenameRollbackGrace, b.ID)
		return renameBroke(err)
	}

	newBookPath := newPrefix + filepath.Base(b.Path)
	// Reclaim, not the source listing: the adapter says which sources
	// are still there. An atomic backend leaves none and enqueues none.
	orphanInserts := buildOrphanInserts(handle.Library.ID, moved.Reclaim, w.renameGrace(), b.ID)

	if err := w.deps.Books.RenameFolderTx(ctx, repo.RenameFolderTxArgs{
		BookID:    b.ID,
		NewFolder: finalFolder,
		NewPath:   newBookPath,
		Files:     fileUpdates,
		Orphans:   orphanInserts,
	}); err != nil {
		slog.Warn("metadata writer: backend rename db tx",
			"book_id", b.ID, "err", err)
		w.scheduleOrphans(ctx, handle.Library.ID, moved.Written, RenameRollbackGrace, b.ID)
		return renameBroke(err)
	}

	return renameDone(finalFolder)
}

// renameGrace returns the configured grace duration for old keys
// after a successful rename. Defaults to 1h when unset, matching
// the ADR-0005 fallback used in cmd/embookshelf wiring.
func (w *MetadataWriter) renameGrace() time.Duration {
	if w.deps.RenameGrace > 0 {
		return w.deps.RenameGrace
	}
	return time.Hour
}

// collectFileLocationUpdates lists the Book's files rows and builds
// per-row UPDATE inputs for any row whose location lives under the
// old prefix. Rows that don't (legacy data drift) are skipped — they
// will not survive the post-tx state but the rename should not fail
// over a misaligned row.
func (w *MetadataWriter) collectFileLocationUpdates(
	ctx context.Context,
	b model.Book,
	oldPrefix, newPrefix string,
) ([]repo.FileLocationUpdate, error) {
	if w.deps.Files == nil {
		return nil, nil
	}
	files, err := w.deps.Files.ListByBook(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	out := make([]repo.FileLocationUpdate, 0, len(files))
	for _, f := range files {
		if !strings.HasPrefix(f.Location, oldPrefix) {
			slog.Warn("metadata writer: backend rename skipping mis-prefixed file",
				"book_id", b.ID, "file_id", f.ID, "location", f.Location)
			continue
		}
		out = append(out, repo.FileLocationUpdate{
			FileID:   f.ID,
			Location: newPrefix + strings.TrimPrefix(f.Location, oldPrefix),
		})
	}
	return out, nil
}

// scheduleOrphans is the rollback escape hatch: enqueue the supplied
// (presumably new-prefix) keys with RenameRollbackGrace so the
// sweeper deletes the half-rename garbage on its next pass. Best
// effort — failure to enqueue is logged but does not change the
// caller's error path.
func (w *MetadataWriter) scheduleOrphans(
	ctx context.Context,
	libraryID string,
	keys []string,
	grace time.Duration,
	bookID string,
) {
	if len(keys) == 0 || w.deps.Orphans == nil {
		return
	}
	rows := buildOrphanInserts(libraryID, keys, grace, bookID)
	if err := w.deps.Orphans.Insert(ctx, rows); err != nil {
		slog.Warn("metadata writer: schedule rollback orphans",
			"library_id", libraryID, "count", len(keys), "err", err)
	}
}

// buildOrphanInserts converts a slice of keys into the typed insert
// rows for the pending_orphans table. eligible_at is now+grace.
func buildOrphanInserts(libraryID string, keys []string, grace time.Duration, bookID string) []repo.PendingOrphanInsert {
	if len(keys) == 0 {
		return nil
	}
	now := time.Now().UTC()
	out := make([]repo.PendingOrphanInsert, 0, len(keys))
	bid := bookID
	for _, k := range keys {
		out = append(out, repo.PendingOrphanInsert{
			LibraryID:  libraryID,
			Key:        k,
			EligibleAt: now.Add(grace),
			Reason:     repo.ReasonOrphanRename,
			BookID:     &bid,
		})
	}
	return out
}

// uniqueDirectoryUnless is a variant of uniqueDirectory that returns
// the input unchanged when it equals the source (oldAbs). Used by
// rename to avoid bumping a target that is the same directory we're
// renaming from (a no-op rename) — though folderDelta should have
// short-circuited that case already.
func uniqueDirectoryUnless(dest, oldAbs string) string {
	if dest == oldAbs {
		return dest
	}
	return uniqueDirectory(dest)
}
