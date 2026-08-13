// SPDX-License-Identifier: AGPL-3.0-or-later

// Package scan is library drift detection: diff a listing of a
// library's storage against the files table and act on the difference.
// It is never an ingest path (ADR-0018) — Reconcile's seam, FileState,
// has no way to create a row.
//
// The listing itself is not here. Where a library's walk starts and how
// its keys become library-relative are questions about the Library's
// Backend, and service.LibraryHandle.Walk answers them; what arrives
// here is the answer, as a slice of WalkEntry.
package scan

import "time"

// WalkEntry is one observation from a storage backend during the
// cheap walk phase. Hashes are NOT computed here.
type WalkEntry struct {
	// Location is what the files table stores: a location relative to
	// the library root (CONTEXT, "Files row"). Producing it is
	// service.LibraryHandle.Walk's job — it is the only thing that knows
	// the root the listing ran under.
	Location string
	// Key is what the backend answers to for these bytes: the key it
	// listed the object under, carried through untouched. A caller that
	// needs to read an entry uses this rather than re-deriving a key
	// from Location, which on a "/"-rooted local backend means a round
	// trip back through the key shim on every single entry (ADR-0030 §2).
	Key   string
	Size  int64
	Mtime time.Time
	ETag  string
}
