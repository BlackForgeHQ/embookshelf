// SPDX-License-Identifier: AGPL-3.0-or-later

package service_test

import (
	"context"
	"io"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage/local"
)

// TestOpenMarkdownResolvesThePlacedKey — the rendition row stores the
// library-relative location PlaceDerived returned; OpenMarkdown must
// find those bytes again on both backend shapes. The local arm is the
// regression: the local backend is "/"-rooted (ADR-0030), so opening
// the bare location instead of StorageKey(location) reads relative to
// nowhere and misses — which is exactly what the guide feed's first
// wiring did.
func TestOpenMarkdownResolvesThePlacedKey(t *testing.T) {
	t.Parallel()

	book := model.Book{ID: "b1", Title: "Dune", Author: "Frank Herbert", Format: "PDF"}
	const body = "# markdown body\n"

	t.Run("LocalLibraryOnASlashRootedBackend", func(t *testing.T) {
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

		placed, err := h.PlaceDerived(context.Background(), book, tempSource(t, body), service.DerivedMarkdown)
		if err != nil {
			t.Fatalf("PlaceDerived: %v", err)
		}

		src, err := h.OpenMarkdown(context.Background(), placed.Location)
		if err != nil {
			t.Fatalf("OpenMarkdown: %v", err)
		}
		defer func() { _ = src.Close() }()
		got, err := io.ReadAll(io.NewSectionReader(src, 0, src.Size()))
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(got) != body {
			t.Fatalf("read back %q, want %q", got, body)
		}

		// The bare location is the bug this method exists to prevent.
		if _, err := h.Storage.Open(context.Background(), placed.Location); err == nil {
			t.Fatal("plain Open(location) found the bytes — the regression this test pins no longer applies")
		}
	})

	t.Run("ObjectStoreBackedLibrary", func(t *testing.T) {
		t.Parallel()

		bucket := t.TempDir()
		fs, err := local.New(bucket)
		if err != nil {
			t.Fatalf("local.New: %v", err)
		}
		h := &service.LibraryHandle{
			Library: model.Library{ID: "lib1"},
			Storage: objectStoreFS{fs},
		}

		placed, err := h.PlaceDerived(context.Background(), book, tempSource(t, body), service.DerivedMarkdown)
		if err != nil {
			t.Fatalf("PlaceDerived: %v", err)
		}
		src, err := h.OpenMarkdown(context.Background(), placed.Location)
		if err != nil {
			t.Fatalf("OpenMarkdown: %v", err)
		}
		defer func() { _ = src.Close() }()
		got, err := io.ReadAll(io.NewSectionReader(src, 0, src.Size()))
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(got) != body {
			t.Fatalf("read back %q, want %q", got, body)
		}
	})
}
