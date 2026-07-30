// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/scan"
	"github.com/blackforge/embookshelf/internal/service"
)

// LibraryScanDeps groups the services LibraryScan needs.
type LibraryScanDeps struct {
	Lib *service.LibraryService
	// LibStore turns a libraryID into a ready-to-use {Library, Storage}
	// view. Required — without it LibraryScan returns early.
	LibStore service.LibraryStore
	// Files is the storage_v2 file repo. Required by the walk+diff pipeline.
	// nil → scan returns early.
	Files *repo.FileRepo
}

// LibraryScan walks a library's storage and reconciles drift between
// disk and the files table (ADR-0018). It never creates books and never
// stages bookdrop items — bookdrop is the only ingest path. Three
// behaviors:
//
//   - New entry whose hash matches an existing files row in the same
//     library: treated as an external rename, location updated.
//   - Missing entry (DB row whose path no longer walks): soft-flagged
//     for the 24h purge sweeper.
//   - Changed / Unchanged entries: no-op apart from clearing
//     missing_since on reappearance.
//
// Per-file errors are logged and skipped. Returning an error asks the
// caller to retry the whole scan.
func LibraryScan(ctx context.Context, args jobs.LibraryScanArgs, deps LibraryScanDeps) error {
	if deps.LibStore == nil || deps.Files == nil {
		slog.Warn("library scan: not wired (missing LibStore or Files)",
			"library_id", args.LibraryID)
		return nil
	}
	handle, err := deps.LibStore.For(ctx, args.LibraryID)
	if err != nil {
		return err
	}
	lib := handle.Library
	if handle.Storage == nil {
		slog.Warn("library scan: no storage for library, skipping", "library_id", lib.ID)
		return nil
	}
	store := handle.Storage

	dbFiles, err := deps.Files.ListByLibrary(ctx, lib.ID)
	if err != nil {
		return errors.New("list db files: " + err.Error())
	}

	// The handle walks. Where the walk starts and whether its results
	// need relativizing are questions about the Library's Backend, and
	// they are answered once, there — this worker used to answer them
	// itself and got it wrong for every S3 Library (#203). What comes
	// back is library-relative, the same shape the files rows below are
	// stored in, so the differ is comparing like with like.
	walked, walkErr := handle.Walk(ctx)
	switch {
	case errors.Is(walkErr, service.ErrNoWalkRoot):
		// An unconfigured local Library, which is a state to report and
		// not a job to retry twenty-five times. Distinctly not the same
		// as an empty walk: falling through with nothing walked would
		// flag every row in the Library missing.
		slog.Warn("library scan: local library has no root configured, skipping",
			"library_id", lib.ID)
		return nil
	case walkErr != nil && !errors.Is(walkErr, context.Canceled):
		slog.Warn("library scan: walk error", "library_id", lib.ID, "err", walkErr)
	}

	cs := scan.Diff(walked, dbFiles)

	// Unchanged: clear missing flag if file reappeared.
	for _, f := range cs.Unchanged {
		clearMissing(ctx, deps.Files, f)
	}

	// New: relocate by hash. A same-library content hit means the file
	// was renamed externally — point the existing row at the new
	// location. No book is materialised; under ADR-0018 scan never
	// ingests. The row ids it moved are what the Missing pass below has
	// to skip: the location they used to live at also shows up as
	// Missing in this same scan.
	relocated := scan.RelocateByHash(ctx, store, deps.Files, lib.ID, cs.New)

	// Changed: no-op on the metadata. Under ADR-0018 in-app edits are the
	// only supported edit path; an external rewrite is out-of-scope and
	// won't be merged back into DB.
	//
	// The missing flag is not metadata, though, and clearing it is not
	// optional: a Changed row is a row whose file the walk just saw, and
	// leaving a stale flag on it hands a present file to the purge
	// sweeper. This is the arm that carries the migrated rows — they were
	// seeded with size 0, so a real file never matches and they land here
	// rather than in Unchanged, which is the only arm that used to clear
	// (#264).
	for _, ce := range cs.Changed {
		slog.Debug("library scan: changed file (no-op)", "loc", ce.Walk.Location)
		clearMissing(ctx, deps.Files, ce.DB)
	}

	// Missing: soft-flag for the 24h purge sweeper. Skip rows that were
	// just relocated above.
	for _, f := range cs.Missing {
		if relocated.Has(f.ID) {
			continue
		}
		if f.MissingSince != nil {
			continue
		}
		if err := deps.Files.MarkMissing(ctx, f.ID, time.Now()); err != nil {
			slog.Warn("library scan: mark missing", "id", f.ID, "err", err)
		}
	}

	if err := deps.Lib.TouchScan(ctx, lib.ID, len(walked), 0); err != nil {
		slog.Warn("library scan: touch", "id", lib.ID, "err", err)
	}
	slog.Info("library scan done",
		"library", lib.ID,
		"files", len(walked),
		"relocated", len(relocated),
		"missing", len(cs.Missing),
	)
	return nil
}

// clearMissing lifts the soft-delete flag off a row the walk just saw.
// A no-op when the row was never flagged, so both callers can hand it
// every row they hold rather than each deciding when the flag matters.
func clearMissing(ctx context.Context, files *repo.FileRepo, f model.File) {
	if f.MissingSince == nil {
		return
	}
	if err := files.ClearMissing(ctx, f.ID); err != nil {
		slog.Warn("library scan: clear missing", "id", f.ID, "err", err)
	}
}
