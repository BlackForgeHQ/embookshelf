// SPDX-License-Identifier: AGPL-3.0-or-later

package scan

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
)

// changeset is the output of Diff: the four ways a walked listing and a
// library's files rows can disagree. Each slice holds the WalkEntry +
// the matching DB row (for Changed) or just the WalkEntry (for New) /
// just the model.File (for Unchanged and Missing).
//
// Unexported because acting on it is Reconcile's job and no caller
// outside this package has one — the drift policy that reads these four
// buckets lives here now. Diff still returns it so the classification
// stays testable on its own; a test can hold the value and read its
// fields without being able to name the type, which is the whole of what
// a test of a classifier needs.
type changeset struct {
	// Unchanged: row exists, size+mtime match. No work needed.
	Unchanged []model.File
	// Changed: row exists but size or mtime differ.
	Changed []changedEntry
	// New: walked but not in DB — a rename candidate, never an ingest.
	New []WalkEntry
	// Missing: in DB but not walked — mark missing_since.
	Missing []model.File
}

// changedEntry pairs the live observation with the stale DB row.
type changedEntry struct {
	Walk WalkEntry
	DB   model.File
}

// Diff computes the changeset comparing walked entries against the
// current DB rows for one library. Both slices may be in any order;
// Diff sorts internally by Location.
func Diff(walked []WalkEntry, dbFiles []model.File) changeset {
	// Build a map of DB rows by Location for O(1) lookup.
	byLoc := make(map[string]model.File, len(dbFiles))
	for _, f := range dbFiles {
		k := objectKey(f.Location)
		// Two rows can name the same object when one holds the legacy
		// absolute form and one the library-relative form — the collision
		// ADR-0030 anticipates. The relative row is the live one, so it
		// wins the lookup and the absolute duplicate reads Missing, which
		// is the resolution the ADR already agreed to.
		if prev, dup := byLoc[k]; dup && filepath.IsAbs(f.Location) && !filepath.IsAbs(prev.Location) {
			continue
		}
		byLoc[k] = f
	}

	var cs changeset
	// Matched by row id, not by location: a row can be matched under a
	// location string that is not the one it stores.
	matched := make(map[string]bool, len(walked))
	for _, w := range walked {
		f, ok := match(byLoc, w)
		if !ok {
			cs.New = append(cs.New, w)
			continue
		}
		matched[f.ID] = true
		// Compare size + mtime (truncated to whole seconds — local FS
		// and S3 both report at ≥1s resolution). Float ETags would
		// fail equality; we strip subsecond precision deliberately.
		if w.Size == f.Size && sameSecond(w.Mtime, f.Mtime) {
			cs.Unchanged = append(cs.Unchanged, f)
			continue
		}
		cs.Changed = append(cs.Changed, changedEntry{Walk: w, DB: f})
	}
	for _, f := range dbFiles {
		if !matched[f.ID] {
			cs.Missing = append(cs.Missing, f)
		}
	}
	return cs
}

// match finds the DB row a walk entry describes, in either vocabulary
// the location column is written in.
//
// files.location is library-relative (CONTEXT, "Files row") and that is
// what the walk reports, so Location is tried first and is the answer
// for every row any live write path produced. Rows holding an absolute
// location also exist, from one producer — migrator.seedFilesFromBooks
// writes books.path verbatim when the library root was unknown at seed
// time — and ADR-0030 §1 declines to migrate them. For those, what the
// row stores is the *backend key*, which is the whole content of
// LibraryHandle.StorageKey's absolute branch: on a "/"-rooted local
// backend an absolute location is already the key. The walk carries that
// key on every entry, so the second lookup asks the same question in the
// same vocabulary rather than reconstructing a library root the differ
// does not have.
//
// Without it a seeded row read Missing while the very bytes it describes
// read New, on every scan, and the 24h purge sweeper deleted it — the
// hash-relocate rescues only rows that carry a content hash, and these
// carry none (#264).
//
// The rejected alternative was to backfill the hash instead, so relocate
// could rescue those rows like any other. task.RunFilesBackfill already
// does exactly that, and already handles the absolute location
// (TestRunFilesBackfill_hashesALegacyAbsoluteLocation), so the option was
// not "cheaper" — it was already shipped, and the bug survived it. It
// survived because coverage was never the gap: the backfill is a
// best-effort background goroutine with no ordering against the scan
// queue, so a scan that wins the race flags the row first, and nothing
// downstream lifts that flag — relocate skips a row it moved in the
// Missing pass but never clears missing_since. Its failure mode is also
// the wrong way round: an unreadable file or a storage blip leaves the
// hash NULL, and the row is then deleted. Depending on a hash is
// depending on having read every byte in the library to answer a question
// about two names.
func match(byLoc map[string]model.File, w WalkEntry) (model.File, bool) {
	if f, ok := byLoc[objectKey(w.Location)]; ok {
		return f, true
	}
	if w.Key == "" || w.Key == w.Location {
		return model.File{}, false
	}
	f, ok := byLoc[objectKey(w.Key)]
	return f, ok
}

// objectKey is the form two strings have to agree in to name the same
// object. Only the leading slash is normalised, for the reason
// storagetest.KeyShapesNameTheSameObject pins: a "/"-rooted backend
// answers to "/a/b" but reports "a/b", so a stored "/srv/lib/x.epub" and
// a listed "srv/lib/x.epub" are one file. Nothing else is cleaned —
// making this fuzzier would start matching rows that name different
// objects, and a wrong match here silently reattaches a books row to
// someone else's bytes.
func objectKey(location string) string {
	return strings.TrimPrefix(location, "/")
}

// sameSecond returns true when a and b are equal after truncating both
// to whole-second precision. This guards against sub-second drift
// between reading mtime from the local FS and reading it back from the
// DB as an ISO-8601 string.
func sameSecond(a, b time.Time) bool {
	return a.Truncate(time.Second).Equal(b.Truncate(time.Second))
}
