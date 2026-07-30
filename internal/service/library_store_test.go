// SPDX-License-Identifier: AGPL-3.0-or-later

package service_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/scan"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
)

// objectStoreFS is a filesystem pretending to be an object store: it
// advertises the capability the key rule branches on and, like a real
// backend, is rooted at its own per-library prefix.
type objectStoreFS struct{ storage.Storage }

func (objectStoreFS) Capabilities() storage.Capability { return storage.CapObjectStore }

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func walkedLocations(entries []scan.WalkEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Location)
	}
	sort.Strings(out)
	return out
}

// The walk answers in the vocabulary the files table speaks — locations
// relative to the library root — whichever kind of backend the library
// is pinned to. That is the whole point of asking the handle: the caller
// stops choosing a root and stops deciding whether to relativize, which
// is where the scan worker got it wrong for every S3 library (#203).
func TestWalkYieldsLibraryRelativeLocations(t *testing.T) {
	t.Parallel()

	const (
		book    = "Kobo Abe/Woman in the Dunes/dunes.epub"
		sidecar = "Kobo Abe/Woman in the Dunes/metadata.embookshelf.json"
	)

	t.Run("LocalLibraryOnASlashRootedBackend", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, root, book, "dunes")
		writeFile(t, root, sidecar, "{}")

		// Exactly what boot builds for an install with no storage backend
		// row: one LocalFS rooted at "/" for the whole instance
		// (ADR-0030 §1).
		rootedAtSlash, err := local.New("/")
		if err != nil {
			t.Fatalf("local.New: %v", err)
		}
		h := &service.LibraryHandle{
			Library: model.Library{ID: "lib1", Root: &root},
			Storage: rootedAtSlash,
		}

		entries, err := h.Walk(context.Background())
		if err != nil {
			t.Fatalf("Walk: %v", err)
		}
		got := walkedLocations(entries)
		want := []string{book, sidecar}
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("Walk locations = %q, want %q", got, want)
		}
		for _, e := range entries {
			if e.Size <= 0 || e.Mtime.IsZero() {
				t.Errorf("entry %q lost its Size/Mtime: %+v", e.Location, e)
			}
		}
	})

	t.Run("ObjectStoreLibraryRootedAtItsOwnPrefix", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, root, book, "dunes")

		fs, err := local.New(root)
		if err != nil {
			t.Fatalf("local.New: %v", err)
		}
		// An S3 library carries no root by design — the backend encodes
		// its own libraries/{slug}/ prefix — and that emptiness must not
		// read as "not configured".
		h := &service.LibraryHandle{
			Library: model.Library{ID: "lib1"},
			Storage: objectStoreFS{fs},
		}

		entries, err := h.Walk(context.Background())
		if err != nil {
			t.Fatalf("Walk: %v", err)
		}
		if got := walkedLocations(entries); len(got) != 1 || got[0] != book {
			t.Fatalf("Walk locations = %q, want [%q]", got, book)
		}
	})

	// The root as an admin spelled it is not the root as the filesystem
	// reports it back. A redundant separator is enough: the walk lists
	// under the cleaned path, so every key comes back cleaned, and a
	// prefix-strip that compares against the raw spelling matches
	// nothing and falls through to emitting the absolute path. Every
	// walked entry then reads New and every row reads Missing — the
	// whole library soft-flagged for the purge sweeper.
	t.Run("LocalRootSpelledNonCanonically", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, root, book, "dunes")

		rootedAtSlash, err := local.New("/")
		if err != nil {
			t.Fatalf("local.New: %v", err)
		}
		spelled := root + "//"
		h := &service.LibraryHandle{
			Library: model.Library{ID: "lib1", Root: &spelled},
			Storage: rootedAtSlash,
		}

		entries, err := h.Walk(context.Background())
		if err != nil {
			t.Fatalf("Walk: %v", err)
		}
		if got := walkedLocations(entries); len(got) != 1 || got[0] != book {
			t.Fatalf("Walk locations = %q, want [%q] — a walk that yields "+
				"absolute locations makes every row in the library read Missing",
				got, book)
		}
	})
}

// The key travels with the location because the walk already knows it.
// The scan used to relativize each entry and then immediately re-derive
// a storage key from it, a round trip through the key shim on every
// entry — and a shim whose local branch only works because the backend
// is rooted at "/".
func TestWalkCarriesTheKeyItListedTheObjectUnder(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "Author/Title/book.epub", "bytes")
	rootedAtSlash, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	h := &service.LibraryHandle{
		Library: model.Library{ID: "lib1", Root: &root},
		Storage: rootedAtSlash,
	}

	entries, err := h.Walk(context.Background())
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	// Whatever shape it is in, it must address the bytes: that is the
	// one thing the conformance suite guarantees of any backend
	// (storagetest, KeyShapesNameTheSameObject).
	rc, err := h.Storage.Get(context.Background(), entries[0].Key)
	if err != nil {
		t.Fatalf("Get(%q) on the key the walk reported: %v", entries[0].Key, err)
	}
	_ = rc.Close()
}

// A local library with no root really is unconfigured, and the walk has
// to say so in a way the caller can tell apart from a walk that failed
// partway: one is a state to report, the other must not be allowed to
// look like "the library is empty" and flag every row missing.
func TestWalkRefusesALocalLibraryWithNoRoot(t *testing.T) {
	t.Parallel()

	rootedAtSlash, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	h := &service.LibraryHandle{Library: model.Library{ID: "lib1"}, Storage: rootedAtSlash}

	if _, err := h.Walk(context.Background()); !errors.Is(err, service.ErrNoWalkRoot) {
		t.Fatalf("Walk on a rootless local library = %v, want ErrNoWalkRoot", err)
	}
}

// tempSource is a file in the shape placement is always handed one: a
// temp file somewhere outside the library, named by whoever produced the
// bytes rather than by where they are going.
func tempSource(t *testing.T, body string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "embookshelf-audiobook-*.mp3")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	if _, err := f.WriteString(body); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return f.Name()
}

// Placing into a book that already exists is not placing a new book. The
// destination is a location the library already has a name for, and the
// operation's whole job is to write it there and nowhere else — no
// collision suffix, no basename inherited from a temp file. Whether that
// write is a put to a bucket or a file on disk is the handle's business,
// which is the same rooting question Walk answers for listing.
func TestPlaceAtWritesToTheLibrarysOwnKey(t *testing.T) {
	t.Parallel()

	const (
		location = "Kobo Abe/Woman in the Dunes/Woman in the Dunes.mp3"
		folder   = "Kobo Abe/Woman in the Dunes"
		body     = "narration-bytes"
	)

	t.Run("ObjectStoreBackedLibrary", func(t *testing.T) {
		t.Parallel()

		// An object store owns its own per-library prefix, so a stored
		// location is already the key it answers to. Backed by a real
		// filesystem rather than a map so the put actually streams.
		bucket := t.TempDir()
		fs, err := local.New(bucket)
		if err != nil {
			t.Fatalf("local.New: %v", err)
		}
		h := &service.LibraryHandle{
			Library: model.Library{ID: "lib1"},
			Storage: objectStoreFS{fs},
		}

		src := tempSource(t, body)
		got, err := h.PlaceAt(context.Background(), location, src, "MP3")
		if err != nil {
			t.Fatalf("PlaceAt: %v", err)
		}

		if got.Location != location {
			t.Errorf("Location = %q, want %q", got.Location, location)
		}
		if got.FolderPath != folder {
			t.Errorf("FolderPath = %q, want %q", got.FolderPath, folder)
		}
		if got.Size != int64(len(body)) {
			t.Errorf("Size = %d, want %d", got.Size, len(body))
		}
		b, err := os.ReadFile(filepath.Join(bucket, filepath.FromSlash(location)))
		if err != nil {
			t.Fatalf("object not at the library's own key: %v", err)
		}
		if string(b) != body {
			t.Errorf("object = %q, want %q", b, body)
		}
		// The bytes are durable in the backend; keeping the temp copy
		// fills the data volume one half-gigabyte narration at a time.
		if _, err := os.Stat(src); !os.IsNotExist(err) {
			t.Errorf("the temp source survived the put: %v", err)
		}
	})

	t.Run("LocalLibraryOnASlashRootedBackend", func(t *testing.T) {
		t.Parallel()

		// The local backend is rooted at "/" for the whole instance
		// (ADR-0030 §1), so the same location has to be resolved against
		// the library root before it names anything.
		root := t.TempDir()
		rootedAtSlash, err := local.New("/")
		if err != nil {
			t.Fatalf("local.New: %v", err)
		}
		h := &service.LibraryHandle{
			Library: model.Library{ID: "lib1", Root: &root},
			Storage: rootedAtSlash,
		}
		// The book's folder already exists holding its EPUB — the exact
		// condition under which the bookdrop placer picks a "Title (2)"
		// sibling, which a later scan reads as a second book.
		writeFile(t, root, folder+"/dunes.epub", "epub")

		src := tempSource(t, body)
		got, err := h.PlaceAt(context.Background(), location, src, "MP3")
		if err != nil {
			t.Fatalf("PlaceAt: %v", err)
		}

		// Library-relative, both of them: what the files row stores.
		if got.Location != location {
			t.Errorf("Location = %q, want %q", got.Location, location)
		}
		if got.FolderPath != folder {
			t.Errorf("FolderPath = %q, want %q", got.FolderPath, folder)
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(location)))
		if err != nil {
			t.Fatalf("file not under the library root: %v", err)
		}
		if string(b) != body {
			t.Errorf("file = %q, want %q", b, body)
		}
		// One folder, both files. Nothing may have appeared beside it.
		entries, err := os.ReadDir(filepath.Join(root, "Kobo Abe"))
		if err != nil {
			t.Fatalf("readdir: %v", err)
		}
		if len(entries) != 1 || entries[0].Name() != "Woman in the Dunes" {
			t.Errorf("author folder holds %d entries, want exactly the book's own folder", len(entries))
		}
		if _, err := os.Stat(src); !os.IsNotExist(err) {
			t.Errorf("the temp source survived the write: %v", err)
		}
	})
}

// A local library with no root has nowhere to write, and the one thing
// that must not happen is for the location to be taken as a key in its
// own right: the backend is rooted at "/", so an unrooted placement puts
// a book's file at the filesystem root. Same distinction Walk draws, for
// the same reason — "unconfigured" is a state to report, not a default.
func TestPlaceAtRefusesALocalLibraryWithNoRoot(t *testing.T) {
	t.Parallel()

	rootedAtSlash, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	h := &service.LibraryHandle{Library: model.Library{ID: "lib1"}, Storage: rootedAtSlash}

	src := tempSource(t, "narration")
	_, err = h.PlaceAt(context.Background(), "Kobo Abe/Woman in the Dunes/x.mp3", src, "MP3")
	if !errors.Is(err, service.ErrNoPlaceRoot) {
		t.Fatalf("PlaceAt on a rootless local library = %v, want ErrNoPlaceRoot", err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("a refused placement consumed the source: %v", err)
	}
}

// A placement that fails must leave the library exactly as it found it.
// The destination here is a directory, so the write gets as far as the
// backend's temp file and then cannot land it — the case that a
// hand-rolled copy-then-rename leaves a truncated .mp3 or a stray .tmp
// at the book's own key, where the next scan indexes it as the
// narration.
func TestPlaceAtLeavesNothingBehindWhenTheWriteFails(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	rootedAtSlash, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	h := &service.LibraryHandle{
		Library: model.Library{ID: "lib1", Root: &root},
		Storage: rootedAtSlash,
	}
	const location = "Kobo Abe/Woman in the Dunes/Woman in the Dunes.mp3"
	blocked := filepath.Join(root, filepath.FromSlash(location))
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	src := tempSource(t, "narration-bytes")
	if _, err := h.PlaceAt(context.Background(), location, src, "MP3"); err == nil {
		t.Fatal("PlaceAt reported success writing over a directory")
	}
	// The source is still the caller's to retry with or reap.
	if _, err := os.Stat(src); err != nil {
		t.Errorf("a failed placement consumed the source: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(blocked))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("book folder holds %v, want only the blocked destination — "+
			"a failed placement left a partial artifact behind", names)
	}
}

func TestLibraryHandle_SidecarKey(t *testing.T) {
	// Per ADR-0003 §8 sidecar lives at LeafBook folder root as
	// `metadata.embookshelf.json`, one per Book.
	h := &service.LibraryHandle{Library: model.Library{ID: "lib1"}}
	cases := []struct {
		bookKey string
		want    string
	}{
		{"Tolkien/The Hobbit/hobbit.epub", "Tolkien/The Hobbit/metadata.embookshelf.json"},
		{"Tolkien/The Hobbit/hobbit.mp3", "Tolkien/The Hobbit/metadata.embookshelf.json"},
		{"books/dune.pdf", "books/metadata.embookshelf.json"},
		{"flat-file.epub", "metadata.embookshelf.json"},
	}
	for _, c := range cases {
		if got := h.SidecarKey(c.bookKey); got != c.want {
			t.Errorf("SidecarKey(%q) = %q, want %q", c.bookKey, got, c.want)
		}
	}
}

// libListFunc is the whole of the library listing the sandbox roots
// need: every registered library, so each can offer its local root.
type libListFunc func(context.Context) ([]model.Library, error)

func (f libListFunc) List(ctx context.Context) ([]model.Library, error) { return f(ctx) }

func libsOf(libs ...model.Library) libListFunc {
	return func(context.Context) ([]model.Library, error) { return libs, nil }
}

func ptr(s string) *string { return &s }

// The fail-closed case, which is the whole reason the roots are a named
// thing rather than an inline loop: an install with no BookDrop and no
// library that lives on a filesystem hands the sandbox nothing, and
// nothing must admit nothing. A roots collector that quietly returned
// "/" — or that a caller defaulted past — turns the gate into a pass.
func TestBookFileRootsAdmitNothingWhenNoneAreConfigured(t *testing.T) {
	t.Parallel()

	// Two libraries that exist but own no filesystem: the s3 shape, where
	// path and root are both empty because the backend encodes the prefix.
	libs := libsOf(
		model.Library{ID: "s3-a"},
		model.Library{ID: "s3-b", Root: ptr("")},
	)

	roots := service.BookFileRoots(context.Background(), "", libs)
	if len(roots) != 0 {
		t.Fatalf("libraries with no local root contributed %v, want none", roots)
	}

	if _, err := service.SandboxPath(filepath.Join(t.TempDir(), "book.epub"), roots); !errors.Is(
		err, service.ErrPathOutsideRoots,
	) {
		t.Fatalf("no configured roots admitted a path (err %v), want ErrPathOutsideRoots", err)
	}
}

// The roots are only half the gate; pinning them against SandboxPath is
// what says the pair still refuses an escape. A books.path that climbs
// out of the library it claims to belong to is the shape that matters —
// the row is attacker-influenced on the delete side, where the sandbox
// is all that stands between a malformed path and an unlink.
func TestBookFileRootsRefuseATraversalOutOfALibraryRoot(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	libRoot := filepath.Join(base, "library")
	roots := service.BookFileRoots(
		context.Background(), "", libsOf(model.Library{ID: "lib", Path: libRoot}),
	)

	escape := filepath.Join(libRoot, "..", "elsewhere", "book.epub")
	if _, err := service.SandboxPath(escape, roots); !errors.Is(err, service.ErrPathOutsideRoots) {
		t.Fatalf("traversal out of the library root returned %v, want ErrPathOutsideRoots", err)
	}

	// A prefix sibling is the same refusal by a different route: the
	// library root must not vouch for a directory that merely starts
	// with its name.
	sibling := filepath.Join(base, "library-backup", "book.epub")
	if _, err := service.SandboxPath(sibling, roots); !errors.Is(err, service.ErrPathOutsideRoots) {
		t.Fatalf("prefix-sibling directory returned %v, want ErrPathOutsideRoots", err)
	}

	inside := filepath.Join(libRoot, "Author", "Title", "book.epub")
	if _, err := service.SandboxPath(inside, roots); err != nil {
		t.Fatalf("a file inside the library root was refused: %v", err)
	}
}

// Both sources have to survive the consolidation, and the library half
// has to read the column that is actually populated: storage-v2 rows
// answer with root, pre-storage-v2 rows with path, and a roots collector
// that only ever read path would leave a migrated library unservable.
func TestBookFileRootsCoverBookDropAndEveryLocalLibrary(t *testing.T) {
	t.Parallel()

	drop := t.TempDir()
	legacy := t.TempDir()
	migrated := t.TempDir()

	roots := service.BookFileRoots(context.Background(), drop, libsOf(
		model.Library{ID: "legacy", Path: legacy},
		model.Library{ID: "migrated", Root: ptr(migrated)},
		model.Library{ID: "s3"},
	))

	for _, root := range []string{drop, legacy, migrated} {
		if _, err := service.SandboxPath(filepath.Join(root, "book.epub"), roots); err != nil {
			t.Errorf("a file under %q was refused: %v", root, err)
		}
	}
	if len(roots) != 3 {
		t.Errorf("roots = %v, want exactly the three configured ones", roots)
	}
}

// A catalog that will not answer must not widen the gate. The sandbox
// degrades to the roots it does have, which on the delete side means a
// skipped unlink rather than an unlink aimed anywhere.
func TestBookFileRootsDegradeToBookDropWhenTheCatalogFails(t *testing.T) {
	t.Parallel()

	drop := t.TempDir()
	failing := libListFunc(func(context.Context) ([]model.Library, error) {
		return nil, errors.New("catalog unavailable")
	})

	roots := service.BookFileRoots(context.Background(), drop, failing)
	if len(roots) != 1 || roots[0] != drop {
		t.Fatalf("roots = %v, want only the BookDrop staging area", roots)
	}
	if _, err := service.SandboxPath(
		filepath.Join(t.TempDir(), "book.epub"), roots,
	); !errors.Is(err, service.ErrPathOutsideRoots) {
		t.Fatalf("a failed listing admitted an unrelated path (err %v)", err)
	}
}
