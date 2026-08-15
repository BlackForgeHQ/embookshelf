// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/blackforge/embookshelf/internal/storage"
)

// The naming and moving primitives shared by the two modules that put
// book bytes where they belong: the Placer (naming a destination for a
// new arrival) and the FolderRenamer (moving an existing Book to match
// its metadata). Both need "this name, unless it is taken" and "move
// this file without clobbering"; neither should have to read the
// other's module to get them (#256).
//
// The naming half is libRoot's, because a free name is only half the
// question — every caller then has to say the same directory in both
// vocabularies, absolute for the filesystem and library-relative for
// files.location and books.folder_path, and doing that conversion by
// hand at each call site is what produced two implementations of it
// (#323).

// libRoot is a library's on-disk root, and the one owner of the
// arithmetic between an absolute path and the library-relative form
// that files.location and books.folder_path are stored in.
//
// It holds a root; it does not choose one. Which reading of "the root"
// a caller passes stays the caller's business: the Placer builds from
// libraryLocalRoot's root-then-path preference, the renamer from
// Library.Path alone, and ADR-0030's Consequences files reconciling
// those two columns as an open item of its own. Unifying them here
// would move books for reasons nobody asked for.
//
// The zero value is the object-store root: empty, because such a
// library has no filesystem and its stored locations are already the
// keys its backend answers to (the keys seam, ADR-0030 §1). Only the
// collision probe is meaningful on it, and freeDirBackend is the one
// method that accepts it.
type libRoot struct {
	dir string
}

// newLibRoot takes a library root as configured — trailing slash
// tolerated, since the two columns that hold one disagree about it.
func newLibRoot(dir string) libRoot {
	return libRoot{dir: strings.TrimRight(dir, "/")}
}

// backendRoot is the root of an object-store library: none. Named so
// the call sites read as a deliberate choice rather than a forgotten
// field.
func backendRoot() libRoot { return libRoot{} }

// empty reports a library with no filesystem root — an object store by
// design, or a local library that has not been configured yet. The
// callers tell those two apart in their own vocabulary (ErrNoPlaceRoot,
// "library has no root configured"), because what it costs differs.
func (r libRoot) empty() bool { return r.dir == "" }

// errOutsideRoot is what rel answers with instead of an absolute path.
//
// The single relativize used to be two: a documented defensive fallback
// in the Placer and a raw strings.TrimPrefix twice in the renamer, both
// of which returned the absolute path unchanged when the prefix did not
// match. On the renamer's path that value was persisted — an absolute
// books.folder_path, which ADR-0030's Consequences names as the rows a
// migration would have had to clean up. A path that is not inside the
// library is now an error at the point it is noticed, so no caller can
// write one without saying so.
var errOutsideRoot = errors.New("path is outside the library root")

// abs joins a library-relative path onto the root. Callers check empty
// first — on an empty root this is just the cleaned input, which is
// right for an object store and meaningless for an unconfigured one.
func (r libRoot) abs(rel string) string {
	return filepath.Join(r.dir, filepath.FromSlash(rel))
}

// rel is the inverse, and the only relativize in the package. It is a
// prefix test rather than filepath.Rel because "not under this root" is
// an error here, not a path expressible with "..".
func (r libRoot) rel(abs string) (string, error) {
	if r.empty() {
		return "", fmt.Errorf("relativize %q: library has no root: %w", abs, errOutsideRoot)
	}
	prefix := r.dir + string(filepath.Separator)
	if !strings.HasPrefix(abs, prefix) || len(abs) == len(prefix) {
		return "", fmt.Errorf("relativize %q against %q: %w", abs, r.dir, errOutsideRoot)
	}
	return filepath.ToSlash(abs[len(prefix):]), nil
}

// freeDir resolves a library-relative folder to one that no other Book
// occupies, walking " (2)", " (3)", … suffixes past any directory that
// already exists — the guard against placing one Book's files into
// another Book's folder when `{Author}/{Title}/` collides.
//
// It returns both forms because every caller needs both: the absolute
// one to create and move into, the relative one to persist. Returning
// only the absolute is what left each caller trimming the root back off
// by hand.
//
// A directory listed in except is treated as free when it is the folder
// asked for. That is the renamer's own source folder: a rename must be
// allowed to land on the directory it is renaming from.
//
// The exception scopes to that first candidate and not to the suffixed
// ones after it, which is uniqueDirectoryUnless's rule kept exactly. It
// matters in one case: when the folder asked for is occupied by another
// Book and the source happens to be the ` (2)` beside it, the walk steps
// over the source to ` (3)` and the Book moves, rather than stopping on
// the folder it is already in and reporting a rename that did not
// happen.
func (r libRoot) freeDir(rel string, except ...string) (string, string, error) {
	if r.empty() {
		return "", "", fmt.Errorf("resolve folder %q: library has no root: %w", rel, errOutsideRoot)
	}
	want := r.abs(rel)
	// Not a redundant check: filepath.Join cleans, so "..", "" and an
	// absolute rel all land somewhere that is not inside the library,
	// and probing from there would create a sibling of the library root
	// (or bump the root itself to " (2)").
	if _, err := r.rel(want); err != nil {
		return "", "", err
	}
	free := want
	if !slices.Contains(except, want) {
		free = firstFreeName(want, "", func(cand string) bool {
			_, err := os.Stat(cand)
			return !os.IsNotExist(err)
		})
	}
	relDir, err := r.rel(free)
	if err != nil {
		return "", "", err
	}
	return free, relDir, nil
}

// freeDirBackend is freeDir for an object store, which has no native
// directories: collision is detected by probing for any object under
// the candidate prefix via List(), and the first prefix that returns no
// objects wins.
//
// It answers in one form rather than two because for an object store
// the two coincide — the backend encodes any per-library prefix, so the
// key is already the library-relative location. Which is also why it
// refuses a rooted libRoot: the answer would be a relative prefix
// handed to a caller that needs an absolute path.
func (r libRoot) freeDirBackend(ctx context.Context, store storage.Storage, folder string) (string, error) {
	if !r.empty() {
		return "", fmt.Errorf("backend folder probe on a filesystem-rooted library %q: %w", r.dir, errOutsideRoot)
	}
	return firstFreeName(folder, "", func(cand string) bool {
		return backendFolderHasObjects(ctx, store, cand)
	}), nil
}

// firstFreeName walks " (2)", " (3)", … suffixes until taken says no,
// inserting the suffix before ext so a filename keeps its extension.
// ext is empty for a directory or an object-store prefix, where the
// suffix simply goes at the end.
//
// Returns name unchanged when it is already free, and gives up at
// 10_000 — the bound the three hand-written copies of this loop shared.
func firstFreeName(name, ext string, taken func(string) bool) string {
	if !taken(name) {
		return name
	}
	stem := strings.TrimSuffix(name, ext)
	for i := 2; i < 10_000; i++ {
		cand := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if !taken(cand) {
			return cand
		}
	}
	return name
}

// moveFile atomically moves src to dest, falling back to copy+remove
// when os.Rename can't cross filesystems. The destination directory
// is created as needed.
func moveFile(src, dest string) error {
	if src == dest {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if err := os.Rename(src, dest); err == nil {
		return nil
	}
	if err := copyFile(src, dest); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil {
		// Leave the copy in place so the DB row still points to a
		// valid file; log so the admin can reap the source manually.
		slog.Warn("copy succeeded but source remove failed",
			"src", src, "err", err)
	}
	return nil
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open src: %w", err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create dest: %w", err)
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("fsync: %w", err)
	}
	return nil
}

// freeFilePath is the file-level collision probe: " (2)", " (3)", …
// before the extension, so a suffixed EPUB is still an EPUB. Returns
// the input unchanged if nothing is there.
//
// Not a libRoot method because it needs no root — the directory it
// probes in came from freeDir, which already established that the
// directory is inside the library.
func freeFilePath(dest string) string {
	return firstFreeName(dest, filepath.Ext(dest), func(cand string) bool {
		_, err := os.Stat(cand)
		return !os.IsNotExist(err)
	})
}

// backendFolderHasObjects returns true if any object exists under the
// given prefix. List errors are conservative — treat as "has objects"
// so we keep walking suffixes rather than overwrite. The trailing slash
// is added so the prefix matches a folder, not a basename.
func backendFolderHasObjects(ctx context.Context, store storage.Storage, folder string) bool {
	prefix := folder + "/"
	it, err := store.List(ctx, prefix)
	if err != nil {
		return true
	}
	defer func() { _ = it.Close() }()
	if _, err := it.Next(ctx); err == nil {
		return true
	} else if !errors.Is(err, io.EOF) {
		return true
	}
	return false
}
