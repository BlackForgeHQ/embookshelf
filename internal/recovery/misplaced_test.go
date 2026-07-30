// SPDX-License-Identifier: AGPL-3.0-or-later

package recovery_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/recovery"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storageloader"
)

// fixture reproduces the install shape #265 broke: a library that
// predates storage-v2, so the backfill gave it a kind=local backend row
// (wireLibraries) and copied path → root. Everything the recovery reads
// — the backend row, the resolver, the handle's own IsObjectStore — is
// the real thing, because the whole question the tool asks is whether
// that shape is local, and a stub would answer it by assumption.
type fixture struct {
	t     *testing.T
	libs  *repo.LibraryRepo
	books *repo.BookRepo
	files *repo.FileRepo
	deps  recovery.Deps

	// fsRoot stands in for "/" — see recovery.Options.FSRoot.
	fsRoot  string
	libRoot string
	lib     model.Library
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	d := repotest.New(t)

	libs := repo.NewLibraryRepo(d)
	backends := repo.NewStorageBackendRepo(d)

	fsRoot := t.TempDir()
	libRoot := filepath.Join(t.TempDir(), "library")
	if err := os.MkdirAll(libRoot, 0o755); err != nil {
		t.Fatalf("mkdir library root: %v", err)
	}

	backend, err := backends.Create(ctx, "local", map[string]any{"root": libRoot})
	if err != nil {
		t.Fatalf("create backend row: %v", err)
	}
	lib, err := libs.CreateLibrary(ctx, "Main", "main", libRoot, nil)
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	// path and root both set, plus a backend row: exactly what
	// migrator.wireLibraries leaves behind.
	if err := libs.SetBackendID(ctx, lib.ID, backend.ID); err != nil {
		t.Fatalf("wire backend: %v", err)
	}
	lib, err = libs.GetByID(ctx, lib.ID)
	if err != nil {
		t.Fatalf("reload library: %v", err)
	}

	resolver, err := storageloader.LoadStorageBackends(ctx, backends)
	if err != nil {
		t.Fatalf("load backends: %v", err)
	}

	books := repo.NewBookRepo(d)
	files := repo.NewFileRepo(d)
	store := service.NewLibraryStore(service.LibraryStoreDeps{
		Libs:     libs,
		Resolver: resolver,
		Files:    files,
	})

	return &fixture{
		t:     t,
		libs:  libs,
		books: books,
		files: files,
		deps: recovery.Deps{
			Libraries: libs,
			Books:     books,
			Files:     files,
			Store:     store,
		},
		fsRoot:  fsRoot,
		libRoot: libRoot,
		lib:     lib,
	}
}

// misplace writes bytes where the broken placer put them: the
// library-relative key resolved against the filesystem root.
func (f *fixture) misplace(location string, body []byte) string {
	f.t.Helper()
	p := filepath.Join(f.fsRoot, location)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatalf("mkdir suspect dir: %v", err)
	}
	if err := os.WriteFile(p, body, 0o644); err != nil {
		f.t.Fatalf("write suspect: %v", err)
	}
	return p
}

func (f *fixture) writeInLibrary(location string, body []byte) string {
	f.t.Helper()
	p := filepath.Join(f.libRoot, location)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatalf("mkdir library dir: %v", err)
	}
	if err := os.WriteFile(p, body, 0o644); err != nil {
		f.t.Fatalf("write in library: %v", err)
	}
	return p
}

// approvedBook creates the catalog rows an approve leaves behind: a book
// whose path is the library-relative key, and a files row at the same
// location carrying the hash of the bytes.
func (f *fixture) approvedBook(author, title, location string, body []byte) model.Book {
	f.t.Helper()
	ctx := context.Background()
	b, err := f.books.Create(ctx, model.Book{
		LibraryID: f.lib.ID,
		Title:     title,
		Author:    author,
		Format:    "EPUB",
		Path:      location,
	})
	if err != nil {
		f.t.Fatalf("create book: %v", err)
	}
	sum := sha256.Sum256(body)
	if _, err := f.files.Insert(ctx, model.File{
		LibraryID:   f.lib.ID,
		BookID:      b.ID,
		Location:    location,
		Size:        int64(len(body)),
		Mtime:       time.Now().UTC(),
		ContentHash: sum[:],
		Format:      "EPUB",
		LastScanned: time.Now().UTC(),
	}); err != nil {
		f.t.Fatalf("insert files row: %v", err)
	}
	return b
}

func (f *fixture) run(apply bool) recovery.Report {
	f.t.Helper()
	rep, err := recovery.Run(context.Background(), f.deps, recovery.Options{
		FSRoot: f.fsRoot,
		Apply:  apply,
	})
	if err != nil {
		f.t.Fatalf("Run: %v", err)
	}
	return rep
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

const wizardLocation = "Ursula K. Le Guin/A Wizard of Earthsea/wizard.epub"

// The load-bearing case: a local library carrying a backend row, a book
// whose bytes sit at the "/"-rooted key while its correct location is
// empty. The tool moves the bytes and leaves the catalog consistent.
func TestRun_movesMisplacedBytesIntoTheLibrary(t *testing.T) {
	f := newFixture(t)
	body := []byte("a wizard of earthsea, in bytes")
	suspect := f.misplace(wizardLocation, body)
	book := f.approvedBook("Ursula K. Le Guin", "A Wizard of Earthsea", wizardLocation, body)

	rep := f.run(true)

	if len(rep.Findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(rep.Findings), rep.Findings)
	}
	got := rep.Findings[0]
	if got.Kind != recovery.KindRecovered {
		t.Fatalf("kind = %q, want %q (%s)", got.Kind, recovery.KindRecovered, got.Detail)
	}
	if got.BookID != book.ID || got.Location != wizardLocation {
		t.Fatalf("finding names the wrong row: %+v", got)
	}

	correct := filepath.Join(f.libRoot, wizardLocation)
	if !exists(correct) {
		t.Fatalf("bytes not at %s", correct)
	}
	if string(mustRead(t, correct)) != string(body) {
		t.Fatal("bytes at the correct location are not the ones that were moved")
	}
	if exists(suspect) {
		t.Fatalf("source still present at %s", suspect)
	}

	// The catalog already pointed here; it must still, and the row must
	// not be left flagged for the purge sweeper.
	row, err := f.files.GetByLocation(context.Background(), f.lib.ID, wizardLocation)
	if err != nil {
		t.Fatalf("files row after recovery: %v", err)
	}
	if row.BookID != book.ID {
		t.Fatalf("files row book_id = %q, want %q", row.BookID, book.ID)
	}
	if row.MissingSince != nil {
		t.Fatal("recovered file is still flagged missing")
	}
}

// The 24h missing-purge deletes the files row of a book whose bytes it
// cannot find, and a scan never re-adopts them (ADR-0018). books.path
// still holds the key, so the file is still recoverable — but only if the
// tool puts the row back, or the bytes land in a library where nothing
// points at them.
func TestRun_recreatesTheFileRowThePurgeDeleted(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	body := []byte("bytes whose files row the purge already took")
	suspect := f.misplace(wizardLocation, body)

	// A book with no files row at all: exactly what the purge leaves.
	book, err := f.books.Create(ctx, model.Book{
		LibraryID: f.lib.ID,
		Title:     "A Wizard of Earthsea",
		Author:    "Ursula K. Le Guin",
		Format:    "EPUB",
		Path:      wizardLocation,
	})
	if err != nil {
		t.Fatalf("create book: %v", err)
	}

	rep := f.run(true)

	if len(rep.Findings) != 1 || rep.Findings[0].Kind != recovery.KindRecovered {
		t.Fatalf("findings = %+v, want one recovered", rep.Findings)
	}
	if !rep.Findings[0].FileRowRecreated {
		t.Fatal("finding does not record that the files row was recreated")
	}
	correct := filepath.Join(f.libRoot, wizardLocation)
	if !exists(correct) || exists(suspect) {
		t.Fatalf("bytes not moved: correct=%v suspect=%v", exists(correct), exists(suspect))
	}

	row, err := f.files.GetByLocation(ctx, f.lib.ID, wizardLocation)
	if err != nil {
		t.Fatalf("files row was not recreated: %v", err)
	}
	if row.BookID != book.ID {
		t.Fatalf("recreated row book_id = %q, want %q", row.BookID, book.ID)
	}
	if row.Format != "EPUB" {
		t.Fatalf("recreated row format = %q, want EPUB", row.Format)
	}
	if row.Size != int64(len(body)) {
		t.Fatalf("recreated row size = %d, want %d", row.Size, len(body))
	}
	want := sha256.Sum256(body)
	if !bytes.Equal(row.ContentHash, want[:]) {
		t.Fatalf("recreated row hash = %x, want %x", row.ContentHash, want[:])
	}
	if row.Mtime.IsZero() {
		t.Fatal("recreated row has no mtime")
	}
}

// A hash that disagrees means something else is at that path. It is not
// this book's file and must not be moved into this book's place.
func TestRun_refusesToMoveWhenTheContentHashDisagrees(t *testing.T) {
	f := newFixture(t)
	suspect := f.misplace(wizardLocation, []byte("some entirely different file"))
	f.approvedBook("Ursula K. Le Guin", "A Wizard of Earthsea", wizardLocation,
		[]byte("the bytes the catalog actually recorded"))

	rep := f.run(true)

	if len(rep.Findings) != 1 {
		t.Fatalf("findings = %+v, want 1", rep.Findings)
	}
	if rep.Findings[0].Kind != recovery.KindMismatch {
		t.Fatalf("kind = %q, want %q", rep.Findings[0].Kind, recovery.KindMismatch)
	}
	if !exists(suspect) {
		t.Fatal("the tool moved a file whose hash did not match")
	}
	if exists(filepath.Join(f.libRoot, wizardLocation)) {
		t.Fatal("mismatched bytes were written into the library")
	}
}

// An occupied destination means the operator already re-imported the
// book. The copy at the root is a duplicate: name it, touch nothing.
func TestRun_reportsAStrayWhenTheDestinationIsOccupied(t *testing.T) {
	f := newFixture(t)
	body := []byte("the misplaced copy")
	suspect := f.misplace(wizardLocation, body)
	reimported := f.writeInLibrary(wizardLocation, []byte("the re-imported copy"))
	f.approvedBook("Ursula K. Le Guin", "A Wizard of Earthsea", wizardLocation, body)

	rep := f.run(true)

	if len(rep.Findings) != 1 {
		t.Fatalf("findings = %+v, want 1", rep.Findings)
	}
	got := rep.Findings[0]
	if got.Kind != recovery.KindOccupied {
		t.Fatalf("kind = %q, want %q", got.Kind, recovery.KindOccupied)
	}
	if got.Suspect != suspect {
		t.Fatalf("stray path = %q, want %q", got.Suspect, suspect)
	}
	if !exists(suspect) {
		t.Fatal("the stray under the filesystem root was deleted")
	}
	if string(mustRead(t, reimported)) != "the re-imported copy" {
		t.Fatal("the re-imported copy was overwritten")
	}
}

// Two shapes of install that were never affected: one whose bytes are
// where they belong, and one created after the storage-v2 migration —
// null backend column, so its placer was always right.
func TestRun_unaffectedInstallReportsNothing(t *testing.T) {
	t.Run("bytes already in the library", func(t *testing.T) {
		f := newFixture(t)
		body := []byte("correctly placed from the start")
		f.writeInLibrary(wizardLocation, body)
		f.approvedBook("Ursula K. Le Guin", "A Wizard of Earthsea", wizardLocation, body)

		rep := f.run(true)
		if len(rep.Findings) != 0 {
			t.Fatalf("findings = %+v, want none", rep.Findings)
		}
		if rep.LibrariesInspected != 1 || rep.BooksInspected != 1 {
			t.Fatalf("inspected %d libraries / %d books, want 1/1",
				rep.LibrariesInspected, rep.BooksInspected)
		}
	})

	t.Run("library created after the migration", func(t *testing.T) {
		f := newFixture(t)
		body := []byte("a file at the root that is not ours")
		suspect := f.misplace(wizardLocation, body)
		f.approvedBook("Ursula K. Le Guin", "A Wizard of Earthsea", wizardLocation, body)
		if err := f.libs.SetBackendID(context.Background(), f.lib.ID, ""); err != nil {
			t.Fatalf("clear backend id: %v", err)
		}

		rep := f.run(true)
		if len(rep.Findings) != 0 {
			t.Fatalf("findings = %+v, want none", rep.Findings)
		}
		if rep.LibrariesInspected != 0 || rep.LibrariesSkipped != 1 {
			t.Fatalf("inspected %d / skipped %d, want 0/1",
				rep.LibrariesInspected, rep.LibrariesSkipped)
		}
		if !exists(suspect) {
			t.Fatal("a library that was never affected had its bytes moved")
		}
	})
}

// The default. A dry run must report exactly what --apply would act on
// and leave both the filesystem and the catalog alone.
func TestRun_dryRunChangesNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	body := []byte("still at the root after a dry run")
	suspect := f.misplace(wizardLocation, body)
	f.approvedBook("Ursula K. Le Guin", "A Wizard of Earthsea", wizardLocation, body)

	rep := f.run(false)

	if rep.Applied {
		t.Fatal("report claims it applied")
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Kind != recovery.KindRecovered {
		t.Fatalf("findings = %+v, want one recovered", rep.Findings)
	}
	if rep.Findings[0].FileRowRecreated {
		t.Fatal("dry run claims it recreated a files row")
	}
	if !exists(suspect) {
		t.Fatal("dry run moved the file")
	}
	if exists(filepath.Join(f.libRoot, wizardLocation)) {
		t.Fatal("dry run wrote into the library")
	}

	// And the run it predicted does exactly that one thing.
	applied := f.run(true)
	if len(applied.Findings) != 1 || applied.Findings[0].Kind != recovery.KindRecovered {
		t.Fatalf("apply findings = %+v, want the same one", applied.Findings)
	}
	if _, err := f.files.GetByLocation(ctx, f.lib.ID, wizardLocation); err != nil {
		t.Fatalf("files row after apply: %v", err)
	}
}

// Re-running after a repair finds nothing: there is no longer a file at
// the root for any row to claim.
func TestRun_isIdempotentAfterApply(t *testing.T) {
	f := newFixture(t)
	body := []byte("moved once")
	f.misplace(wizardLocation, body)
	f.approvedBook("Ursula K. Le Guin", "A Wizard of Earthsea", wizardLocation, body)

	if n := len(f.run(true).Findings); n != 1 {
		t.Fatalf("first run findings = %d, want 1", n)
	}
	second := f.run(true)
	if len(second.Findings) != 0 {
		t.Fatalf("second run findings = %+v, want none", second.Findings)
	}
	if third := f.run(false); len(third.Findings) != 0 {
		t.Fatalf("dry run after repair findings = %+v, want none", third.Findings)
	}
}
