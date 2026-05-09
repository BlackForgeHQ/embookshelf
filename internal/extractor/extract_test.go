// SPDX-License-Identifier: AGPL-3.0-or-later

package extractor

import (
	"context"
	"testing"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/sidecar"
)

func TestExtract_NilSourceReturnsError(t *testing.T) {
	if _, err := Extract(context.Background(), nil, nil, "EPUB", "x.epub"); err == nil {
		t.Fatal("expected error on nil source")
	}
}

func TestFormatToPath(t *testing.T) {
	cases := []struct {
		format, key, want string
	}{
		{"EPUB", "books/h.epub", "books/h.epub"},
		{"EPUB", "", "x.epub"},
		{"PDF", "", "x.pdf"},
		{"MP3", "", "x.mp3"},
		{"M4B", "", "x.m4b"},
		{"AZW3", "", "x.azw3"},
		{"unknown", "", ""},
		{"", "books/h.epub", "books/h.epub"},
	}
	for _, c := range cases {
		if got := formatToPath(c.format, c.key); got != c.want {
			t.Errorf("formatToPath(%q,%q)=%q want %q", c.format, c.key, got, c.want)
		}
	}
}

func TestLayerSidecar_Overlays(t *testing.T) {
	m := fileproc.Metadata{Title: "Original", Author: "Original Author"}
	s := sidecar.Sidecar{Title: "Override", Description: "Synopsis"}
	out := layerSidecar(m, s)
	if out.Title != "Override" {
		t.Errorf("Title=%q want Override", out.Title)
	}
	if out.Author != "Original Author" {
		t.Errorf("Author=%q want preserved", out.Author)
	}
	if out.Description != "Synopsis" {
		t.Errorf("Description=%q want Synopsis", out.Description)
	}
}

func TestLayerSidecar_ISBNOverlay(t *testing.T) {
	m := fileproc.Metadata{Title: "Original", ISBN: "0000000000"}
	s := sidecar.Sidecar{ISBN: "978-0-441-17271-9"}
	out := layerSidecar(m, s)
	if out.ISBN != "978-0-441-17271-9" {
		t.Errorf("ISBN=%q want 978-0-441-17271-9", out.ISBN)
	}
	if out.Title != "Original" {
		t.Errorf("Title=%q want preserved", out.Title)
	}
}

func TestLayerSidecar_EmptyISBNDoesNotOverwrite(t *testing.T) {
	m := fileproc.Metadata{ISBN: "978-0-7432-7356-5"}
	out := layerSidecar(m, sidecar.Sidecar{})
	if out.ISBN != "978-0-7432-7356-5" {
		t.Errorf("ISBN=%q want preserved", out.ISBN)
	}
}

func TestLayerSidecar_EmptySidecarLeavesFieldsAlone(t *testing.T) {
	m := fileproc.Metadata{Title: "Keep", Language: "en"}
	out := layerSidecar(m, sidecar.Sidecar{})
	if out.Title != "Keep" {
		t.Errorf("Title=%q want Keep", out.Title)
	}
	if out.Language != "en" {
		t.Errorf("Language=%q want en", out.Language)
	}
}

func TestIsAudioFormat(t *testing.T) {
	cases := map[string]bool{
		"MP3": true, "M4B": true,
		"EPUB": false, "PDF": false, "CBZ": false, "": false,
	}
	for f, want := range cases {
		if got := isAudioFormat(f); got != want {
			t.Errorf("isAudioFormat(%q)=%v want %v", f, got, want)
		}
	}
}
