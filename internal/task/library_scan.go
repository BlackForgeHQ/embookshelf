// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"context"
	"errors"
	"log/slog"

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
	// Files is the storage_v2 file repo, which is what the worker hands
	// scan.Reconcile as its FileState. Required — nil → scan returns
	// early.
	Files *repo.FileRepo
}

// LibraryScan is the wiring around one library's drift scan (ADR-0018):
// resolve the library's handle, hand scan.Reconcile the walk and the
// files table, then touch the library's scan timestamp and log what
// happened. The policy — what a walk does to the files table, in which
// order, and the promise that it never creates a row — is
// scan.Reconcile's, where it can be tested without a database.
//
// What is decided here and not there is which walk failures abort the
// scan (see walkLibrary): an unconfigured local library is a state to
// report, not a job to retry, and it must never fall through to a
// reconcile with an empty listing, which would flag every row in the
// library missing.
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

	// The drift policy itself is scan.Reconcile's, over the five-method
	// view of the files table it needs. It snapshots the table, calls the
	// walk below, diffs, and acts; what comes back is counts, because by
	// then it has acted.
	rep, err := scan.Reconcile(ctx, scan.ReconcileInput{
		LibraryID: lib.ID,
		Walk:      walkLibrary(handle),
		Store:     handle.Storage,
		Files:     deps.Files,
	})
	switch {
	case errors.Is(err, service.ErrNoWalkRoot):
		// An unconfigured local Library, which is a state to report and
		// not a job to retry twenty-five times. It aborted the reconcile
		// before anything was written, which is the whole point:
		// falling through with nothing walked would flag every row in
		// the Library missing.
		slog.Warn("library scan: local library has no root configured, skipping",
			"library_id", lib.ID)
		return nil
	case err != nil:
		return err
	}

	if err := deps.Lib.TouchScan(ctx, lib.ID, rep.Walked, 0); err != nil {
		slog.Warn("library scan: touch", "id", lib.ID, "err", err)
	}
	slog.Info("library scan done",
		"library", lib.ID,
		"files", rep.Walked,
		"relocated", rep.Relocated,
		"missing", rep.Missing,
	)
	return nil
}

// walkLibrary is the listing the reconcile runs, once it has snapshotted
// the files table.
//
// The handle walks. Where the walk starts and whether its results need
// relativizing are questions about the Library's Backend, and they are
// answered once, there — this worker used to answer them itself and got
// it wrong for every S3 Library (#203). What comes back is
// library-relative, the same shape the files rows are stored in, so the
// reconcile is comparing like with like.
//
// Which walk failures abort the scan is decided here, because it is the
// only place that can tell them apart: an unconfigured local Library is
// a state to report and must abort, while a walk that failed partway
// still collected something worth reconciling — and a scan that treats
// either as an empty library flags every row in it missing.
func walkLibrary(handle *service.LibraryHandle) func(context.Context) ([]scan.WalkEntry, error) {
	return func(ctx context.Context) ([]scan.WalkEntry, error) {
		walked, err := handle.Walk(ctx)
		switch {
		case errors.Is(err, service.ErrNoWalkRoot):
			return nil, err
		case err != nil && !errors.Is(err, context.Canceled):
			slog.Warn("library scan: walk error",
				"library_id", handle.Library.ID, "err", err)
		}
		return walked, nil
	}
}
