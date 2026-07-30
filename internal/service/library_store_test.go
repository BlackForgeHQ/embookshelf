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
