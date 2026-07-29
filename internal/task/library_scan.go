// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/hashing"
	"github.com/blackforge/embookshelf/internal/jobs"
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

	// Where the walk starts, and it differs by Backend kind.
	//
	// An object store owns its own per-library prefix, so the walk starts
	// at the top of it and the keys come back already library-relative. A
	// local Backend is rooted at "/" for the whole instance (ADR-0030
	// §1), so the walk starts at the Library's own root and every entry
	// has to be relativized against it.
	//
	// The previous guard could not tell those apart: an S3 Library has no
	// root by design, that read as "not configured", and Library scan
	// silently skipped every one of them while reporting success (#203).
	prefix := ""
	if !handle.IsObjectStore() {
		prefix = lib.Path
		if lib.Root != nil && *lib.Root != "" {
			prefix = *lib.Root
		}
		if prefix == "" {
			slog.Warn("library scan: local library has no root configured, skipping",
				"library_id", lib.ID)
			return nil
		}
	}

	dbFiles, err := deps.Files.ListByLibrary(ctx, lib.ID)
	if err != nil {
		return errors.New("list db files: " + err.Error())
	}

	var walked []scan.WalkEntry
	entries, errc := scan.Walk(ctx, store, prefix)
	for e := range entries {
		if !handle.IsObjectStore() {
			// A "/"-rooted local Backend lists absolute paths with the
			// leading slash stripped; the DB stores library-relative
			// locations. An object store already lists them relative to
			// its own prefix, so there is nothing to strip.
			e.Location = handle.Relativize("/" + e.Location)
		}
		walked = append(walked, e)
	}
	if walkErr := <-errc; walkErr != nil && !errors.Is(walkErr, context.Canceled) {
		slog.Warn("library scan: walk error", "library_id", lib.ID, "err", walkErr)
	}

	cs := scan.Diff(walked, dbFiles)

	// Unchanged: clear missing flag if file reappeared.
	for _, f := range cs.Unchanged {
		if f.MissingSince != nil {
			if err := deps.Files.ClearMissing(ctx, f.ID); err != nil {
				slog.Warn("library scan: clear missing", "id", f.ID, "err", err)
			}
		}
	}

	// New: hash + relocate-by-hash. A same-library hash hit means the
	// file was renamed externally — point the existing row at the new
	// location. No book is materialised; under ADR-0018 scan never
	// ingests. Track relocated row IDs so the Missing pass below skips
	// them (the old location they used to live at also shows up as
	// Missing in this scan).
	relocated := make(map[string]struct{})
	for _, w := range cs.New {
		key := handle.StorageKey(w.Location)
		if !fileproc.IsSupported(key) {
			continue
		}
		hash, _, herr := hashing.HashFile(ctx, store, key)
		if herr != nil {
			slog.Warn("library scan: hash new entry", "loc", w.Location, "err", herr)
			continue
		}
		matches, mErr := deps.Files.GetByContentHash(ctx, hash)
		if mErr != nil {
			slog.Warn("library scan: lookup by hash", "loc", w.Location, "err", mErr)
			continue
		}
		for _, m := range matches {
			if m.LibraryID != lib.ID {
				continue
			}
			if err := deps.Files.UpdateLocation(ctx, m.ID, w.Location); err != nil {
				slog.Warn("library scan: relocate by hash", "id", m.ID, "loc", w.Location, "err", err)
				break
			}
			relocated[m.ID] = struct{}{}
			break
		}
	}

	// Changed: no-op. Under ADR-0018 in-app edits are the only supported
	// edit path; an external rewrite is out-of-scope and won't be merged
	// back into DB.
	for _, ce := range cs.Changed {
		slog.Debug("library scan: changed file (no-op)", "loc", ce.Walk.Location)
	}

	// Missing: soft-flag for the 24h purge sweeper. Skip rows that were
	// just relocated above.
	for _, f := range cs.Missing {
		if _, ok := relocated[f.ID]; ok {
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
		"library", lib.ID, "prefix", prefix, "object_store", handle.IsObjectStore(),
		"files", len(walked),
		"relocated", len(relocated),
		"missing", len(cs.Missing),
	)
	return nil
}
