package service_test

import (
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
)

func TestLibraryHandle_SidecarKey(t *testing.T) {
	h := &service.LibraryHandle{Library: model.Library{ID: "lib1"}}
	cases := []struct {
		bookKey string
		want    string
	}{
		{"folder/harry-potter.epub", "folder/harry-potter.embookshelf.json"},
		{"books/dune.pdf", "books/dune.embookshelf.json"},
		{"audio/dune/disc-1.m4b", "audio/dune/disc-1.embookshelf.json"},
		{"flat-file.epub", "flat-file.embookshelf.json"},
	}
	for _, c := range cases {
		if got := h.SidecarKey(c.bookKey); got != c.want {
			t.Errorf("SidecarKey(%q) = %q, want %q", c.bookKey, got, c.want)
		}
	}
}
