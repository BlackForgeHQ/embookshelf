// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// FolderRenamer moves a Book's bytes so its location matches its
// metadata. It is its own module, not a step of the write pipeline: it
// owns the migrate-vs-rename dispatch, the collision probing, the
// persist, and the S3 rollback policy, and the pipeline only ever sees
// a RenameOutcome.
//
// It lived inside the pipeline as four methods on the writer, which
// kept a boolean and a string under a comment explaining that a false
// meant either "declined" or "broke" and the two could not be told
// apart at that seam. That is a module saying its return type is wrong,
// and it is why an edit that renamed nothing at all looked exactly like
// an edit whose rename failed (#211, #212).
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

// RenameRollbackGrace is the grace window applied to half-rename
// new keys that need to be reaped after a phase-1 copy or DB-tx
// failure. Short by design: no client ever held a presigned URL for
// these keys (DB never referenced them), so there's nothing to wait
// for. ADR-0005 §3.4.
const RenameRollbackGrace = 5 * time.Minute

// RenameStore is the slice of *repo.BookRepo the renamer needs to make
// the DB agree with a move.
type RenameStore interface {
	SetFolderPath(ctx context.Context, bookID, folderPath, path string) error
	// RenameFolderTx is the single-transaction DB swap that finalises
	// an S3 folder rename per ADR-0005: rewrites every files.location
	// supplied, sets books.folder_path + books.path, and enqueues the
	// supplied orphan rows.
	RenameFolderTx(ctx context.Context, args repo.RenameFolderTxArgs) error
}

// RenameFileStore is the slice of *repo.FileRepo the renamer needs:
// enumerate a Book's file rows and point them at their new locations.
type RenameFileStore interface {
	ListByBook(ctx context.Context, bookID string) ([]model.File, error)
	UpdateLocation(ctx context.Context, fileID, newLocation string) error
}

// PendingOrphansEnqueuer is the slice of *repo.PendingOrphanRepo the
// renamer needs for ADR-0005: old keys after a successful S3 rename,
// half-rename garbage after a failed one.
type PendingOrphansEnqueuer interface {
	Insert(ctx context.Context, rows []repo.PendingOrphanInsert) error
}

// FolderRenamerDeps groups the renamer's own dependencies. Orphans is
// nil-tolerant: without an orphan queue the S3 arm fails closed and
// local renames are unaffected.
type FolderRenamerDeps struct {
	Store RenameStore
	Files RenameFileStore
	// Orphans is the queue used by the S3 rename rollback path
	// (ADR-0005). Nil disables backend rename — a degraded renamer
	// behaves like the pre-ADR-0005 build.
	Orphans PendingOrphansEnqueuer
	// Grace is the eligible_at delta applied to the *old* keys enqueued
	// after a successful S3 rename. Defaults to 1h when zero. Operators
	// set this to ≥ 2 × PresignTTL.
	Grace time.Duration
}

type FolderRenamer struct {
	deps FolderRenamerDeps
}

func NewFolderRenamer(deps FolderRenamerDeps) *FolderRenamer {
	return &FolderRenamer{deps: deps}
}

// RenameOutcome is what a Relocate attempt did.
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

// Relocate is the module's one entry point: it moves the Book so its
// location matches oldFolder → newFolder, and the dispatch is its own
// business. An empty oldFolder is a flat-layout Book that has never had
// a folder, and it gets the lazy migration of ADR-0003 §5 — this is the
// only place that ordering decision is stated. A foldered Book gets the
// rename arm its backend calls for.
func (r *FolderRenamer) Relocate(
	ctx context.Context,
	b model.Book,
	handle *LibraryHandle,
	oldFolder, newFolder string,
) RenameOutcome {
	if handle == nil || handle.Storage == nil {
		return renameDeclined("library has no storage")
	}
	if oldFolder == "" {
		return r.migrateToFolderLayout(ctx, b, handle, newFolder)
	}
	if handle.IsObjectStore() {
		return r.renameBackend(ctx, b, handle, oldFolder, newFolder)
	}
	return r.renameLocal(ctx, b, handle, oldFolder, newFolder)
}

// renameLocal is the local-fs arm. The Book owns a folder and it moves
// whole, through Storage.MovePrefix — one atomic rename(2) inside the
// local adapter, carrying the sidecar and any companion files for free.
func (r *FolderRenamer) renameLocal(
	ctx context.Context,
	b model.Book,
	handle *LibraryHandle,
	oldFolder, newFolder string,
) RenameOutcome {
	// The root deliberately reads Library.Path, not localRoot()'s
	// Root-then-Path preference. The two columns disagree and ADR-0030
	// files that as its own problem; switching which one the rename
	// reads under cover of this refactor would move books for reasons
	// nobody asked for. libRoot holds whichever root it is given — it
	// does not settle that question.
	root := newLibRoot(handle.Library.Path)
	if root.empty() {
		return renameDeclined("library has no root configured")
	}

	// Build a unique destination dir if the target already exists —
	// a collision with another Book that happens to share Author+Title.
	// Collision probing is policy and stays here; only the move itself
	// is the adapter's. The source folder is excepted so a no-op rename
	// does not bump itself to " (2)".
	oldAbs := root.abs(oldFolder)
	finalAbs, finalFolder, err := root.freeDir(newFolder, oldAbs)
	if err != nil {
		// Nothing has moved yet. The old code trimmed the prefix by hand
		// and, when it did not match, persisted the absolute path as
		// books.folder_path (#323).
		slog.Warn("folder renamer: resolve target folder",
			"book_id", b.ID, "folder", newFolder, "err", err)
		return renameBroke(err)
	}

	// Through the adapter, not os.Rename: a local library's LocalFS is
	// rooted at "/" (ADR-0030 §1), so these absolute paths are exactly
	// the keys it answers to. The MoveResult is empty by contract for an
	// atomic backend — nothing written to reclaim, no source left to
	// schedule — which is why this arm has no rollback and the backend
	// one does.
	if _, err := handle.Storage.MovePrefix(ctx, oldAbs, finalAbs); err != nil {
		slog.Warn("folder renamer: move",
			"book_id", b.ID, "from", oldAbs, "to", finalAbs, "err", err)
		return renameBroke(err)
	}

	if err := r.persist(ctx, b, finalFolder); err != nil {
		// Persist failed after the on-disk move. The DB still says "old
		// folder", reality is "new folder". A known soft-failure; scan
		// reattach corrects it by content hash on the next pass.
		return renameBroke(err)
	}
	return renameDone(finalFolder)
}

// persist updates files.location for every files row of the Book and
// books.folder_path + books.path so the DB reflects the post-move
// layout. Best-effort: per-file failures log and continue; the
// books-row update is the last write so a crash midway leaves only file
// rows pointing at stale locations, which scan reattach corrects on
// next pass.
func (r *FolderRenamer) persist(
	ctx context.Context,
	b model.Book,
	newFolder string,
) error {
	if r.deps.Files != nil {
		files, err := r.deps.Files.ListByBook(ctx, b.ID)
		if err != nil {
			slog.Warn("folder renamer: list files post-move",
				"book_id", b.ID, "err", err)
		}
		for _, f := range files {
			newLoc := path.Join(newFolder, filepath.Base(f.Location))
			if err := r.deps.Files.UpdateLocation(ctx, f.ID, newLoc); err != nil {
				slog.Warn("folder renamer: update files.location",
					"book_id", b.ID, "file_id", f.ID, "err", err)
			}
		}
	}
	newBookPath := path.Join(newFolder, filepath.Base(b.Path))
	if err := r.deps.Store.SetFolderPath(ctx, b.ID, newFolder, newBookPath); err != nil {
		slog.Warn("folder renamer: set folder_path",
			"book_id", b.ID, "folder", newFolder, "err", err)
		return err
	}
	return nil
}

// renameBackend is the S3 (any non-local Storage) arm, per ADR-0005.
// Pipeline:
//
//  1. Resolve a non-colliding new prefix via backendRoot().freeDirBackend.
//  2. Storage.MovePrefix the old folder onto it. The adapter owns the
//     list, the per-key copy and its retry budget; it hands back the
//     destinations it wrote and the sources it left alive.
//  3. On failure, schedule MoveResult.Written with RenameRollbackGrace
//     and bail.
//  4. Compute the per-files location updates for the rows DB knows
//     about (others ride along under the new prefix without a row).
//  5. RenameFolderTx: single transaction wraps files updates +
//     books folder_path/path + INSERT pending_orphans
//     (MoveResult.Reclaim, Grace).
//
// Inline phase-2 deletes do not happen here — the sweeper drains
// pending_orphans after the grace window. This keeps already-issued
// presigned URLs valid for at least 2× PresignTTL.
func (r *FolderRenamer) renameBackend(
	ctx context.Context,
	b model.Book,
	handle *LibraryHandle,
	oldFolder, newFolder string,
) RenameOutcome {
	if r.deps.Orphans == nil {
		// Without an orphan queue we cannot defer the source delete
		// safely. Fail closed — sidecar full-mirror still carries the
		// edit per ADR-0001's S3 fallback.
		return renameDeclined("no orphan queue; ADR-0005 rename is fail-closed")
	}

	// backendRoot: an object store has no filesystem root, and the
	// prefix it answers with is already the library-relative location
	// (ADR-0030 §1). No arithmetic, only the probe.
	finalFolder, err := backendRoot().freeDirBackend(ctx, handle.Storage, newFolder)
	if err != nil {
		slog.Warn("folder renamer: resolve backend prefix",
			"book_id", b.ID, "folder", newFolder, "err", err)
		return renameBroke(err)
	}
	oldPrefix := oldFolder + "/"
	newPrefix := finalFolder + "/"

	moved, err := handle.Storage.MovePrefix(ctx, oldPrefix, newPrefix)
	if err != nil {
		slog.Warn("folder renamer: backend move prefix",
			"book_id", b.ID, "from", oldPrefix, "to", newPrefix, "err", err)
		// Written is populated even on error — a copy loop that broke
		// halfway still created objects, and this module owns the
		// decision to reclaim them.
		r.scheduleOrphans(ctx, handle.Library.ID, moved.Written, RenameRollbackGrace, b.ID)
		return renameBroke(err)
	}

	fileUpdates, err := r.collectFileLocationUpdates(ctx, b, oldPrefix, newPrefix)
	if err != nil {
		slog.Warn("folder renamer: backend file enumeration",
			"book_id", b.ID, "err", err)
		r.scheduleOrphans(ctx, handle.Library.ID, moved.Written, RenameRollbackGrace, b.ID)
		return renameBroke(err)
	}

	newBookPath := newPrefix + filepath.Base(b.Path)
	// Reclaim, not the source listing: the adapter says which sources
	// are still there. An atomic backend leaves none and enqueues none.
	orphanInserts := buildOrphanInserts(handle.Library.ID, moved.Reclaim, r.grace(), b.ID)

	if err := r.deps.Store.RenameFolderTx(ctx, repo.RenameFolderTxArgs{
		BookID:    b.ID,
		NewFolder: finalFolder,
		NewPath:   newBookPath,
		Files:     fileUpdates,
		Orphans:   orphanInserts,
	}); err != nil {
		slog.Warn("folder renamer: backend db tx",
			"book_id", b.ID, "err", err)
		r.scheduleOrphans(ctx, handle.Library.ID, moved.Written, RenameRollbackGrace, b.ID)
		return renameBroke(err)
	}

	return renameDone(finalFolder)
}

// grace returns the configured grace duration for old keys after a
// successful rename. Defaults to 1h when unset, matching the ADR-0005
// fallback internal/app applies when building the renamer.
func (r *FolderRenamer) grace() time.Duration {
	if r.deps.Grace > 0 {
		return r.deps.Grace
	}
	return time.Hour
}

// collectFileLocationUpdates lists the Book's files rows and builds
// per-row UPDATE inputs for any row whose location lives under the
// old prefix. Rows that don't (legacy data drift) are skipped — they
// will not survive the post-tx state but the rename should not fail
// over a misaligned row.
func (r *FolderRenamer) collectFileLocationUpdates(
	ctx context.Context,
	b model.Book,
	oldPrefix, newPrefix string,
) ([]repo.FileLocationUpdate, error) {
	if r.deps.Files == nil {
		return nil, nil
	}
	files, err := r.deps.Files.ListByBook(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	out := make([]repo.FileLocationUpdate, 0, len(files))
	for _, f := range files {
		if !strings.HasPrefix(f.Location, oldPrefix) {
			slog.Warn("folder renamer: skipping mis-prefixed file",
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
func (r *FolderRenamer) scheduleOrphans(
	ctx context.Context,
	libraryID string,
	keys []string,
	grace time.Duration,
	bookID string,
) {
	if len(keys) == 0 || r.deps.Orphans == nil {
		return
	}
	rows := buildOrphanInserts(libraryID, keys, grace, bookID)
	if err := r.deps.Orphans.Insert(ctx, rows); err != nil {
		slog.Warn("folder renamer: schedule rollback orphans",
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

// migrateToFolderLayout gives a flat-layout Book its own folder — the
// lazy migration of ADR-0003 §5, not a rename: it builds the first
// folder a Book has ever had, out of files sitting loose at the library
// root beside other Books' files.
//
// Only this Book's files move. Sibling files at the library root belong
// to other flat-layout Books and must not be swept along, which is the
// whole reason this cannot be a prefix move.
func (r *FolderRenamer) migrateToFolderLayout(
	ctx context.Context,
	b model.Book,
	handle *LibraryHandle,
	newFolder string,
) RenameOutcome {
	if handle.IsObjectStore() {
		// S3 BackendPlacer has always written {Author}/{Title} prefixes
		// (ADR-0003 §7), so a folder_path of "" on an S3-backed Book is
		// a pre-storage_v2 row or data corruption rather than a
		// flat-layout book. There is nothing to list and nothing to
		// migrate.
		return renameDeclined("object-store book has no flat layout to migrate")
	}
	// Library.Path again, the same reading the rename arm makes and for
	// the same reason (ADR-0030).
	root := newLibRoot(handle.Library.Path)
	if root.empty() {
		return renameDeclined("library has no root configured")
	}

	finalAbs, finalFolder, err := root.freeDir(newFolder)
	if err != nil {
		// Before this, an unusable newFolder probed from the library
		// root itself: it made a sibling of the root, moved the Book's
		// files into it, and wrote the absolute path to folder_path.
		slog.Warn("folder renamer: resolve migration folder",
			"book_id", b.ID, "folder", newFolder, "err", err)
		return renameBroke(err)
	}
	if err := os.MkdirAll(finalAbs, 0o755); err != nil {
		slog.Warn("folder renamer: mkdir new folder",
			"book_id", b.ID, "dir", finalAbs, "err", err)
		return renameBroke(err)
	}
	if err := r.moveFlatFiles(ctx, b, root, finalAbs); err != nil {
		return renameBroke(err)
	}
	if err := r.persist(ctx, b, finalFolder); err != nil {
		// The files moved and the DB still says otherwise. A known
		// soft-failure; scan reattach corrects it by content hash.
		return renameBroke(err)
	}
	return renameDone(finalFolder)
}

// moveFlatFiles handles the lazy-migration case for a legacy
// flat-layout Book. Moves every files row's on-disk entry from
// `{libRoot}/{basename}` into `{newDir}/{basename}` so we don't
// scoop up siblings that belong to other Books.
func (r *FolderRenamer) moveFlatFiles(
	ctx context.Context,
	b model.Book,
	root libRoot,
	newDir string,
) error {
	files, err := r.deps.Files.ListByBook(ctx, b.ID)
	if err != nil {
		slog.Warn("folder renamer: list files for flat move",
			"book_id", b.ID, "err", err)
		return err
	}
	// No rows is not the same as no files: an un-scanned Book still has
	// its primary path, and that is what there is to move.
	if len(files) == 0 {
		return moveSingleFlatFile(b, root, newDir)
	}
	for _, f := range files {
		// Flat-layout files live directly under the library root
		// with location = filename. Bail if a non-flat file shows
		// up — should not happen but keeps us honest.
		if strings.Contains(f.Location, "/") {
			slog.Warn("folder renamer: skipping non-flat file row in flat move",
				"book_id", b.ID, "location", f.Location)
			continue
		}
		from := root.abs(f.Location)
		to := filepath.Join(newDir, f.Location)
		if err := moveFile(from, to); err != nil {
			slog.Warn("folder renamer: move flat file",
				"book_id", b.ID, "from", from, "to", to, "err", err)
			return err
		}
	}
	return nil
}

// moveSingleFlatFile moves just the Book's primary file (b.Path) into
// the new folder, for a Book whose files have not been enumerated into
// rows yet.
//
// It used to double as the fallback for a nil Files repo. No production
// wiring produces one — internal/app always supplies it — so that branch
// was unreachable, and it was the only thing keeping the flat move able
// to proceed without the repo it needs (#212).
func moveSingleFlatFile(b model.Book, root libRoot, newDir string) error {
	base := filepath.Base(b.Path)
	if base == "" || base == "." || base == "/" {
		return fmt.Errorf("book %s has no usable primary path %q", b.ID, b.Path)
	}
	// books.path is mixed and stays mixed (ADR-0030): a legacy row holds
	// an absolute path, which is already where the file is.
	from := root.abs(b.Path)
	if filepath.IsAbs(b.Path) {
		from = b.Path
	}
	to := filepath.Join(newDir, base)
	if err := moveFile(from, to); err != nil {
		slog.Warn("folder renamer: move single flat file",
			"book_id", b.ID, "from", from, "to", to, "err", err)
		return err
	}
	return nil
}
