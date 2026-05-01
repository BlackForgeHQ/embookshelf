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

func TestLibraryHandle_CanWriteInFile_LocalBackend(t *testing.T) {
	h := &service.LibraryHandle{
		Library: model.Library{BackendID: nil},
	}
	if !h.CanWriteInFile() {
		t.Error("local backend should allow in-file write")
	}
}

func TestLibraryHandle_CanWriteInFile_S3Backend(t *testing.T) {
	bid := "backend-1"
	h := &service.LibraryHandle{
		Library: model.Library{BackendID: &bid},
	}
	if h.CanWriteInFile() {
		t.Error("S3-backed library must NOT allow in-file write in Phase 1")
	}
}
