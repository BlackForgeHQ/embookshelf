// SPDX-License-Identifier: AGPL-3.0-or-later

package service_test

import (
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
)

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
