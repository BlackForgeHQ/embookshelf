package task

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/riverqueue/river"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/hashing"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/scan"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/sidecar"
	"github.com/blackforge/embookshelf/internal/storage"
)

// LibraryScanArgs is the payload for walking a library's filesystem
// root. The library id also names the scan — each library owns
// exactly one path since migration 000018.
type LibraryScanArgs struct {
	LibraryID string `json:"library_id"`
}

func (LibraryScanArgs) Kind() string { return "library.scan" }

// BookDropEnqueuer is the slice of the queue client that the scan
// task needs. Defined here so the task package doesn't import queue
// (avoids the queue↔task cycle — queue already imports task to
// register workers).
type BookDropEnqueuer interface {
	EnqueueBookDrop(ctx context.Context, itemID string) error
	EnqueueScanImport(ctx context.Context, args ScanImportArgs) error
}

// LibraryScanDeps groups the services LibraryScan needs, plus the
// enqueuer used to schedule child BookDropIngest jobs for newly
// discovered files.
type LibraryScanDeps struct {
	BookDrop *service.BookDropService
	Lib      *service.LibraryService
	Queue    BookDropEnqueuer
	// LibStore turns a libraryID into a ready-to-use {Library, Storage}
	// view. Required — without it LibraryScan returns early.
	LibStore service.LibraryStore
	// Files is the storage_v2 file repo. Required by the two-phase
	// walk+diff pipeline. nil → scan returns early.
	Files *repo.FileRepo
	// Books is the book repo used by the lock-aware re-extract path
	// to load the current book row and persist the merged metadata.
	// nil → scan skips re-extract and falls back to the legacy
	// hash-only update on Changed files.
	Books *repo.BookRepo
	// LockMerger applies the lock-aware merge of file-extracted
	// metadata onto a DB row. Default service.MergeLocked.
	LockMerger func(current, extracted model.Book) model.Book
}

// LibraryScan walks a library's filesystem root and stages every
// unseen supported file into the bookdrop queue. It does not
// extract metadata — that's BookDropIngest's job, fired for each
// enqueued item. Returning an error from this function asks the
// caller to retry the whole scan; per-file errors are logged and
// skipped without aborting.
func LibraryScan(ctx context.Context, args LibraryScanArgs, deps LibraryScanDeps) error {
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
	root := lib.Path
	if lib.Root != nil && *lib.Root != "" {
		root = *lib.Root
	}
	if root == "" {
		slog.Warn("library scan: empty root, skipping", "library_id", lib.ID)
		return nil
	}

	// Phase 1: read DB state.
	dbFiles, err := deps.Files.ListByLibrary(ctx, lib.ID)
	if err != nil {
		return errors.New("list db files: " + err.Error())
	}

	// Phase 2: walk storage and collect WalkEntry slice.
	var walked []scan.WalkEntry
	entries, errc := scan.Walk(ctx, store, root)
	for e := range entries {
		// Convert the absolute key (LocalFS rooted at "/") to library-
		// relative location so it matches what the DB stores.
		e.Location = handle.Relativize("/" + e.Location)
		walked = append(walked, e)
	}
	if walkErr := <-errc; walkErr != nil && !errors.Is(walkErr, context.Canceled) {
		slog.Warn("library scan: walk error", "library_id", lib.ID, "err", walkErr)
	}

	// Phase 3: diff.
	cs := scan.Diff(walked, dbFiles)

	// Phase 4: act on each category.
	fileCount := len(walked)
	discovered := 0

	// Unchanged: clear missing flag if file reappeared.
	for _, f := range cs.Unchanged {
		if f.MissingSince != nil {
			if err := deps.Files.ClearMissing(ctx, f.ID); err != nil {
				slog.Warn("library scan: clear missing", "id", f.ID, "err", err)
			}
		}
	}

	// Changed: re-hash and update the row in place (or reattach on rename).
	for _, ce := range cs.Changed {
		key := joinKey(root, ce.Walk.Location)
		hash, size, herr := hashing.HashFile(ctx, store, key)
		if herr != nil {
			slog.Warn("library scan: rehash failed", "loc", ce.Walk.Location, "err", herr)
			continue
		}

		// Hash-stamp short-circuit: if the file's actual hash matches
		// what the DB already recorded, this is "we just wrote it" —
		// no metadata re-extract needed. Either the scan tick raced
		// the MetadataWriter's hash-stamp call (rare) or scan was
		// triggered by an mtime change without bytes changing.
		if len(ce.DB.ContentHash) > 0 && bytes.Equal(hash, ce.DB.ContentHash) {
			continue
		}

		reattached, rerr := scan.MaybeReattach(ctx, deps.Files, lib.ID, hash, ce.Walk.Location, ce.DB.ID)
		if rerr != nil {
			slog.Warn("library scan: reattach failed", "loc", ce.Walk.Location, "err", rerr)
		} else if reattached {
			continue
		}

		// External edit (or first-time hash on a never-stamped row):
		// re-extract and merge with locks.
		if deps.Books != nil && deps.LockMerger != nil {
			reExtractAndMerge(ctx, deps, ce.DB.BookID, key, store, handle)
		}

		if err := deps.Files.SetContentHash(ctx, ce.DB.ID, hash, size, ce.Walk.Mtime); err != nil {
			slog.Warn("library scan: update changed row", "id", ce.DB.ID, "err", err)
		}
	}

	// New: classify per ADR-0003 §3 to split root-depth flat files
	// (legacy single-file Books — still go through bookdrop staging
	// for back-compat) from LeafBook folder groupings (auto-imported
	// directly via ScanImport, ADR-0004 §1).
	cls := scan.Classify(cs.New, func(loc string) bool {
		return fileproc.IsSupported("/" + joinKey(root, loc))
	})

	// LeafBooks: one ScanImport job per folder.
	for _, lb := range cls.LeafBooks {
		if deps.Queue == nil {
			break
		}
		args := ScanImportArgs{
			LibraryID: lib.ID,
			Folder:    lb.Folder,
			Mixed:     lb.Mixed,
			Files:     make([]ScanImportFile, len(lb.Files)),
		}
		for i, f := range lb.Files {
			args.Files[i] = ScanImportFile{
				Location: f.Location,
				Size:     f.Size,
				Mtime:    f.Mtime,
			}
		}
		if err := deps.Queue.EnqueueScanImport(ctx, args); err != nil {
			slog.Warn("library scan: enqueue scan import",
				"library_id", lib.ID, "folder", lb.Folder, "err", err)
			continue
		}
		discovered += len(lb.Files)
	}

	// Flat files: legacy single-file Books, still routed through the
	// bookdrop staging queue. Lazy-migration §5 picks them up into the
	// new layout when their owners edit metadata.
	for _, w := range cls.Flat {
		absPath := joinKey(root, w.Location)
		format := fileproc.FormatForExt(filepath.Ext(w.Location))
		fullPath := "/" + absPath
		item, created, err := deps.BookDrop.Enqueue(ctx, fullPath, format, w.Size)
		if err != nil {
			slog.Warn("library scan: enqueue", "path", fullPath, "err", err)
			continue
		}
		if !created {
			continue
		}
		if deps.Queue != nil {
			if err := deps.Queue.EnqueueBookDrop(ctx, item.ID); err != nil {
				slog.Warn("library scan: enqueue queue job", "id", item.ID, "err", err)
			}
		}
		discovered++
	}

	// Missing: soft-flag files for the 24h purge sweeper.
	for _, f := range cs.Missing {
		if f.MissingSince != nil {
			continue // already marked
		}
		if err := deps.Files.MarkMissing(ctx, f.ID, time.Now()); err != nil {
			slog.Warn("library scan: mark missing", "id", f.ID, "err", err)
		}
	}

	if err := deps.Lib.TouchScan(ctx, lib.ID, fileCount, discovered); err != nil {
		slog.Warn("library scan: touch", "id", lib.ID, "err", err)
	}
	slog.Info("library scan done",
		"library", lib.ID, "root", root,
		"files", fileCount, "discovered", discovered,
		"changed", len(cs.Changed), "missing", len(cs.Missing),
	)
	return nil
}

// LibraryScanWorker is the River adapter for LibraryScan.
type LibraryScanWorker struct {
	river.WorkerDefaults[LibraryScanArgs]
	Deps LibraryScanDeps
}

func (w *LibraryScanWorker) Work(ctx context.Context, job *river.Job[LibraryScanArgs]) error {
	return LibraryScan(ctx, job.Args, w.Deps)
}

// reExtractAndMerge runs the file-format-specific extractor on a
// changed file, overlays the sidecar, and applies the lock-aware
// merge into DB. Best-effort: errors are logged, never aborting
// the scan.
func reExtractAndMerge(
	ctx context.Context,
	deps LibraryScanDeps,
	bookID, fileKey string,
	store storage.Storage,
	handle *service.LibraryHandle,
) {
	_ = handle
	if bookID == "" {
		return
	}
	src, err := store.Open(ctx, fileKey)
	if err != nil {
		slog.Warn("library scan: open for re-extract", "key", fileKey, "err", err)
		return
	}
	defer func() { _ = src.Close() }()

	proc, _, err := fileproc.Dispatch(fileKey)
	if err != nil {
		slog.Debug("library scan: no processor for re-extract", "key", fileKey, "err", err)
	}
	var meta fileproc.Metadata
	if proc != nil {
		meta, err = proc.Extract(ctx, src)
		if err != nil {
			slog.Warn("library scan: extract failed", "key", fileKey, "err", err)
		}
	}

	side, sErr := sidecar.Read(ctx, store, fileKey)
	if sErr != nil {
		slog.Warn("library scan: sidecar read", "key", fileKey, "err", sErr)
	}

	// Build the extracted shape from processor + sidecar overlay.
	// Sidecar wins on conflicts (model.MergeEditable b-wins-on-non-zero).
	processorEM := model.EditableMetadata{
		Title:       meta.Title,
		Author:      meta.Author,
		Description: meta.Description,
		Language:    meta.Language,
	}
	extractedEM := model.MergeEditable(processorEM, side)

	var extracted model.Book
	extracted.ApplyEditable(extractedEM)

	current, err := deps.Books.GetByID(ctx, "", bookID)
	if err != nil {
		slog.Warn("library scan: load current book", "book_id", bookID, "err", err)
		return
	}
	merged := deps.LockMerger(current, extracted)
	if err := deps.Books.UpdateMetadata(ctx, merged); err != nil {
		slog.Warn("library scan: update merged metadata", "book_id", bookID, "err", err)
	}
}
