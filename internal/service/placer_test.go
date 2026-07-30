// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"io"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
)

func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestLocalPlacer_FolderLayout(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	src := writeTempFile(t, staging, "hobbit.epub", "epub bytes")

	p := LocalPlacer{Root: root}
	res, err := p.Place(context.Background(), PlaceSource{
		Path:   src,
		Format: "EPUB",
		Author: "J.R.R. Tolkien",
		Title:  "The Hobbit",
	})
	if err != nil {
		t.Fatalf("Place: %v", err)
	}

	wantLocation := filepath.Join("J.R.R. Tolkien", "The Hobbit", "hobbit.epub")
	if res.Location != wantLocation {
		t.Errorf("Location=%q want %q", res.Location, wantLocation)
	}
	wantFolder := filepath.Join("J.R.R. Tolkien", "The Hobbit")
	if res.FolderPath != wantFolder {
		t.Errorf("FolderPath=%q want %q", res.FolderPath, wantFolder)
	}

	on := filepath.Join(root, wantLocation)
	if _, err := os.Stat(on); err != nil {
		t.Fatalf("stat moved file: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source still exists after place: err=%v", err)
	}
}

func TestLocalPlacer_FallbackSentinels(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	src := writeTempFile(t, staging, "mystery.epub", "x")

	p := LocalPlacer{Root: root}
	res, err := p.Place(context.Background(), PlaceSource{
		Path:   src,
		Format: "EPUB",
		Author: "",
		Title:  "",
	})
	if err != nil {
		t.Fatalf("Place: %v", err)
	}

	wantFolder := filepath.Join("Unknown Author", "Untitled")
	if res.FolderPath != wantFolder {
		t.Errorf("FolderPath=%q want %q", res.FolderPath, wantFolder)
	}
}

func TestLocalPlacer_Sanitization(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	src := writeTempFile(t, staging, "weird.epub", "x")

	p := LocalPlacer{Root: root}
	res, err := p.Place(context.Background(), PlaceSource{
		Path:   src,
		Format: "EPUB",
		Author: "Author/With/Slashes",
		Title:  "Title:With*Stars",
	})
	if err != nil {
		t.Fatalf("Place: %v", err)
	}

	wantFolder := filepath.Join("Author_With_Slashes", "Title_With_Stars")
	if res.FolderPath != wantFolder {
		t.Errorf("FolderPath=%q want %q", res.FolderPath, wantFolder)
	}
}

func TestLocalPlacer_FolderCollisionGetsSuffix(t *testing.T) {
	root := t.TempDir()
	stagingA := t.TempDir()
	stagingB := t.TempDir()
	srcA := writeTempFile(t, stagingA, "edition1.epub", "a")
	srcB := writeTempFile(t, stagingB, "edition2.epub", "b")

	p := LocalPlacer{Root: root}

	resA, err := p.Place(context.Background(), PlaceSource{
		Path: srcA, Format: "EPUB", Author: "Tolkien", Title: "The Hobbit",
	})
	if err != nil {
		t.Fatalf("Place A: %v", err)
	}
	if resA.FolderPath != filepath.Join("Tolkien", "The Hobbit") {
		t.Fatalf("A FolderPath=%q", resA.FolderPath)
	}

	resB, err := p.Place(context.Background(), PlaceSource{
		Path: srcB, Format: "EPUB", Author: "Tolkien", Title: "The Hobbit",
	})
	if err != nil {
		t.Fatalf("Place B: %v", err)
	}
	wantB := filepath.Join("Tolkien", "The Hobbit (2)")
	if resB.FolderPath != wantB {
		t.Errorf("B FolderPath=%q want %q", resB.FolderPath, wantB)
	}

	// Both files exist on disk.
	for _, want := range []string{
		filepath.Join(root, resA.Location),
		filepath.Join(root, resB.Location),
	} {
		if _, err := os.Stat(want); err != nil {
			t.Errorf("missing file %q: %v", want, err)
		}
	}
}

func TestLocalPlacer_FileCollisionWithinSameFolder(t *testing.T) {
	root := t.TempDir()
	stagingA := t.TempDir()
	stagingB := t.TempDir()
	srcA := writeTempFile(t, stagingA, "hobbit.epub", "a")
	srcB := writeTempFile(t, stagingB, "hobbit.epub", "b")

	p := LocalPlacer{Root: root}

	if _, err := p.Place(context.Background(), PlaceSource{
		Path: srcA, Format: "EPUB", Author: "Tolkien", Title: "The Hobbit",
	}); err != nil {
		t.Fatalf("Place A: %v", err)
	}

	// Pre-create a placeholder so the directory exists; force B to also
	// see "hobbit.epub" present. Without uniqueDirectory's title suffix
	// we'd land in the same folder; assert that we either get a unique
	// directory OR a unique file basename.
	resB, err := p.Place(context.Background(), PlaceSource{
		Path: srcB, Format: "EPUB", Author: "Tolkien", Title: "The Hobbit",
	})
	if err != nil {
		t.Fatalf("Place B: %v", err)
	}
	if resB.Location == filepath.Join("Tolkien", "The Hobbit", "hobbit.epub") {
		t.Errorf("B overwrote A: Location=%q", resB.Location)
	}
}

func TestLocalPlacer_EmptyRoot(t *testing.T) {
	p := LocalPlacer{Root: ""}
	_, err := p.Place(context.Background(), PlaceSource{Path: "/tmp/x"})
	if err == nil {
		t.Fatal("expected error on empty root")
	}
}

// --- builder -----------------------------------------------------------

// placerObjectStore is a LocalFS that claims to be an object store, so
// the builder sees the capability an s3 backend advertises without the
// test needing a bucket. Bytes still land on disk, which is how the test
// can assert the key BackendPlacer chose.
type placerObjectStore struct{ *local.LocalFS }

func (placerObjectStore) Capabilities() storage.Capability {
	return storage.CapObjectStore
}

// migratedLocalLibrary is the install shape the storage-v2 backfill
// leaves behind: a library that lives on a filesystem *and* carries a
// backend_id, because the backfill seeded one kind=local backend row per
// distinct libraries.path and wired every library to it
// (migrator.wireLibraries). root is copied from path, so the row still
// says where on disk it lives.
func migratedLocalLibrary(t *testing.T, libRoot, backendID string) model.Library {
	t.Helper()
	root := libRoot
	id := backendID
	return model.Library{
		ID:        "lib-1",
		Name:      "Test Library",
		Path:      libRoot,
		Root:      &root,
		BackendID: &id,
	}
}

// A local library that carries a backend row must still place inside its
// own root. libraries.backend_id means "a backend row exists", not "this
// is not local" — the same confusion #202 was about — and every local
// backend is constructed rooted at the whole filesystem
// (storageloader.buildBackend keeps LocalFS at "/"), so a
// library-relative key written through it lands at the instance root
// instead of inside the library (#265).
//
// instanceRoot stands in for the "/" a real local backend is rooted at:
// a temp dir keeps the assertion honest without the test writing to the
// machine's filesystem root.
func TestDefaultPlacerBuilder_MigratedLocalLibraryPlacesInsideTheLibrary(t *testing.T) {
	instanceRoot := t.TempDir()
	libRoot := filepath.Join(instanceRoot, "srv", "books")
	if err := os.MkdirAll(libRoot, 0o755); err != nil {
		t.Fatalf("mkdir library root: %v", err)
	}
	fs, err := local.New(instanceRoot)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}

	const backendID = "backend-local"
	lib := migratedLocalLibrary(t, libRoot, backendID)
	build := DefaultPlacerBuilder(&storage.MapResolver{
		Backends: map[string]storage.Storage{backendID: fs},
	})
	p, err := build(lib)
	if err != nil {
		t.Fatalf("build placer: %v", err)
	}

	src := writeTempFile(t, t.TempDir(), "dune.epub", "epub bytes")
	res, err := p.Place(context.Background(), PlaceSource{
		Path:   src,
		Format: "EPUB",
		Author: "Frank Herbert",
		Title:  "Dune",
	})
	if err != nil {
		t.Fatalf("Place: %v", err)
	}

	wantLocation := filepath.Join("Frank Herbert", "Dune", "dune.epub")
	if res.Location != wantLocation {
		t.Errorf("Location=%q want %q", res.Location, wantLocation)
	}
	if got, want := res.FolderPath, filepath.Join("Frank Herbert", "Dune"); got != want {
		t.Errorf("FolderPath=%q want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(libRoot, wantLocation)); err != nil {
		t.Errorf("bytes are not inside the library: %v", err)
	}
	if _, err := os.Stat(filepath.Join(instanceRoot, wantLocation)); err == nil {
		t.Errorf("bytes landed at the instance root %q — a library-relative key "+
			"written through the %q-rooted local backend", instanceRoot, instanceRoot)
	}
}

// The pre-migration shape: no backend row at all. The builder has always
// been right here, and the fix must leave it alone.
func TestDefaultPlacerBuilder_LocalLibraryWithoutABackendRow(t *testing.T) {
	libRoot := t.TempDir()
	lib := model.Library{ID: "lib-1", Path: libRoot}

	p, err := DefaultPlacerBuilder(nil)(lib)
	if err != nil {
		t.Fatalf("build placer: %v", err)
	}
	src := writeTempFile(t, t.TempDir(), "hobbit.epub", "epub bytes")
	res, err := p.Place(context.Background(), PlaceSource{
		Path: src, Format: "EPUB", Author: "Tolkien", Title: "The Hobbit",
	})
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if _, err := os.Stat(filepath.Join(libRoot, res.Location)); err != nil {
		t.Errorf("bytes are not inside the library: %v", err)
	}
}

// An object-store library keeps BackendPlacer: its backend owns the
// per-library prefix, so the bare {Author}/{Title}/{basename} key is
// already the key the bucket answers to (ADR-0003 §7).
func TestDefaultPlacerBuilder_ObjectStoreLibraryUploadsAtTheBareKey(t *testing.T) {
	bucket := t.TempDir()
	fs, err := local.New(bucket)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}

	const backendID = "backend-s3"
	id := backendID
	// An s3 library has no filesystem of its own: repo.CreateLibrary
	// leaves root empty because the backend encodes the prefix.
	empty := ""
	lib := model.Library{ID: "lib-s3", Root: &empty, BackendID: &id}

	p, err := DefaultPlacerBuilder(&storage.MapResolver{
		Backends: map[string]storage.Storage{backendID: placerObjectStore{fs}},
	})(lib)
	if err != nil {
		t.Fatalf("build placer: %v", err)
	}
	if _, ok := p.(BackendPlacer); !ok {
		t.Fatalf("placer=%T want BackendPlacer", p)
	}

	src := writeTempFile(t, t.TempDir(), "neuromancer.epub", "epub bytes")
	res, err := p.Place(context.Background(), PlaceSource{
		Path: src, Format: "EPUB", Author: "William Gibson", Title: "Neuromancer",
	})
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	wantKey := path.Join("William Gibson", "Neuromancer", "neuromancer.epub")
	if res.Location != wantKey {
		t.Errorf("Location=%q want %q", res.Location, wantKey)
	}
	rc, err := fs.Get(context.Background(), wantKey)
	if err != nil {
		t.Fatalf("uploaded object missing at %q: %v", wantKey, err)
	}
	defer func() { _ = rc.Close() }()
	if b, _ := io.ReadAll(rc); string(b) != "epub bytes" {
		t.Errorf("uploaded bytes = %q", b)
	}
}

// A library with neither an object store nor a root has nowhere to place
// into, and saying so is the whole point of the check: the local backend
// is rooted at "/", so letting the write through would put a book's file
// at the filesystem root.
func TestDefaultPlacerBuilder_RootlessLocalLibraryRefuses(t *testing.T) {
	if _, err := DefaultPlacerBuilder(nil)(model.Library{ID: "lib-1"}); err == nil {
		t.Fatal("expected an error for a library with no root and no backend")
	}
}
