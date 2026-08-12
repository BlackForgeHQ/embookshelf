// SPDX-License-Identifier: AGPL-3.0-or-later

package scan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
)

// FileState is every question and every write the drift reconcile makes
// of the files table, and nothing else. Five methods: read the library's
// rows, find rows by content, point a row at a new location, and raise
// or lower the missing flag.
//
// What is absent is the point. There is no Insert, so a scan cannot
// materialise a files row and could not be made to without widening this
// interface in a diff someone has to read — which is ADR-0018 (bookdrop
// is the only ingest path) expressed as a type rather than as a comment.
//
// *repo.FileRepo satisfies it. So does an in-memory fake, which is why
// the drift rules below are testable without a database.
type FileState interface {
	FileLocations

	// ListByLibrary returns every files row in one library, including
	// rows already flagged missing — a flagged row whose file reappears
	// is exactly what the reconcile has to notice.
	ListByLibrary(ctx context.Context, libraryID string) ([]model.File, error)
	// MarkMissing soft-flags a row for the 24h purge sweeper.
	MarkMissing(ctx context.Context, fileID string, when time.Time) error
	// ClearMissing lifts that flag.
	ClearMissing(ctx context.Context, fileID string) error
}

// ReconcileInput is one library's scan: how to list it, where its bytes
// can be read from, and the files table to reconcile against.
type ReconcileInput struct {
	// LibraryID scopes both the rows read and the content-hash matches
	// acted on: the same bytes in another library are another library's
	// book, not this one moving.
	LibraryID string
	// Walk lists the library, library-relative — what
	// service.LibraryHandle.Walk does. Required.
	//
	// It is a function and not a listing because *when* it runs is part
	// of the policy, and therefore belongs in here with the rest of it.
	// The files snapshot has to be taken before the walk starts, never
	// after: bookdrop places a book's bytes and only then inserts its
	// row (service.BookDropService.Approve), so a row read after the
	// walk can describe a file the walk had already passed the folder
	// of. That row is in no walked entry, lands in Missing, gets flagged,
	// and the 24h sweeper deletes it — a book with no files row pointing
	// at bytes that are right there, which is the #264 shape. Scans are
	// admin-triggered with no periodic timer, so nothing is guaranteed
	// to come along and clear the flag first.
	//
	// Taking the snapshot first closes it: a row created during the walk
	// is simply not in the snapshot, so it cannot be classified at all.
	// It is picked up, correctly, by the next scan.
	//
	// Returning an error aborts the scan before anything is written.
	// A caller that would rather reconcile a partial listing than abort
	// says so by logging and returning what it collected with a nil
	// error — the choice is the caller's because only the caller can
	// tell "the walk failed partway" from "this library is unconfigured",
	// and reconciling an empty listing flags every row in the library.
	Walk func(ctx context.Context) ([]WalkEntry, error)
	// Store reads bytes for the relocate-by-hash pass. A nil Store
	// disables that pass rather than failing the scan.
	Store storage.Storage
	// Files is the seam. Required.
	Files FileState
}

// ReconcileReport is what a scan did, for the caller's scan-touch and
// log line. Counts only: the reconcile has already acted.
type ReconcileReport struct {
	// Walked is how many entries the walk reported, which is what the
	// library's file count is touched with.
	Walked int
	// Relocated is how many rows an external rename moved.
	Relocated int
	// Missing is how many rows the walk did not account for — including
	// rows that were already flagged before this scan.
	Missing int
}

// Reconcile is the whole of the library-scan drift policy under ADR-0018:
// diff a walk against the files table and act on the difference, without
// ever creating anything.
//
//   - Unchanged — clear the missing flag if the row was carrying one.
//   - New — hash the bytes and look for a same-library row with that
//     content. A hit is an external rename: point the row at the new
//     location. A miss is ignored; scan is not an ingest path.
//   - Changed — no-op on the metadata, but clear the missing flag: the
//     walk just saw the file, and a stale flag hands a present file to
//     the purge sweeper. This is the arm the storage-v2 seeded rows land
//     in — seeded with size 0, they never match Unchanged (#264).
//   - Missing — soft-flag for the 24h purge sweeper, skipping rows this
//     same scan relocated and rows already flagged.
//
// Order is load-bearing twice, which is why both halves are in here
// rather than split across a caller:
//
//   - The files snapshot is read before the walk runs, so a row created
//     while the walk was in flight cannot be mistaken for a missing one
//     (see ReconcileInput.Walk).
//   - The relocate pass runs before the missing pass, because the
//     location a relocated row used to live at also comes back from this
//     scan as Missing, and flagging it would undo the relocate.
//
// Per-row write failures are logged and skipped — one row must not cost
// the rest of the library its scan. Only reading the files table and
// walking return an error, because both are ways of ending up with a
// listing that isn't the library, and acting on one of those flags rows
// that are perfectly fine.
func Reconcile(ctx context.Context, in ReconcileInput) (ReconcileReport, error) {
	if in.Files == nil {
		return ReconcileReport{}, errors.New("reconcile: no file state")
	}
	if in.Walk == nil {
		return ReconcileReport{}, errors.New("reconcile: no walk")
	}

	dbFiles, err := in.Files.ListByLibrary(ctx, in.LibraryID)
	if err != nil {
		return ReconcileReport{}, fmt.Errorf("list db files: %w", err)
	}

	// Strictly after the snapshot. Nothing has been written yet, so an
	// aborted walk leaves the library exactly as it found it.
	walked, err := in.Walk(ctx)
	if err != nil {
		return ReconcileReport{}, err
	}

	cs := Diff(walked, dbFiles)

	// Unchanged: clear missing flag if file reappeared.
	for _, f := range cs.Unchanged {
		clearMissing(ctx, in.Files, f)
	}

	// New: relocate by hash. A same-library content hit means the file
	// was renamed externally — point the existing row at the new
	// location. No book is materialised; under ADR-0018 scan never
	// ingests. The row ids it moved are what the Missing pass below has
	// to skip: the location they used to live at also shows up as
	// Missing in this same scan.
	relocated := RelocateByHash(ctx, in.Store, in.Files, in.LibraryID, cs.New)

	// Changed: no-op on the metadata. Under ADR-0018 in-app edits are the
	// only supported edit path; an external rewrite is out-of-scope and
	// won't be merged back into DB.
	//
	// The missing flag is not metadata, though, and clearing it is not
	// optional: a Changed row is a row whose file the walk just saw, and
	// leaving a stale flag on it hands a present file to the purge
	// sweeper.
	for _, ce := range cs.Changed {
		slog.Debug("library scan: changed file (no-op)", "loc", ce.Walk.Location)
		clearMissing(ctx, in.Files, ce.DB)
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
		if err := in.Files.MarkMissing(ctx, f.ID, time.Now()); err != nil {
			slog.Warn("library scan: mark missing", "id", f.ID, "err", err)
		}
	}

	return ReconcileReport{
		Walked:    len(walked),
		Relocated: len(relocated),
		Missing:   len(cs.Missing),
	}, nil
}

// clearMissing lifts the soft-delete flag off a row the walk just saw.
// A no-op when the row was never flagged, so both callers can hand it
// every row they hold rather than each deciding when the flag matters.
func clearMissing(ctx context.Context, files FileState, f model.File) {
	if f.MissingSince == nil {
		return
	}
	if err := files.ClearMissing(ctx, f.ID); err != nil {
		slog.Warn("library scan: clear missing", "id", f.ID, "err", err)
	}
}
