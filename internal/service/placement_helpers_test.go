// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/blackforge/embookshelf/internal/storage/local"
)

// The placement arithmetic, tested directly (#323). It used to be
// reachable only through a Placer or a Renamer, which is why the
// renamer's raw prefix trim could return an absolute path for years
// without a test noticing: nothing ever asked it a question it could
// get wrong.

func TestLibRootAbsJoinsOntoTheRoot(t *testing.T) {
	r := newLibRoot("/lib")

	if got := r.abs("Tolkien/The Hobbit"); got != filepath.Join("/lib", "Tolkien", "The Hobbit") {
		t.Errorf("abs = %q", got)
	}
}

func TestLibRootTrimsATrailingSlashSoTheArithmeticIsStable(t *testing.T) {
	r := newLibRoot("/lib/")

	got, err := r.rel("/lib/Tolkien/hobbit.epub")
	if err != nil {
		t.Fatalf("rel: %v", err)
	}
	if got != "Tolkien/hobbit.epub" {
		t.Errorf("rel = %q, want the library-relative form", got)
	}
}

func TestLibRootRoundTrips(t *testing.T) {
	r := newLibRoot(t.TempDir())
	const want = "Tolkien/The Hobbit/hobbit.epub"

	got, err := r.rel(r.abs(want))
	if err != nil {
		t.Fatalf("rel: %v", err)
	}
	if got != want {
		t.Errorf("round trip = %q, want %q", got, want)
	}
}

// The failure this type exists for. The renamer trimmed the prefix by
// hand and handed the absolute path back when it did not match, and
// that value went into books.folder_path — the very rows ADR-0030 says
// a migration would have had to clean up.
func TestLibRootRelRefusesAPathOutsideTheRootRatherThanReturningIt(t *testing.T) {
	r := newLibRoot("/lib")

	for _, abs := range []string{
		"/elsewhere/Tolkien/hobbit.epub",
		"/library-of-congress/hobbit.epub", // shares a textual prefix, not a directory
		"/lib",                             // the root itself is not a folder in the library
		"relative/already.epub",
	} {
		got, err := r.rel(abs)
		if err == nil {
			t.Errorf("rel(%q) = %q with no error — an absolute write in disguise", abs, got)
		}
		if !errors.Is(err, errOutsideRoot) {
			t.Errorf("rel(%q) err = %v, want errOutsideRoot", abs, err)
		}
		if got != "" {
			t.Errorf("rel(%q) returned %q alongside its error", abs, got)
		}
	}
}

func TestLibRootWithNoRootRefusesTheArithmetic(t *testing.T) {
	r := newLibRoot("")

	if !r.empty() {
		t.Fatal("empty root did not report itself empty")
	}
	if _, err := r.rel("/lib/hobbit.epub"); err == nil {
		t.Error("rel on an unconfigured root answered without an error")
	}
	if _, _, err := r.freeDir("Tolkien/The Hobbit"); err == nil {
		t.Error("freeDir on an unconfigured root answered without an error")
	}
}

func TestFreeDirReturnsBothFormsOfAFreeDirectory(t *testing.T) {
	r := newLibRoot(t.TempDir())

	abs, rel, err := r.freeDir("Tolkien/The Hobbit")
	if err != nil {
		t.Fatalf("freeDir: %v", err)
	}
	if rel != "Tolkien/The Hobbit" {
		t.Errorf("rel = %q, want the folder asked for", rel)
	}
	if abs != r.abs("Tolkien/The Hobbit") {
		t.Errorf("abs = %q", abs)
	}
}

func TestFreeDirProbesPastACollision(t *testing.T) {
	r := newLibRoot(t.TempDir())
	mkdirAll(t, r.abs("Tolkien/The Hobbit"))
	mkdirAll(t, r.abs("Tolkien/The Hobbit (2)"))

	abs, rel, err := r.freeDir("Tolkien/The Hobbit")
	if err != nil {
		t.Fatalf("freeDir: %v", err)
	}
	if rel != "Tolkien/The Hobbit (3)" {
		t.Errorf("rel = %q, want the first free suffix", rel)
	}
	if abs != r.abs(rel) {
		t.Errorf("the two forms disagree: %q vs %q", abs, r.abs(rel))
	}
}

// The renamer's own source directory: a rename must be allowed to land
// on the folder it is renaming from, or a no-op rename would bump
// itself to " (2)".
func TestFreeDirTreatsAnExceptedDirectoryAsFree(t *testing.T) {
	r := newLibRoot(t.TempDir())
	same := r.abs("Tolkien/The Hobbit")
	mkdirAll(t, same)

	abs, rel, err := r.freeDir("Tolkien/The Hobbit", same)
	if err != nil {
		t.Fatalf("freeDir: %v", err)
	}
	if abs != same || rel != "Tolkien/The Hobbit" {
		t.Errorf("freeDir = (%q, %q), want the excepted directory unchanged", abs, rel)
	}
}

// The exception is the folder asked for, not every candidate after it —
// uniqueDirectoryUnless's rule, which scoped it to the first. Here the
// folder asked for belongs to another Book and the source is the " (2)"
// beside it: the walk steps over the source rather than stopping on it,
// so the Book actually moves instead of being told it landed where it
// already was.
func TestFreeDirWalksPastAnExceptedDirectoryItWasNotAskedFor(t *testing.T) {
	r := newLibRoot(t.TempDir())
	mkdirAll(t, r.abs("Tolkien/The Hobbit"))  // another Book's folder
	source := r.abs("Tolkien/The Hobbit (2)") // ours, where we are now
	mkdirAll(t, source)

	abs, rel, err := r.freeDir("Tolkien/The Hobbit", source)
	if err != nil {
		t.Fatalf("freeDir: %v", err)
	}
	if rel != "Tolkien/The Hobbit (3)" {
		t.Errorf("rel = %q, want the first free suffix past the source", rel)
	}
	if abs == source {
		t.Error("the source folder was handed back as the destination — a rename that reports done without moving")
	}
}

func TestFreeDirRefusesAFolderThatIsNotInsideTheLibrary(t *testing.T) {
	r := newLibRoot(t.TempDir())

	// "" and "." are the library root itself, which is not a folder in
	// the library; ".." escapes it. Before, each of these probed from
	// the root and produced an absolute folder_path.
	for _, folder := range []string{"", ".", "..", "../escape"} {
		if abs, rel, err := r.freeDir(folder); err == nil {
			t.Errorf("freeDir(%q) = (%q, %q) with no error", folder, abs, rel)
		}
	}

	// An absolute folder is confined rather than refused — filepath.Join
	// reads it as relative to the root, which is where the Book belongs
	// anyway. Pinned because it is the one input of this shape that does
	// not error.
	abs, rel, err := r.freeDir("/absolute")
	if err != nil {
		t.Fatalf("freeDir: %v", err)
	}
	if rel != "absolute" || abs != r.abs("absolute") {
		t.Errorf("freeDir(\"/absolute\") = (%q, %q), want it confined under the root", abs, rel)
	}
}

func TestFreeFilePathKeepsTheExtensionWhenItSuffixes(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "hobbit.epub")

	if got := freeFilePath(dest); got != dest {
		t.Errorf("a free name was changed: %q", got)
	}

	writeTempFile(t, dir, "hobbit.epub", "x")
	want := filepath.Join(dir, "hobbit (2).epub")
	if got := freeFilePath(dest); got != want {
		t.Errorf("freeFilePath = %q, want %q", got, want)
	}
}

// The object-store counterpart: no root arithmetic, because an object
// store's keys are already library-relative (keyRoot, ADR-0030 §1), so
// only the collision probe survives.
func TestBackendRootProbesForObjectsUnderThePrefix(t *testing.T) {
	dir := t.TempDir()
	store, err := local.New(dir)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	ctx := context.Background()

	got, err := backendRoot().freeDirBackend(ctx, store, "Tolkien/The Hobbit")
	if err != nil {
		t.Fatalf("freeDirBackend: %v", err)
	}
	if got != "Tolkien/The Hobbit" {
		t.Errorf("an empty prefix was suffixed: %q", got)
	}

	occupied := filepath.Join(dir, "Tolkien", "The Hobbit")
	mkdirAll(t, occupied)
	writeTempFile(t, occupied, "hobbit.epub", "x")

	got, err = backendRoot().freeDirBackend(ctx, store, "Tolkien/The Hobbit")
	if err != nil {
		t.Fatalf("freeDirBackend: %v", err)
	}
	if got != "Tolkien/The Hobbit (2)" {
		t.Errorf("freeDirBackend = %q, want the first free prefix", got)
	}
}

// The probe answers in library-relative keys, so asking it of a rooted
// library would hand a local caller a path it cannot use. An error, not
// a plausible-looking string.
func TestFreeDirBackendRefusesARootedLibrary(t *testing.T) {
	store, err := local.New(t.TempDir())
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}

	if got, err := newLibRoot("/lib").freeDirBackend(context.Background(), store, "Tolkien"); err == nil {
		t.Errorf("freeDirBackend on a rooted library = %q, want an error", got)
	}
}

func mkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}
