// SPDX-License-Identifier: AGPL-3.0-or-later

package scan_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/blackforge/embookshelf/internal/scan"
	"github.com/blackforge/embookshelf/internal/storage/local"
)

func TestWalk_EmptyDir(t *testing.T) {
	root := t.TempDir()
	store, err := local.New(root)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	ctx := context.Background()
	out, errc := scan.Walk(ctx, store, "")

	var entries []scan.WalkEntry
	for e := range out {
		entries = append(entries, e)
	}
	if err := <-errc; err != nil {
		t.Fatalf("unexpected error from empty dir walk: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestWalk_ThreeFiles(t *testing.T) {
	root := t.TempDir()
	store, err := local.New(root)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}

	// Create 3 known files with distinct content (and thus distinct sizes).
	files := map[string]string{
		"a.epub":        "content of a",
		"subdir/b.epub": "content of b — slightly longer",
		"c.pdf":         "c",
	}
	for rel, content := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile %q: %v", rel, err)
		}
	}

	ctx := context.Background()
	out, errc := scan.Walk(ctx, store, "")

	var entries []scan.WalkEntry
	for e := range out {
		entries = append(entries, e)
	}
	if err := <-errc; err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Location < entries[j].Location })

	for _, e := range entries {
		if e.Size <= 0 {
			t.Errorf("entry %q has non-positive Size %d", e.Location, e.Size)
		}
		if e.Mtime.IsZero() {
			t.Errorf("entry %q has zero Mtime", e.Location)
		}
	}

	// Verify the locations we expect are present.
	locs := make(map[string]bool, len(entries))
	for _, e := range entries {
		locs[e.Location] = true
	}
	for rel := range files {
		if !locs[rel] {
			t.Errorf("expected location %q not found in walk entries", rel)
		}
	}
}

func TestWalk_CancelledContext(t *testing.T) {
	root := t.TempDir()
	store, err := local.New(root)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}

	// Create a file so the walk has something to iterate over.
	abs := filepath.Join(root, "book.epub")
	if err := os.WriteFile(abs, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	out, errc := scan.Walk(ctx, store, "")

	// Drain both channels; at least one should reflect cancellation.
	var entries []scan.WalkEntry
	for e := range out {
		entries = append(entries, e)
	}
	walkErr := <-errc

	// A pre-cancelled context must surface ctx.Err() on errc OR result in
	// zero entries (the iterator itself may short-circuit before sending).
	if walkErr != nil {
		if walkErr != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", walkErr)
		}
		// Good — error path surfaced correctly.
		return
	}
	// If no error was sent, the walk must have returned 0 entries (the
	// context was cancelled before any select could send).
	if len(entries) != 0 {
		t.Fatalf("with cancelled ctx expected 0 entries or ctx.Canceled error, got %d entries and nil err", len(entries))
	}
}
