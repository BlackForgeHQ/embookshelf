// SPDX-License-Identifier: AGPL-3.0-or-later

package task_test

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
	"github.com/blackforge/embookshelf/internal/task"
)

// objectStoreFS is a filesystem pretending to be an object store: it
// advertises the capability the write and scan paths branch on, and —
// like a real backend — it is rooted at its own prefix, so the keys it
// lists are already library-relative.
type objectStoreFS struct{ storage.Storage }

func (objectStoreFS) Capabilities() storage.Capability { return storage.CapObjectStore }

// scanFixture wires the scan worker against a real database and a
// Storage of the caller's choosing.
func scanFixture(t *testing.T, objectStore bool) (task.LibraryScanDeps, model.Library, string, *repo.LibraryRepo) {
	t.Helper()
	return scanFixtureRooted(t, objectStore, nil)
}

// scanFixtureRooted is scanFixture with control over how the Library's
// root is spelled in the row, which is not always how the filesystem
// spells it back.
func scanFixtureRooted(t *testing.T, objectStore bool, spell func(root string) string) (task.LibraryScanDeps, model.Library, string, *repo.LibraryRepo) {
	t.Helper()
	d := repotest.New(t)
	root := t.TempDir()

	// A backend-rooted store lists keys relative to itself; a local one
	// is rooted at "/" for the whole instance and lists absolute paths.
	var store storage.Storage
	var err error
	if objectStore {
		var fs *local.LocalFS
		fs, err = local.New(root)
		store = objectStoreFS{fs}
	} else {
		store, err = local.New("/")
	}
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}

	lr := repo.NewLibraryRepo(d)
	// An S3 library carries no root: the backend encodes its own
	// libraries/{slug}/ prefix. A local one carries the path it walks.
	libPath := root
	if objectStore {
		libPath = ""
	} else if spell != nil {
		libPath = spell(root)
	}
	lib, err := lr.CreateLibrary(context.Background(), "Scanned", "scanned", libPath, nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	files := repo.NewFileRepo(d)
	deps := task.LibraryScanDeps{
		Lib: service.NewLibraryService(lr, repo.NewBookRepo(d), service.LibraryServiceDeps{}, nil),
		LibStore: service.NewLibraryStore(service.LibraryStoreDeps{
			Libs:     lr,
			Resolver: storage.ConstantResolver{S: store},
			Files:    files,
		}),
		Files: files,
	}
	return deps, lib, root, lr
}

func sha256Sum(content string) []byte {
	sum := sha256.Sum256([]byte(content))
	return sum[:]
}

func writeBook(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// An S3-backed Library has no root by design — the Backend already
// encodes its own prefix — and the worker read that emptiness as "not
// configured", warned, and returned nil. So Library scan has never run
// on an S3 Library, and reported success while skipping it: a book added
// outside the app was never picked up, and a file removed was never
// marked missing (#203).
func TestLibraryScanWalksAnObjectStoreLibrary(t *testing.T) {
	deps, lib, root, _ := scanFixture(t, true)
	ctx := context.Background()

	writeBook(t, root, "Kobo Abe/Woman in the Dunes/dunes.epub", "dunes")
	// A row that no longer walks, so the missing pass has something to do.
	if _, err := deps.Files.Insert(ctx, model.File{
		LibraryID: lib.ID, Location: "Gone/Away/gone.epub", Format: "EPUB",
	}); err != nil {
		t.Fatalf("seed missing row: %v", err)
	}

	if err := task.LibraryScan(ctx, jobs.LibraryScanArgs{LibraryID: lib.ID}, deps); err != nil {
		t.Fatalf("LibraryScan: %v", err)
	}

	gone, err := deps.Files.GetByLocation(ctx, lib.ID, "Gone/Away/gone.epub")
	if err != nil {
		t.Fatalf("GetByLocation: %v", err)
	}
	if gone.MissingSince == nil {
		t.Error("a row whose file no longer walks was not flagged missing — the scan skipped the library")
	}
}

// The relocate-by-hash path is what turns an externally renamed file
// into a moved row rather than a missing one plus a new one. It has to
// work on both Backend kinds; only the local one was reachable before.
func TestLibraryScanRelocatesByHashOnAnObjectStoreLibrary(t *testing.T) {
	deps, lib, root, _ := scanFixture(t, true)
	ctx := context.Background()

	const content = "the same bytes"
	writeBook(t, root, "Kobo Abe/Woman in the Dunes/renamed.epub", content)

	sum := sha256Sum(content)
	if _, err := deps.Files.Insert(ctx, model.File{
		LibraryID:   lib.ID,
		Location:    "Kobo Abe/Woman in the Dunes/original.epub",
		Format:      "EPUB",
		ContentHash: sum,
	}); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	if err := task.LibraryScan(ctx, jobs.LibraryScanArgs{LibraryID: lib.ID}, deps); err != nil {
		t.Fatalf("LibraryScan: %v", err)
	}

	moved, err := deps.Files.GetByLocation(ctx, lib.ID, "Kobo Abe/Woman in the Dunes/renamed.epub")
	if err != nil {
		t.Fatalf("the row was not relocated to the new location: %v", err)
	}
	if moved.MissingSince != nil {
		t.Error("a relocated row was also flagged missing")
	}
}

// The local path keeps working: it walks from the Library root and
// relativizes what it finds, which is the asymmetry ADR-0030 §1
// preserves.
func TestLibraryScanWalksALocalLibrary(t *testing.T) {
	deps, lib, root, _ := scanFixture(t, false)
	ctx := context.Background()

	writeBook(t, root, "Kobo Abe/Woman in the Dunes/dunes.epub", "dunes")
	if _, err := deps.Files.Insert(ctx, model.File{
		LibraryID: lib.ID, Location: "Gone/Away/gone.epub", Format: "EPUB",
	}); err != nil {
		t.Fatalf("seed missing row: %v", err)
	}

	if err := task.LibraryScan(ctx, jobs.LibraryScanArgs{LibraryID: lib.ID}, deps); err != nil {
		t.Fatalf("LibraryScan: %v", err)
	}

	gone, err := deps.Files.GetByLocation(ctx, lib.ID, "Gone/Away/gone.epub")
	if err != nil {
		t.Fatalf("GetByLocation: %v", err)
	}
	if gone.MissingSince == nil {
		t.Error("a row whose file no longer walks was not flagged missing")
	}
}

// The root as an admin spelled it is not the root the filesystem spells
// back. The Backend cleans the prefix before listing, so a redundant
// separator in the row is enough for a string-prefix strip to match
// nothing — and the old one fell through to emitting the absolute path.
// Every walked entry then read New and every row read Missing, so a
// scan of a perfectly healthy Library soft-flagged the whole of it for
// the 24h purge sweeper.
//
// The walk now promises library-relative locations for every spelling of
// the root, which is the only reason the differ can be trusted to
// compare like with like.
func TestLibraryScanWalksALocalLibraryWhoseRootIsSpelledNonCanonically(t *testing.T) {
	deps, lib, root, _ := scanFixtureRooted(t, false,
		func(r string) string { return r + "//" })
	ctx := context.Background()

	const loc = "Kobo Abe/Woman in the Dunes/dunes.epub"
	writeBook(t, root, loc, "dunes")
	if _, err := deps.Files.Insert(ctx, model.File{
		LibraryID: lib.ID, Location: loc, Format: "EPUB",
	}); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	if err := task.LibraryScan(ctx, jobs.LibraryScanArgs{LibraryID: lib.ID}, deps); err != nil {
		t.Fatalf("LibraryScan: %v", err)
	}

	present, err := deps.Files.GetByLocation(ctx, lib.ID, loc)
	if err != nil {
		t.Fatalf("GetByLocation: %v", err)
	}
	if present.MissingSince != nil {
		t.Error("a file that is right there was flagged missing — the walk " +
			"emitted absolute locations, so nothing matched the rows")
	}
}

// Where the rescue for an absolute-location row ends.
//
// Rows holding an absolute location exist, from one producer:
// migrator.seedFilesFromBooks writes books.path verbatim when the
// library root was unknown at seed time. The walk yields
// library-relative locations, the differ compares by exact string, so
// such a row reads Missing while the very bytes it describes read New —
// the consequence ADR-0030 names and declines to migrate.
//
// Relocate-by-hash is the only thing standing between that row and the
// purge sweeper, and it can only reach a row that carries a content
// hash. The seeded rows carry none (seedFilesFromBooks inserts location,
// size 0 and mtime, and no hash), so for exactly the rows this shape
// comes from, Missing is the final answer. Pinned in one test because
// the difference between the two halves is invisible in the code.
func TestLibraryScanRescuesAnAbsoluteRowOnlyWhenItCarriesAHash(t *testing.T) {
	deps, lib, root, _ := scanFixture(t, false)
	ctx := context.Background()

	const (
		hashed   = "Kobo Abe/Woman in the Dunes/dunes.epub"
		hashless = "Ursula Le Guin/The Dispossessed/dispossessed.epub"
	)
	writeBook(t, root, hashed, "dunes bytes")
	writeBook(t, root, hashless, "dispossessed bytes")

	// Both rows hold the absolute path the backfill would have written.
	absHashed := filepath.Join(root, filepath.FromSlash(hashed))
	absHashless := filepath.Join(root, filepath.FromSlash(hashless))
	if _, err := deps.Files.Insert(ctx, model.File{
		LibraryID: lib.ID, Location: absHashed, Format: "EPUB",
		ContentHash: sha256Sum("dunes bytes"),
	}); err != nil {
		t.Fatalf("seed hashed row: %v", err)
	}
	if _, err := deps.Files.Insert(ctx, model.File{
		LibraryID: lib.ID, Location: absHashless, Format: "EPUB",
	}); err != nil {
		t.Fatalf("seed hashless row: %v", err)
	}

	if err := task.LibraryScan(ctx, jobs.LibraryScanArgs{LibraryID: lib.ID}, deps); err != nil {
		t.Fatalf("LibraryScan: %v", err)
	}

	moved, err := deps.Files.GetByLocation(ctx, lib.ID, hashed)
	if err != nil {
		t.Fatalf("the hashed absolute row was not relocated to its relative location: %v", err)
	}
	if moved.MissingSince != nil {
		t.Error("a relocated row was also flagged missing")
	}

	stranded, err := deps.Files.GetByLocation(ctx, lib.ID, absHashless)
	if err != nil {
		t.Fatalf("GetByLocation: %v", err)
	}
	if stranded.MissingSince == nil {
		t.Error("a hashless absolute row survived the scan unflagged — if that " +
			"is now true, the absolute-location consequence in ADR-0030 has " +
			"been addressed somewhere and this test should say where")
	}
}

// A local Library with no root really is unconfigured, and declining is
// right — but it has to say which case it hit, because the other case
// used to land here silently.
func TestLibraryScanDeclinesALocalLibraryWithNoRoot(t *testing.T) {
	deps, _, _, lr := scanFixture(t, false)
	ctx := context.Background()

	// Same database the deps read from — a library created elsewhere is
	// a library the worker cannot find.
	rootless, err := lr.CreateLibrary(ctx, "Rootless", "rootless", "", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	// Not an error: an unconfigured library is a state to report, not a
	// job to retry twenty-five times.
	if err := task.LibraryScan(ctx, jobs.LibraryScanArgs{LibraryID: rootless.ID}, deps); err != nil {
		t.Fatalf("LibraryScan on a rootless library returned %v, want nil", err)
	}
}
