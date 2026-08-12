// SPDX-License-Identifier: AGPL-3.0-or-later

package scan_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/scan"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
)

// fakeFileState is the whole files table the reconcile is allowed to
// see, in memory. It exists so the drift rules — which are policy, not
// SQL — can be asserted without a database, and so ADR-0018 becomes
// checkable: FileState has no create method, so a scan that tried to
// materialise a row could not compile, and the row count below pins
// that the reconcile does not try.
type fakeFileState struct {
	rows []model.File
	// marked and cleared record the flag writes in order, so a rule
	// phrased as "does not touch the row" is asserted as a missing call
	// rather than as a value that happened to stay put.
	marked  []string
	cleared []string
	moved   map[string]string
}

func newFakeFileState(rows ...model.File) *fakeFileState {
	return &fakeFileState{rows: rows, moved: map[string]string{}}
}

func (f *fakeFileState) ListByLibrary(_ context.Context, libraryID string) ([]model.File, error) {
	var out []model.File
	for _, r := range f.rows {
		if r.LibraryID == libraryID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeFileState) GetByContentHash(_ context.Context, hash []byte) ([]model.File, error) {
	var out []model.File
	for _, r := range f.rows {
		if len(r.ContentHash) > 0 && string(r.ContentHash) == string(hash) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeFileState) UpdateLocation(_ context.Context, fileID, newLocation string) error {
	f.moved[fileID] = newLocation
	for i := range f.rows {
		if f.rows[i].ID == fileID {
			f.rows[i].Location = newLocation
			return nil
		}
	}
	return nil
}

func (f *fakeFileState) MarkMissing(_ context.Context, fileID string, when time.Time) error {
	f.marked = append(f.marked, fileID)
	for i := range f.rows {
		if f.rows[i].ID == fileID {
			t := when
			f.rows[i].MissingSince = &t
			return nil
		}
	}
	return nil
}

func (f *fakeFileState) ClearMissing(_ context.Context, fileID string) error {
	f.cleared = append(f.cleared, fileID)
	for i := range f.rows {
		if f.rows[i].ID == fileID {
			f.rows[i].MissingSince = nil
			return nil
		}
	}
	return nil
}

func (f *fakeFileState) row(t *testing.T, id string) model.File {
	t.Helper()
	for _, r := range f.rows {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no row %q in the fake files table", id)
	return model.File{}
}

func has(ids []string, id string) bool {
	for _, got := range ids {
		if got == id {
			return true
		}
	}
	return false
}

const testLibrary = "lib-1"

var baseMtime = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func flaggedAt(t time.Time) *time.Time { return &t }

func sum(content string) []byte {
	s := sha256.Sum256([]byte(content))
	return s[:]
}

// backingStore writes content into a temp dir and returns a Storage
// rooted at it, so the relocate arm can hash real bytes. No database is
// involved either way — the drift rules are the subject, and the only
// thing storage contributes is a sha256.
func backingStore(t *testing.T, files map[string]string) storage.Storage {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write %q: %v", rel, err)
		}
	}
	store, err := local.New(root)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	return store
}

func entry(loc string, size int64, mtime time.Time) scan.WalkEntry {
	return scan.WalkEntry{Location: loc, Key: loc, Size: size, Mtime: mtime}
}

// walked is a walk that just answers with a listing.
func walked(entries ...scan.WalkEntry) func(context.Context) ([]scan.WalkEntry, error) {
	return func(context.Context) ([]scan.WalkEntry, error) { return entries, nil }
}

// The four drift rules ADR-0018 leaves the scan: what a walk that found
// a row's file does to that row's missing flag, and what the missing
// pass is allowed to flag.
func TestReconcileDriftRules(t *testing.T) {
	const (
		present = "Kobo Abe/Woman in the Dunes/dunes.epub"
		renamed = "Kobo Abe/Woman in the Dunes/renamed.epub"
		gone    = "Gone/Away/gone.epub"
	)
	stale := baseMtime.Add(-48 * time.Hour)

	tests := []struct {
		name   string
		files  map[string]string
		rows   []model.File
		walked []scan.WalkEntry
		check  func(t *testing.T, f *fakeFileState, rep scan.ReconcileReport)
	}{
		{
			// Rule 1. A row the walk saw unchanged is a present file, and
			// a present file must not be left holding a flag the purge
			// sweeper acts on.
			name: "unchanged clears the missing flag",
			rows: []model.File{{
				ID: "f1", LibraryID: testLibrary, Location: present,
				Size: 5, Mtime: baseMtime, MissingSince: flaggedAt(stale),
			}},
			walked: []scan.WalkEntry{entry(present, 5, baseMtime)},
			check: func(t *testing.T, f *fakeFileState, rep scan.ReconcileReport) {
				if got := f.row(t, "f1").MissingSince; got != nil {
					t.Errorf("MissingSince = %v, want nil", got)
				}
				if !has(f.cleared, "f1") {
					t.Error("ClearMissing was never called for the unchanged row")
				}
				if rep.Missing != 0 {
					t.Errorf("Missing = %d, want 0", rep.Missing)
				}
			},
		},
		{
			// Rule 2. Changed is a no-op on metadata, but the flag is not
			// metadata: the walk just saw the file. This is the arm the
			// storage-v2 seeded rows land in — size 0 never matches, so
			// they never reach Unchanged (#264).
			name: "changed clears the missing flag too",
			rows: []model.File{{
				ID: "f1", LibraryID: testLibrary, Location: present,
				Size: 0, Mtime: baseMtime, MissingSince: flaggedAt(stale),
			}},
			walked: []scan.WalkEntry{entry(present, 5, baseMtime)},
			check: func(t *testing.T, f *fakeFileState, rep scan.ReconcileReport) {
				if got := f.row(t, "f1").MissingSince; got != nil {
					t.Errorf("MissingSince = %v, want nil", got)
				}
				if !has(f.cleared, "f1") {
					t.Error("ClearMissing was never called for the changed row")
				}
			},
		},
		{
			// Rule 3. The location a relocated row used to live at comes
			// back from this same scan as Missing. Flagging it would undo
			// the relocate one line later, which is why the missing pass
			// has to read what the relocate pass moved.
			name:  "missing pass skips a row relocated in the same scan",
			files: map[string]string{renamed: "the same bytes"},
			rows: []model.File{{
				ID: "f1", LibraryID: testLibrary, Location: present,
				Size: 14, Mtime: baseMtime, ContentHash: sum("the same bytes"),
			}},
			walked: []scan.WalkEntry{entry(renamed, 14, baseMtime)},
			check: func(t *testing.T, f *fakeFileState, rep scan.ReconcileReport) {
				if got := f.moved["f1"]; got != renamed {
					t.Fatalf("row location = %q, want %q", got, renamed)
				}
				if has(f.marked, "f1") {
					t.Error("a row this scan relocated was also flagged missing")
				}
				if got := f.row(t, "f1").MissingSince; got != nil {
					t.Errorf("MissingSince = %v, want nil", got)
				}
				if rep.Relocated != 1 {
					t.Errorf("Relocated = %d, want 1", rep.Relocated)
				}
			},
		},
		{
			// Rule 4. An already-flagged row keeps the timestamp it was
			// first flagged with: re-stamping it would push the 24h purge
			// deadline out on every scan, so a file that really is gone
			// would never age out.
			name: "missing pass skips an already-flagged row",
			rows: []model.File{{
				ID: "f1", LibraryID: testLibrary, Location: gone,
				MissingSince: flaggedAt(stale),
			}},
			walked: nil,
			check: func(t *testing.T, f *fakeFileState, rep scan.ReconcileReport) {
				if has(f.marked, "f1") {
					t.Error("MarkMissing was called on a row that was already flagged")
				}
				got := f.row(t, "f1").MissingSince
				if got == nil || !got.Equal(stale) {
					t.Errorf("MissingSince = %v, want the original %v", got, stale)
				}
				if rep.Missing != 1 {
					t.Errorf("Missing = %d, want 1 — the row is still missing", rep.Missing)
				}
			},
		},
		{
			// The control the three rules above must not cost: a row with
			// no file behind it is still flagged, once.
			name: "an unflagged row the walk did not see is flagged",
			rows: []model.File{{
				ID: "f1", LibraryID: testLibrary, Location: gone,
			}},
			walked: nil,
			check: func(t *testing.T, f *fakeFileState, rep scan.ReconcileReport) {
				if !has(f.marked, "f1") {
					t.Fatal("a row whose file no longer walks was not flagged missing")
				}
				if got := f.row(t, "f1").MissingSince; got == nil {
					t.Error("MissingSince is nil after MarkMissing")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := newFakeFileState(tc.rows...)
			rep, err := scan.Reconcile(context.Background(), scan.ReconcileInput{
				LibraryID: testLibrary,
				Walk:      walked(tc.walked...),
				Store:     backingStore(t, tc.files),
				Files:     files,
			})
			if err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if rep.Walked != len(tc.walked) {
				t.Errorf("Walked = %d, want %d", rep.Walked, len(tc.walked))
			}
			tc.check(t, files, rep)
		})
	}
}

// ADR-0018: scan is drift detection, never an ingest path. A walked file
// no row knows about, and whose bytes match no row's hash, is ignored —
// no books row, no files row, no bookdrop item. FileState carries no
// create method at all, so the compiler is the first half of this; the
// row count is the second, and pins that the relocate arm does not
// quietly grow one.
func TestReconcileCreatesNothing(t *testing.T) {
	const stranger = "Someone Else/A Book/stranger.epub"
	files := newFakeFileState(model.File{
		ID: "f1", LibraryID: testLibrary, Location: "Kept/Book/kept.epub",
		Size: 4, Mtime: baseMtime,
	})

	rep, err := scan.Reconcile(context.Background(), scan.ReconcileInput{
		LibraryID: testLibrary,
		Walk: walked(
			entry("Kept/Book/kept.epub", 4, baseMtime),
			entry(stranger, 9, baseMtime),
		),
		Store: backingStore(t, map[string]string{stranger: "strangers"}),
		Files: files,
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(files.rows) != 1 {
		t.Fatalf("the files table holds %d rows, want 1 — the scan ingested", len(files.rows))
	}
	if files.rows[0].Location != "Kept/Book/kept.epub" {
		t.Errorf("the surviving row moved to %q", files.rows[0].Location)
	}
	if rep.Relocated != 0 {
		t.Errorf("Relocated = %d, want 0 — nothing matched by content", rep.Relocated)
	}
}

// A same-content hit in another library is another library's book, not
// this one moving. Pinned here because the reconcile is the thing that
// now owns the library scoping the worker used to pass in.
func TestReconcileDoesNotRelocateAcrossLibraries(t *testing.T) {
	const renamed = "Kobo Abe/Woman in the Dunes/renamed.epub"
	files := newFakeFileState(model.File{
		ID: "f1", LibraryID: "other-lib", Location: "elsewhere/original.epub",
		ContentHash: sum("the same bytes"),
	})

	rep, err := scan.Reconcile(context.Background(), scan.ReconcileInput{
		LibraryID: testLibrary,
		Walk:      walked(entry(renamed, 14, baseMtime)),
		Store:     backingStore(t, map[string]string{renamed: "the same bytes"}),
		Files:     files,
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rep.Relocated != 0 {
		t.Errorf("Relocated = %d, want 0", rep.Relocated)
	}
	if got := files.row(t, "f1").Location; got != "elsewhere/original.epub" {
		t.Errorf("another library's row moved to %q", got)
	}
}

// A book approved while the walk is in flight must not be flagged
// missing by that same walk.
//
// Bookdrop places the bytes and only then inserts the files row
// (service.BookDropService.Approve), so the two land on opposite sides of
// a walk that has already passed the destination folder: the walk cannot
// have seen the file, and a files snapshot taken afterwards does contain
// the row. Classified against each other, that row reads Missing, gets
// flagged, and RunMissingPurge deletes it once MissingTTL has passed —
// a book with no files row pointing at bytes that are right there, the
// #264 shape. Scans are admin-triggered with no periodic timer, so
// nothing is guaranteed to rescan and clear the flag inside 24h.
//
// Reconcile owns the ordering that closes this — snapshot, then walk —
// which is why the walk is a function it calls rather than a listing it
// is handed. The insert here happens during the walk, exactly where
// Approve's does.
func TestReconcileIgnoresARowCreatedDuringTheWalk(t *testing.T) {
	const (
		existing = "Kobo Abe/Woman in the Dunes/dunes.epub"
		approved = "Ursula Le Guin/The Dispossessed/dispossessed.epub"
	)
	files := newFakeFileState(model.File{
		ID: "f1", LibraryID: testLibrary, Location: existing,
		Size: 5, Mtime: baseMtime,
	})

	rep, err := scan.Reconcile(context.Background(), scan.ReconcileInput{
		LibraryID: testLibrary,
		Store:     backingStore(t, nil),
		Files:     files,
		Walk: func(context.Context) ([]scan.WalkEntry, error) {
			// Approve lands mid-walk: bytes into a folder this walk has
			// already been past, then the row.
			files.rows = append(files.rows, model.File{
				ID: "f2", LibraryID: testLibrary, Location: approved,
				Size: 18, Mtime: baseMtime,
			})
			return []scan.WalkEntry{entry(existing, 5, baseMtime)}, nil
		},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if has(files.marked, "f2") {
		t.Error("a row inserted while the walk was running was flagged missing — " +
			"the files snapshot was taken after the walk, so bookdrop's " +
			"place-then-insert lands on both sides of it")
	}
	if got := files.row(t, "f2").MissingSince; got != nil {
		t.Errorf("the freshly approved row carries MissingSince = %v", got)
	}
	if rep.Missing != 0 {
		t.Errorf("Missing = %d, want 0 — the new row is not in the snapshot at all", rep.Missing)
	}
}

// A walk that could not run must leave the library exactly as it found
// it. The caller aborts by returning an error — an unconfigured library
// is the live case — and the one thing that must not happen is for the
// empty listing to be reconciled, because every row in the library then
// reads Missing and the sweeper takes the lot.
func TestReconcileWritesNothingWhenTheWalkFails(t *testing.T) {
	files := newFakeFileState(model.File{
		ID: "f1", LibraryID: testLibrary, Location: "Kept/Book/kept.epub",
	})
	sentinel := errors.New("library has no local root to walk")

	_, err := scan.Reconcile(context.Background(), scan.ReconcileInput{
		LibraryID: testLibrary,
		Store:     backingStore(t, nil),
		Files:     files,
		Walk: func(context.Context) ([]scan.WalkEntry, error) {
			return nil, sentinel
		},
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Reconcile = %v, want the walk's own error back for the caller to classify", err)
	}
	if len(files.marked) != 0 || len(files.cleared) != 0 {
		t.Errorf("a failed walk still wrote: marked=%v cleared=%v", files.marked, files.cleared)
	}
}
