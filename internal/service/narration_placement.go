// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"path"
	"strings"

	"github.com/blackforge/embookshelf/internal/layout"
	"github.com/blackforge/embookshelf/internal/model"
)

// NarrationKey is where a book's generated audio belongs: inside the
// book's own folder, named after the book.
//
// Derived from books.folder_path when it is set, because that is the
// authoritative record of where this book actually lives — a book placed
// before the folder-per-book layout, or one whose folder took a
// collision suffix at approve time, is not at {Author}/{Title} and
// guessing would put its narration somewhere else entirely.
func (h *LibraryHandle) NarrationKey(book model.Book) string {
	folder := ""
	if book.FolderPath != nil {
		folder = strings.Trim(*book.FolderPath, "/")
	}
	if folder == "" {
		folder = path.Join(layout.SanitizeAuthor(book.Author), layout.SanitizeTitle(book.Title))
	}
	return path.Join(folder, layout.SanitizeTitle(book.Title)+".mp3")
}

// PlaceNarration moves generated audio from a local temp file into the
// book's own folder, overwriting any previous rendition.
//
// All the policy is in the key: a narration is another file of the book
// that already exists (ADR-0025), so it goes exactly where that book
// lives and nowhere else. Writing it there is PlaceAt's job, and PlaceAt
// is where the argument for why this is not Placer.Place is written down.
func (h *LibraryHandle) PlaceNarration(ctx context.Context, book model.Book, srcPath string) (PlaceResult, error) {
	return h.PlaceAt(ctx, h.NarrationKey(book), srcPath, "MP3")
}
