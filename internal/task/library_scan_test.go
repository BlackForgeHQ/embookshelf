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
