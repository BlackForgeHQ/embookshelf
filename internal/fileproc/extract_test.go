// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"context"
	"testing"

	"github.com/blackforge/embookshelf/internal/sidecar"
)

func TestExtractBook_NilSourceReturnsError(t *testing.T) {
	if _, err := ExtractBook(context.Background(), nil, nil, "EPUB", "x.epub"); err == nil {
		t.Fatal("expected error on nil source")
	}
}

// The slug entry point replaces a fake-filename round trip: the caller
// hands over a books.format value and gets the Processor, with no
// synthesized path in between.
func TestDispatchFormat(t *testing.T) {
	cases := []struct {
		format  string
		wantErr bool
	}{
		{format: "EPUB"},
		{format: "epub"},
		{format: " PDF "},
		{format: "CBZ"},
		{format: "MP3"},
		{format: "M4B"},
		{format: "AZW3", wantErr: true},
		{format: "MOBI", wantErr: true},
		{format: "FB2", wantErr: true},
		{format: "", wantErr: true},
		{format: "unknown", wantErr: true},
	}
	for _, c := range cases {
		proc, err := DispatchFormat(c.format)
		if c.wantErr {
			if err == nil {
				t.Errorf("DispatchFormat(%q) = %T, want an error", c.format, proc)
			}
			continue
		}
		if err != nil {
			t.Errorf("DispatchFormat(%q): %v", c.format, err)
			continue
		}
		if proc == nil {
			t.Errorf("DispatchFormat(%q) returned a nil processor", c.format)
		}
	}
}

// The key is what the bytes actually are; the slug is what a row claims
// they are. When they disagree and the key carries a known extension, the
// key wins.
func TestDispatchKeyOrFormat(t *testing.T) {
	cases := []struct {
		name, key, format, wantFormat string
		wantErr                       bool
	}{
		{name: "key wins", key: "books/h.epub", format: "PDF", wantFormat: "EPUB"},
		{name: "key alone", key: "books/h.pdf", wantFormat: "PDF"},
		{name: "slug alone", format: "EPUB", wantFormat: "EPUB"},
		{name: "slug lowercased", format: "mp3", wantFormat: "MP3"},
		{name: "unknown key falls back to slug", key: "books/h.bin", format: "EPUB", wantFormat: "EPUB"},
		{name: "neither", wantErr: true},
		{name: "unsupported slug", format: "AZW3", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			proc, format, err := dispatchKeyOrFormat(c.key, c.format)
			if c.wantErr {
				if err == nil {
					t.Fatalf("want an error, got %T / %q", proc, format)
				}
				return
			}
			if err != nil {
				t.Fatalf("dispatchKeyOrFormat(%q,%q): %v", c.key, c.format, err)
			}
			if format != c.wantFormat {
				t.Errorf("format = %q, want %q", format, c.wantFormat)
			}
		})
	}
}

func TestLayerSidecar_Overlays(t *testing.T) {
	m := Metadata{Title: "Original", Author: "Original Author"}
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
	m := Metadata{Title: "Original", ISBN: "0000000000"}
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
	m := Metadata{ISBN: "978-0-7432-7356-5"}
	out := layerSidecar(m, sidecar.Sidecar{})
	if out.ISBN != "978-0-7432-7356-5" {
		t.Errorf("ISBN=%q want preserved", out.ISBN)
	}
}

func TestLayerSidecar_EmptySidecarLeavesFieldsAlone(t *testing.T) {
	m := Metadata{Title: "Keep", Language: "en"}
	out := layerSidecar(m, sidecar.Sidecar{})
	if out.Title != "Keep" {
		t.Errorf("Title=%q want Keep", out.Title)
	}
	if out.Language != "en" {
		t.Errorf("Language=%q want en", out.Language)
	}
}
