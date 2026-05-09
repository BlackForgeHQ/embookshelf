// SPDX-License-Identifier: AGPL-3.0-or-later

package scan

import (
	"time"

	"github.com/blackforge/embookshelf/internal/model"
)

// Changeset is the output of Diff. Each slice holds the WalkEntry +
// the matching DB row (for Changed and Missing) or just the
// WalkEntry (for New) / just the model.File (for Missing).
type Changeset struct {
	// Unchanged: row exists, size+mtime match. No work needed.
	Unchanged []model.File
	// Changed: row exists but size or mtime differ — re-hash + update.
	Changed []ChangedEntry
	// New: walked but not in DB — enqueue ingest.
	New []WalkEntry
	// Missing: in DB but not walked — mark missing_since.
	Missing []model.File
}

// ChangedEntry pairs the live observation with the stale DB row.
type ChangedEntry struct {
	Walk WalkEntry
	DB   model.File
}

// Diff computes the changeset comparing walked entries against the
// current DB rows for one library. Both slices may be in any order;
// Diff sorts internally by Location.
func Diff(walked []WalkEntry, dbFiles []model.File) Changeset {
	// Build a map of DB rows by Location for O(1) lookup.
	byLoc := make(map[string]model.File, len(dbFiles))
	for _, f := range dbFiles {
		byLoc[f.Location] = f
	}

	var cs Changeset
	seen := make(map[string]bool, len(walked))
	for _, w := range walked {
		seen[w.Location] = true
		f, ok := byLoc[w.Location]
		if !ok {
			cs.New = append(cs.New, w)
			continue
		}
		// Compare size + mtime (truncated to whole seconds — local FS
		// and S3 both report at ≥1s resolution). Float ETags would
		// fail equality; we strip subsecond precision deliberately.
		if w.Size == f.Size && sameSecond(w.Mtime, f.Mtime) {
			cs.Unchanged = append(cs.Unchanged, f)
			continue
		}
		cs.Changed = append(cs.Changed, ChangedEntry{Walk: w, DB: f})
	}
	for _, f := range dbFiles {
		if !seen[f.Location] {
			cs.Missing = append(cs.Missing, f)
		}
	}
	return cs
}

// sameSecond returns true when a and b are equal after truncating both
// to whole-second precision. This guards against sub-second drift
// between reading mtime from the local FS and reading it back from the
// DB as an ISO-8601 string.
func sameSecond(a, b time.Time) bool {
	return a.Truncate(time.Second).Equal(b.Truncate(time.Second))
}
