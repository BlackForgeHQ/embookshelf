// SPDX-License-Identifier: AGPL-3.0-or-later

package scan_test

import (
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/scan"
)

// baseTime is a fixed reference time used across subtests.
var baseTime = time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)

func makeFile(id, loc string, size int64, mtime time.Time) model.File {
	return model.File{
		ID:        id,
		LibraryID: "lib1",
		Location:  loc,
		Size:      size,
		Mtime:     mtime,
		Format:    "EPUB",
	}
}

func makeEntry(loc string, size int64, mtime time.Time) scan.WalkEntry {
	return scan.WalkEntry{
		Location: loc,
		Size:     size,
		Mtime:    mtime,
	}
}

// makeKeyedEntry is a walk entry in the shape service.LibraryHandle.Walk
// actually produces: a library-relative Location, plus the key the
// backend listed the object under. On a local library those differ —
// the backend is rooted at "/" for the whole instance (ADR-0030 §1) — and
// the difference is what lets the differ recognise a row that stores the
// key rather than the relative location.
func makeKeyedEntry(loc, key string, size int64, mtime time.Time) scan.WalkEntry {
	e := makeEntry(loc, size, mtime)
	e.Key = key
	return e
}

func TestDiff_BothEmpty(t *testing.T) {
	cs := scan.Diff(nil, nil)
	if len(cs.Unchanged) != 0 {
		t.Errorf("Unchanged: want 0, got %d", len(cs.Unchanged))
	}
	if len(cs.Changed) != 0 {
		t.Errorf("Changed: want 0, got %d", len(cs.Changed))
	}
	if len(cs.New) != 0 {
		t.Errorf("New: want 0, got %d", len(cs.New))
	}
	if len(cs.Missing) != 0 {
		t.Errorf("Missing: want 0, got %d", len(cs.Missing))
	}
}

func TestDiff_AllMatching(t *testing.T) {
	mtime := baseTime
	walked := []scan.WalkEntry{
		makeEntry("a.epub", 100, mtime),
		makeEntry("b.epub", 200, mtime),
	}
	dbFiles := []model.File{
		makeFile("id1", "a.epub", 100, mtime),
		makeFile("id2", "b.epub", 200, mtime),
	}

	cs := scan.Diff(walked, dbFiles)

	if len(cs.Unchanged) != 2 {
		t.Errorf("Unchanged: want 2, got %d", len(cs.Unchanged))
	}
	if len(cs.Changed) != 0 {
		t.Errorf("Changed: want 0, got %d", len(cs.Changed))
	}
	if len(cs.New) != 0 {
		t.Errorf("New: want 0, got %d", len(cs.New))
	}
	if len(cs.Missing) != 0 {
		t.Errorf("Missing: want 0, got %d", len(cs.Missing))
	}
}

func TestDiff_OneNew(t *testing.T) {
	walked := []scan.WalkEntry{
		makeEntry("new.epub", 500, baseTime),
	}
	var dbFiles []model.File

	cs := scan.Diff(walked, dbFiles)

	if len(cs.New) != 1 {
		t.Fatalf("New: want 1, got %d", len(cs.New))
	}
	if cs.New[0].Location != "new.epub" {
		t.Errorf("New[0].Location = %q, want new.epub", cs.New[0].Location)
	}
	if len(cs.Unchanged) != 0 || len(cs.Changed) != 0 || len(cs.Missing) != 0 {
		t.Errorf("expected only New entries, got Unchanged=%d Changed=%d Missing=%d",
			len(cs.Unchanged), len(cs.Changed), len(cs.Missing))
	}
}

func TestDiff_OneMissing(t *testing.T) {
	var walked []scan.WalkEntry
	dbFiles := []model.File{
		makeFile("id1", "gone.epub", 300, baseTime),
	}

	cs := scan.Diff(walked, dbFiles)

	if len(cs.Missing) != 1 {
		t.Fatalf("Missing: want 1, got %d", len(cs.Missing))
	}
	if cs.Missing[0].Location != "gone.epub" {
		t.Errorf("Missing[0].Location = %q, want gone.epub", cs.Missing[0].Location)
	}
	if len(cs.Unchanged) != 0 || len(cs.Changed) != 0 || len(cs.New) != 0 {
		t.Errorf("expected only Missing entries, got Unchanged=%d Changed=%d New=%d",
			len(cs.Unchanged), len(cs.Changed), len(cs.New))
	}
}

func TestDiff_ChangedBySize(t *testing.T) {
	mtime := baseTime
	walked := []scan.WalkEntry{
		makeEntry("book.epub", 999, mtime), // size differs from DB
	}
	dbFiles := []model.File{
		makeFile("id1", "book.epub", 100, mtime),
	}

	cs := scan.Diff(walked, dbFiles)

	if len(cs.Changed) != 1 {
		t.Fatalf("Changed: want 1, got %d", len(cs.Changed))
	}
	if cs.Changed[0].Walk.Location != "book.epub" {
		t.Errorf("Changed[0].Walk.Location = %q, want book.epub", cs.Changed[0].Walk.Location)
	}
	if cs.Changed[0].DB.ID != "id1" {
		t.Errorf("Changed[0].DB.ID = %q, want id1", cs.Changed[0].DB.ID)
	}
	if len(cs.Unchanged) != 0 || len(cs.New) != 0 || len(cs.Missing) != 0 {
		t.Errorf("expected only Changed entries")
	}
}

func TestDiff_ChangedByMtimeMoreThanOneSecond(t *testing.T) {
	walked := []scan.WalkEntry{
		makeEntry("book.epub", 100, baseTime.Add(2*time.Second)),
	}
	dbFiles := []model.File{
		makeFile("id1", "book.epub", 100, baseTime),
	}

	cs := scan.Diff(walked, dbFiles)

	if len(cs.Changed) != 1 {
		t.Fatalf("Changed: want 1 (mtime >1s apart), got %d", len(cs.Changed))
	}
	if len(cs.Unchanged) != 0 {
		t.Errorf("Unchanged: want 0, got %d", len(cs.Unchanged))
	}
}

func TestDiff_UnchangedByMtimeLessThanOneSecond(t *testing.T) {
	// Two timestamps that differ by only 500ms should be considered the same
	// after truncation to the second.
	t1 := baseTime
	t2 := baseTime.Add(500 * time.Millisecond)

	walked := []scan.WalkEntry{
		makeEntry("book.epub", 100, t2),
	}
	dbFiles := []model.File{
		makeFile("id1", "book.epub", 100, t1),
	}

	cs := scan.Diff(walked, dbFiles)

	if len(cs.Unchanged) != 1 {
		t.Fatalf("Unchanged: want 1 (mtime <1s apart), got %d", len(cs.Unchanged))
	}
	if len(cs.Changed) != 0 {
		t.Errorf("Changed: want 0 (sub-second diff should be ignored), got %d", len(cs.Changed))
	}
}

func TestDiff_Mixed(t *testing.T) {
	mtime := baseTime

	// Walked: a (match), b (size changed), c (new), d is absent (missing)
	walked := []scan.WalkEntry{
		makeEntry("a.epub", 100, mtime),  // unchanged
		makeEntry("b.epub", 9999, mtime), // changed: size differs
		makeEntry("e.epub", 100, mtime),  // unchanged second
		makeEntry("c.epub", 400, mtime),  // new
	}
	dbFiles := []model.File{
		makeFile("id-a", "a.epub", 100, mtime), // unchanged
		makeFile("id-b", "b.epub", 100, mtime), // changed
		makeFile("id-e", "e.epub", 100, mtime), // unchanged second
		makeFile("id-d", "d.epub", 300, mtime), // missing
	}

	cs := scan.Diff(walked, dbFiles)

	if len(cs.Unchanged) != 2 {
		t.Errorf("Unchanged: want 2, got %d", len(cs.Unchanged))
	}
	if len(cs.Changed) != 1 {
		t.Errorf("Changed: want 1, got %d", len(cs.Changed))
	}
	if len(cs.New) != 1 {
		t.Errorf("New: want 1, got %d", len(cs.New))
	}
	if len(cs.Missing) != 1 {
		t.Errorf("Missing: want 1, got %d", len(cs.Missing))
	}

	if cs.Changed[0].DB.ID != "id-b" {
		t.Errorf("Changed entry should be id-b, got %q", cs.Changed[0].DB.ID)
	}
	if cs.New[0].Location != "c.epub" {
		t.Errorf("New entry should be c.epub, got %q", cs.New[0].Location)
	}
	if cs.Missing[0].ID != "id-d" {
		t.Errorf("Missing entry should be id-d, got %q", cs.Missing[0].ID)
	}
}

func TestDiff_AllNew(t *testing.T) {
	walked := []scan.WalkEntry{
		makeEntry("x.epub", 100, baseTime),
		makeEntry("y.epub", 200, baseTime),
	}

	cs := scan.Diff(walked, nil)

	if len(cs.New) != 2 {
		t.Errorf("New: want 2, got %d", len(cs.New))
	}
	if len(cs.Unchanged) != 0 || len(cs.Changed) != 0 || len(cs.Missing) != 0 {
		t.Errorf("expected only New entries")
	}
}

func TestDiff_AllMissing(t *testing.T) {
	dbFiles := []model.File{
		makeFile("id1", "x.epub", 100, baseTime),
		makeFile("id2", "y.epub", 200, baseTime),
	}

	cs := scan.Diff(nil, dbFiles)

	if len(cs.Missing) != 2 {
		t.Errorf("Missing: want 2, got %d", len(cs.Missing))
	}
	if len(cs.Unchanged) != 0 || len(cs.Changed) != 0 || len(cs.New) != 0 {
		t.Errorf("expected only Missing entries")
	}
}

// TestDiff_AbsoluteRowMatchesTheWalkEntryCarryingItsKey covers the case
// ADR-0030's Consequences names: a files row holding an absolute
// location, against a walk of the library that yields library-relative
// ones.
//
// The two strings are not equal and never will be — ADR-0030 §1 declines
// to migrate those rows — but they are two vocabularies for one object,
// and the walk carries both. Location is what a live write path stores;
// Key is what the backend answers to, which is exactly what an absolute
// location already is on a "/"-rooted local backend.
//
// The row is the shape migrator.seedFilesFromBooks produced: size 0, no
// content hash, so relocate-by-hash can never reach it. Before the key
// lookup it read Missing on every scan while the very bytes it describes
// read New, and the 24h purge sweeper deleted it (#264). Size 0 against
// a real file means it lands in Changed, not Unchanged, which is why the
// worker's Changed arm has to clear the missing flag too.
func TestDiff_AbsoluteRowMatchesTheWalkEntryCarryingItsKey(t *testing.T) {
	const (
		rel = "Kobo Abe/Woman in the Dunes/dunes.epub"
		// The backend reports keys with the leading slash off, even though
		// it also answers to the absolute form (storagetest,
		// KeyShapesNameTheSameObject).
		key = "srv/library/" + rel
		abs = "/" + key
	)
	walked := []scan.WalkEntry{makeKeyedEntry(rel, key, 100, baseTime)}
	dbFiles := []model.File{makeFile("id-seeded", abs, 0, baseTime)}

	cs := scan.Diff(walked, dbFiles)

	if len(cs.New) != 0 {
		t.Errorf("New = %+v, want none: the walked file is the seeded row", cs.New)
	}
	if len(cs.Missing) != 0 {
		t.Errorf("Missing = %+v, want none: the file is right there", cs.Missing)
	}
	if len(cs.Changed) != 1 || cs.Changed[0].DB.ID != "id-seeded" {
		t.Fatalf("Changed = %+v, want the seeded row (size 0 vs 100)", cs.Changed)
	}
}

// The key lookup must not turn every absolute row into a match. A row
// pointing outside the walked library is a genuinely missing file, and
// still has to be flagged and eventually purged.
func TestDiff_AbsoluteRowUnderAnotherRootIsStillMissing(t *testing.T) {
	const rel = "Kobo Abe/Woman in the Dunes/dunes.epub"
	walked := []scan.WalkEntry{
		makeKeyedEntry(rel, "srv/library/"+rel, 100, baseTime),
	}
	dbFiles := []model.File{
		makeFile("id-elsewhere", "/mnt/old-disk/"+rel, 100, baseTime),
	}

	cs := scan.Diff(walked, dbFiles)

	if len(cs.Missing) != 1 || cs.Missing[0].ID != "id-elsewhere" {
		t.Fatalf("Missing = %+v, want the row under the other root", cs.Missing)
	}
	if len(cs.New) != 1 || cs.New[0].Location != rel {
		t.Fatalf("New = %+v, want the walked file %q", cs.New, rel)
	}
}

// Both forms of the same file in one library is the UNIQUE collision
// ADR-0030 anticipates — the hash-relocate having already written the
// relative row next to the absolute one it could not update. The
// relative row is the live one, so it takes the match; the absolute
// duplicate reads Missing, which is the resolution the ADR agreed to.
func TestDiff_RelativeRowWinsOverItsAbsoluteDuplicate(t *testing.T) {
	const (
		rel = "Kobo Abe/Woman in the Dunes/dunes.epub"
		key = "srv/library/" + rel
	)
	walked := []scan.WalkEntry{makeKeyedEntry(rel, key, 100, baseTime)}
	dbFiles := []model.File{
		makeFile("id-absolute", "/"+key, 100, baseTime),
		makeFile("id-relative", rel, 100, baseTime),
	}

	cs := scan.Diff(walked, dbFiles)

	if len(cs.Unchanged) != 1 || cs.Unchanged[0].ID != "id-relative" {
		t.Fatalf("Unchanged = %+v, want the live relative row", cs.Unchanged)
	}
	if len(cs.Missing) != 1 || cs.Missing[0].ID != "id-absolute" {
		t.Fatalf("Missing = %+v, want the absolute duplicate", cs.Missing)
	}
}

func TestDiff_MissingRowWithMissingSinceSet(t *testing.T) {
	// A DB row that already has MissingSince set but reappears in the walk
	// should be classified as Unchanged (if size+mtime match) — the caller
	// is responsible for calling ClearMissing.
	mtime := baseTime
	missingSince := mtime.Add(-time.Hour)
	f := makeFile("id1", "reappeared.epub", 100, mtime)
	f.MissingSince = &missingSince

	walked := []scan.WalkEntry{
		makeEntry("reappeared.epub", 100, mtime),
	}
	dbFiles := []model.File{f}

	cs := scan.Diff(walked, dbFiles)

	if len(cs.Unchanged) != 1 {
		t.Fatalf("Unchanged: want 1 (reappeared file with matching size+mtime), got %d", len(cs.Unchanged))
	}
	if len(cs.Changed) != 0 || len(cs.New) != 0 || len(cs.Missing) != 0 {
		t.Errorf("expected only Unchanged")
	}
}
