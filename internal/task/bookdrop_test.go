package task

import (
	"testing"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/sidecar"
)

func TestLayerSidecar_NonEmptyOverlays(t *testing.T) {
	m := fileproc.Metadata{Title: "Original", Author: "A"}
	s := sidecar.Sidecar{Title: "Override", Description: "hello"}
	out := layerSidecar(m, s)
	if out.Title != "Override" {
		t.Errorf("title not overridden, got %q", out.Title)
	}
	if out.Author != "A" {
		t.Errorf("author should be unchanged, got %q", out.Author)
	}
	if out.Description != "hello" {
		t.Errorf("description not overlaid, got %q", out.Description)
	}
}

func TestLayerSidecar_EmptySidecarNoChange(t *testing.T) {
	m := fileproc.Metadata{Title: "Keep", Author: "Keep", Description: "Keep", Language: "en"}
	s := sidecar.Sidecar{} // empty
	out := layerSidecar(m, s)
	if out.Title != "Keep" {
		t.Errorf("Title should be unchanged: got %q", out.Title)
	}
	if out.Author != "Keep" {
		t.Errorf("Author should be unchanged: got %q", out.Author)
	}
	if out.Description != "Keep" {
		t.Errorf("Description should be unchanged: got %q", out.Description)
	}
	if out.Language != "en" {
		t.Errorf("Language should be unchanged: got %q", out.Language)
	}
}

func TestLayerSidecar_LanguageOverlay(t *testing.T) {
	m := fileproc.Metadata{Title: "Book", Language: "en"}
	s := sidecar.Sidecar{Language: "fr"}
	out := layerSidecar(m, s)
	if out.Language != "fr" {
		t.Errorf("Language: got %q want fr", out.Language)
	}
}

func TestLayerSidecar_CoverBytesNotOverwritten(t *testing.T) {
	cover := []byte{0xFF, 0xD8, 0xFF} // fake JPEG header
	m := fileproc.Metadata{
		Title:      "Book",
		HasCover:   true,
		CoverBytes: cover,
		CoverMime:  "image/jpeg",
	}
	s := sidecar.Sidecar{Title: "Override"}
	out := layerSidecar(m, s)
	if out.Title != "Override" {
		t.Errorf("Title: got %q want Override", out.Title)
	}
	if !out.HasCover {
		t.Error("HasCover should be preserved")
	}
	if string(out.CoverBytes) != string(cover) {
		t.Error("CoverBytes should not be overwritten by sidecar")
	}
}
