// SPDX-License-Identifier: AGPL-3.0-or-later

package scan

import (
	"context"
	"log/slog"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/hashing"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
)

// FileLocations is the slice of the files repo relocate-by-hash needs:
// find rows by content, and point one at a new location.
type FileLocations interface {
	GetByContentHash(ctx context.Context, hash []byte) ([]model.File, error)
	UpdateLocation(ctx context.Context, fileID, newLocation string) error
}

// Relocated is the set of files rows a relocate pass pointed at a new
// location, by row id.
//
// It exists because the missing pass has to read it: the location a
// relocated row used to live at also comes back from this scan as
// Missing, and flagging it would undo the relocate one line later. That
// coupling is the reason the two used to be written as one block of
// inline worker code, and it is exactly what the return value has to
// carry for the extraction to be real rather than cosmetic.
type Relocated map[string]struct{}

// Has reports whether this scan already moved the row.
func (r Relocated) Has(fileID string) bool {
	_, ok := r[fileID]
	return ok
}

// RelocateByHash is the external-rename safety net under ADR-0018: for
// each walked entry the differ classified as New, hash the bytes and
// look for an existing files row in the same library with that content.
// A hit means the file was renamed outside the app — point the row at
// the new location. A miss means nothing: no book is materialised, and
// no bookdrop item is staged, because scan is never an ingest path.
//
// CONTEXT.md has named this "scan.RelocateByHash (or inline equivalent
// in task.LibraryScan)" since before it existed. It is the function now.
//
// Locations must already be library-relative — what the row's location
// column stores — and each entry's Key is what the backend is asked for
// the bytes. service.LibraryHandle.Walk produces both; this function
// derives neither, which is the point.
//
// Per-entry failures are logged and skipped: one unreadable file must
// not cost the rest of the library its rename detection.
func RelocateByHash(
	ctx context.Context,
	store storage.Storage,
	files FileLocations,
	libraryID string,
	entries []WalkEntry,
) Relocated {
	relocated := make(Relocated)
	if store == nil || files == nil {
		return relocated
	}
	for _, w := range entries {
		if !fileproc.IsSupported(w.Location) {
			continue
		}
		hash, _, err := hashing.HashFile(ctx, store, w.Key)
		if err != nil {
			slog.Warn("relocate by hash: hash new entry", "loc", w.Location, "err", err)
			continue
		}
		matches, err := files.GetByContentHash(ctx, hash)
		if err != nil {
			slog.Warn("relocate by hash: lookup by hash", "loc", w.Location, "err", err)
			continue
		}
		for _, m := range matches {
			// Same content in another library is another library's book,
			// not this one moving.
			if m.LibraryID != libraryID {
				continue
			}
			if err := files.UpdateLocation(ctx, m.ID, w.Location); err != nil {
				slog.Warn("relocate by hash: update location",
					"id", m.ID, "loc", w.Location, "err", err)
				break
			}
			relocated[m.ID] = struct{}{}
			break
		}
	}
	return relocated
}
