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

// TestDiff_AbsoluteRowAgainstRelativeWalk pins the case ADR-0030's
// Consequences names and nothing tested: a files row holding an
// absolute location, against a walk of the library that yields
// library-relative ones.
//
// The differ compares locations by exact string, so the two are not the
// same file to it — the row reads Missing while the very bytes it
// describes read New. It is only representable now that the walk
// guarantees one shape on its side; before, a walked location could come
// back absolute too (the relativize fallback), so a test like this could
// not say which side of the mismatch it was looking at.
//
// This is deliberately a characterisation, not a wish. ADR-0030 §1
// declines to migrate those rows, and the rescue lives one layer up in
// RelocateByHash — which only fires for rows that carry a content hash.
// The rows this shape actually comes from (migrator.seedFilesFromBooks)
// carry none, so for them the Missing classification below is the final
// answer and the purge sweeper acts on it.
func TestDiff_AbsoluteRowAgainstRelativeWalk(t *testing.T) {
	const (
		rel = "Kobo Abe/Woman in the Dunes/dunes.epub"
		abs = "/srv/library/" + rel
	)
	walked := []scan.WalkEntry{makeEntry(rel, 100, baseTime)}
	dbFiles := []model.File{makeFile("id-legacy", abs, 100, baseTime)}

	cs := scan.Diff(walked, dbFiles)

	if len(cs.New) != 1 || cs.New[0].Location != rel {
		t.Fatalf("New = %+v, want the walked relative location %q", cs.New, rel)
	}
	if len(cs.Missing) != 1 || cs.Missing[0].ID != "id-legacy" {
		t.Fatalf("Missing = %+v, want the absolute row id-legacy", cs.Missing)
	}
	if len(cs.Unchanged) != 0 || len(cs.Changed) != 0 {
		t.Errorf("an absolute row and its relative walk entry are not the same "+
			"file to the differ; got Unchanged=%d Changed=%d",
			len(cs.Unchanged), len(cs.Changed))
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
