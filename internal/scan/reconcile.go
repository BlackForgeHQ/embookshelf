// SPDX-License-Identifier: AGPL-3.0-or-later

package scan

import (
	"context"
	"errors"
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

// ReconcileInput is one library's scan: what the walk saw, where its
// bytes can be read from, and the files table to reconcile against.
type ReconcileInput struct {
	// LibraryID scopes both the rows read and the content-hash matches
	// acted on: the same bytes in another library are another library's
	// book, not this one moving.
	LibraryID string
	// Walked is the library-relative listing, materialised — what
	// service.LibraryHandle.Walk returns. A partial listing is a caller's
	// decision to make; whatever arrives here is treated as the whole
	// library, and every row it does not mention reads Missing.
	Walked []WalkEntry
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
// The order matters and is load-bearing in one place: the relocate pass
// runs before the missing pass, because the location a relocated row used
// to live at also comes back from this scan as Missing, and flagging it
// would undo the relocate.
//
// Per-row write failures are logged and skipped — one row must not cost
// the rest of the library its scan. Only the initial read of the files
// table returns an error, because a scan that cannot see the table would
// otherwise flag the whole library missing.
func Reconcile(ctx context.Context, in ReconcileInput) (ReconcileReport, error) {
	if in.Files == nil {
		return ReconcileReport{}, errors.New("reconcile: no file state")
	}

	dbFiles, err := in.Files.ListByLibrary(ctx, in.LibraryID)
	if err != nil {
		return ReconcileReport{}, errors.New("list db files: " + err.Error())
	}

	cs := Diff(in.Walked, dbFiles)

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
		Walked:    len(in.Walked),
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
