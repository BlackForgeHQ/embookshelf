// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"path/filepath"
	"testing"
)

func TestSandboxAllowsPathInsideARoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	want := filepath.Join(root, "Author", "Title", "book.epub")

	got, err := sandboxPath(want, []string{root})
	if err != nil {
		t.Fatalf("sandboxPath: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSandboxAllowsTheRootItself(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, err := sandboxPath(root, []string{root}); err != nil {
		t.Fatalf("root itself must be allowed: %v", err)
	}
}

func TestSandboxRejectsPathOutsideEveryRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, err := sandboxPath(filepath.Join(t.TempDir(), "elsewhere.epub"), []string{root}); err == nil {
		t.Fatal("want an error for a path outside every root, got nil")
	}
}

// A sibling directory sharing a name prefix must not pass: /data/lib
// must not admit /data/library-backup.
func TestSandboxRejectsPrefixSiblingDirectory(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	root := filepath.Join(base, "lib")
	sibling := filepath.Join(base, "lib-backup", "book.epub")

	if _, err := sandboxPath(sibling, []string{root}); err == nil {
		t.Fatal("want an error for a prefix-sibling directory, got nil")
	}
}

func TestSandboxRejectsTraversalEscape(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	escape := filepath.Join(root, "..", "..", "etc", "passwd")

	if _, err := sandboxPath(escape, []string{root}); err == nil {
		t.Fatal("want an error for a traversal escape, got nil")
	}
}

// Traversal that stays inside the root resolves and is allowed.
func TestSandboxNormalizesTraversalWithinRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	messy := filepath.Join(root, "a", "..", "b.epub")

	got, err := sandboxPath(messy, []string{root})
	if err != nil {
		t.Fatalf("sandboxPath: %v", err)
	}
	if want := filepath.Join(root, "b.epub"); got != want {
		t.Errorf("got %q, want the cleaned %q", got, want)
	}
}

// No configured roots means nothing is servable or deletable —
// fail closed rather than defaulting to the whole filesystem.
func TestSandboxRejectsWhenNoRootsConfigured(t *testing.T) {
	t.Parallel()

	if _, err := sandboxPath(filepath.Join(t.TempDir(), "x.epub"), nil); err == nil {
		t.Fatal("want an error when no roots are configured, got nil")
	}
}

func TestSandboxIgnoresEmptyRootEntries(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "book.epub")

	if _, err := sandboxPath(target, []string{"", root, ""}); err != nil {
		t.Fatalf("empty root entries must be skipped, not fatal: %v", err)
	}
}
