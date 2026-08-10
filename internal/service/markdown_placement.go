// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"path"
	"strings"

	"github.com/blackforge/embookshelf/internal/layout"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
)

// MarkdownKey is where a book's Markdown rendition belongs: inside the
// book's own folder, named after the book. Same derivation as
// NarrationKey, for the same reason — books.folder_path is the
// authoritative record of where the book actually lives.
//
// The rendition deliberately gets no files row (ADR-0033 §4): it is
// machine feed, not a library artifact. Scan's relocate pass hashes the
// stray file, finds no row, and no-ops (ADR-0018), so the catalog never
// sees it; book_markdown_renditions owns its whole lifecycle.
func (h *LibraryHandle) MarkdownKey(book model.Book) string {
	folder := ""
	if book.FolderPath != nil {
		folder = strings.Trim(*book.FolderPath, "/")
	}
	if folder == "" {
		folder = path.Join(layout.SanitizeAuthor(book.Author), layout.SanitizeTitle(book.Title))
	}
	return path.Join(folder, layout.SanitizeTitle(book.Title)+".md")
}

// OpenMarkdown opens a Markdown rendition by its tracking-row location.
//
// Through StorageKey, not Storage.Open directly: the row stores the
// library-relative location PlaceAt returned, and the local backend is
// rooted at "/" (ADR-0030), so the bare location would be read relative
// to nowhere and miss. Backend-backed libraries pass through unchanged —
// their keys are already object keys. Mirrors what OpenBook does for
// files rows.
func (h *LibraryHandle) OpenMarkdown(ctx context.Context, location string) (storage.Source, error) {
	if h.Storage == nil {
		return nil, errors.New("library handle: no storage")
	}
	return h.Storage.Open(ctx, h.StorageKey(location))
}

// PlaceMarkdown moves converted markdown from a local temp file into the
// book's own folder, overwriting any previous rendition. All the policy
// is in the key — see PlaceNarration for why this is PlaceAt and not
// Placer.Place. The "MD" format has no MIME in the format table, so the
// object is stored without a declared content type — nothing serves it
// to a browser.
func (h *LibraryHandle) PlaceMarkdown(ctx context.Context, book model.Book, srcPath string) (PlaceResult, error) {
	return h.PlaceAt(ctx, h.MarkdownKey(book), srcPath, "MD")
}

// EpubKey is where a book's generated EPUB belongs: the book's own
// folder, named after the book — the NarrationKey derivation with the
// .epub extension (ADR-0034 §1: another file of the same book).
func (h *LibraryHandle) EpubKey(book model.Book) string {
	folder := ""
	if book.FolderPath != nil {
		folder = strings.Trim(*book.FolderPath, "/")
	}
	if folder == "" {
		folder = path.Join(layout.SanitizeAuthor(book.Author), layout.SanitizeTitle(book.Title))
	}
	return path.Join(folder, layout.SanitizeTitle(book.Title)+".epub")
}

// PlaceEPUB moves a rendered EPUB from a local temp file into the
// book's own folder, overwriting any previous generation.
func (h *LibraryHandle) PlaceEPUB(ctx context.Context, book model.Book, srcPath string) (PlaceResult, error) {
	return h.PlaceAt(ctx, h.EpubKey(book), srcPath, "EPUB")
}
