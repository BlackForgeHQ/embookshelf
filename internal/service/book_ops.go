// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/blackforge/embookshelf/internal/layout"
	"github.com/blackforge/embookshelf/internal/model"
)

// DerivedKind names a derived artifact a book can grow: the machine or
// user deliverables generated *from* the book, placed beside it in its
// own folder. The kind selects the extension and the stored format —
// the only two facts the three placements ever differed by (#299).
type DerivedKind string

const (
	DerivedMarkdown  DerivedKind = "markdown"
	DerivedEPUB      DerivedKind = "epub"
	DerivedNarration DerivedKind = "narration"
)

// derivedSpecs is the one statement of what differs per kind.
var derivedSpecs = map[DerivedKind]struct {
	ext    string
	format string
}{
	DerivedMarkdown:  {ext: ".md", format: "MD"},
	DerivedEPUB:      {ext: ".epub", format: "EPUB"},
	DerivedNarration: {ext: ".mp3", format: "MP3"},
}

// DerivedKey is where a book's derived artifact belongs: inside the
// book's own folder, named after the book, extension per kind.
//
// Derived from books.folder_path when it is set, because that is the
// authoritative record of where this book actually lives — a book placed
// before the folder-per-book layout, or one whose folder took a
// collision suffix at approve time, is not at {Author}/{Title} and
// guessing would put its artifact somewhere else entirely.
//
// The markdown rendition deliberately gets no files row (ADR-0033 §4):
// it is machine feed, not a library artifact. Scan's relocate pass
// hashes the stray file, finds no row, and no-ops (ADR-0018), so the
// catalog never sees it; book_markdown_renditions owns its lifecycle.
func (h *LibraryHandle) DerivedKey(book model.Book, kind DerivedKind) string {
	folder := ""
	if book.FolderPath != nil {
		folder = strings.Trim(*book.FolderPath, "/")
	}
	if folder == "" {
		folder = path.Join(layout.SanitizeAuthor(book.Author), layout.SanitizeTitle(book.Title))
	}
	return path.Join(folder, layout.SanitizeTitle(book.Title)+derivedSpecs[kind].ext)
}

// PlaceDerived moves a derived artifact from a local temp file into the
// book's own folder, overwriting any previous generation. The one
// placement entry point across the three kinds.
//
// All the policy is in the key: a derived artifact is another file of
// the book that already exists (ADR-0025), so it goes exactly where
// that book lives and nowhere else. Writing it there is PlaceAt's job,
// and PlaceAt is where the argument for why this is not Placer.Place is
// written down — the book's folder already exists, and Placer would
// answer that with a "Title (2)" sibling scan reads as a second book.
// "MD" has no MIME in the format table, so markdown is stored without a
// declared content type — nothing serves it to a browser.
func (h *LibraryHandle) PlaceDerived(
	ctx context.Context, book model.Book, srcPath string, kind DerivedKind,
) (PlaceResult, error) {
	spec, ok := derivedSpecs[kind]
	if !ok {
		return PlaceResult{}, fmt.Errorf("unknown derived kind %q", kind)
	}
	return h.PlaceAt(ctx, h.DerivedKey(book, kind), srcPath, spec.format)
}

// BookOps is the deep book-operations module over the library store:
// the library-touching steps every derived-artifact job shares — open
// the book's bytes, read its provenance hash, open its markdown
// rendition, place a derived file — each resolving the library itself.
// The job registry consumes these and declares job wiring instead of
// restating library plumbing per job; the resolve-library-fails arm is
// testable here, through a real interface, instead of being buried in
// inline closures (#299).
type BookOps struct {
	store LibraryStore
	hash  func(context.Context, model.Book) []byte
}

func NewBookOps(store LibraryStore) *BookOps {
	return &BookOps{store: store, hash: NewPrimaryHash(store)}
}

// Open yields the book's bytes as a stream — the converter POST body's
// shape.
func (o *BookOps) Open(ctx context.Context, book model.Book) (io.Reader, int64, io.Closer, error) {
	handle, err := o.store.For(ctx, book.LibraryID)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("resolve library: %w", err)
	}
	return handle.OpenBook(ctx, book)
}

// PrimaryHash answers "which bytes is this book, right now" through the
// shared warn-and-degrade seam (#297).
func (o *BookOps) PrimaryHash(ctx context.Context, book model.Book) []byte {
	return o.hash(ctx, book)
}

// OpenMarkdown opens a Markdown rendition by its tracking-row location,
// in the markdown feed's shape.
func (o *BookOps) OpenMarkdown(ctx context.Context, book model.Book, location string) (io.ReadCloser, error) {
	handle, err := o.store.For(ctx, book.LibraryID)
	if err != nil {
		return nil, fmt.Errorf("resolve library: %w", err)
	}
	// OpenMarkdown, not Open: the row stores the library-relative
	// location, and the local backend is "/"-rooted (ADR-0030) — the
	// bare location misses.
	src, err := handle.OpenMarkdown(ctx, location)
	if err != nil {
		return nil, err
	}
	return struct {
		io.Reader
		io.Closer
	}{io.NewSectionReader(src, 0, src.Size()), src}, nil
}

// PlaceDerived places one derived artifact into the book's own folder.
func (o *BookOps) PlaceDerived(
	ctx context.Context, book model.Book, srcPath string, kind DerivedKind,
) (PlaceResult, error) {
	handle, err := o.store.For(ctx, book.LibraryID)
	if err != nil {
		return PlaceResult{}, fmt.Errorf("resolve library: %w", err)
	}
	return handle.PlaceDerived(ctx, book, srcPath, kind)
}

// Placer binds PlaceDerived to one kind, in the shape worker deps take.
func (o *BookOps) Placer(kind DerivedKind) func(context.Context, model.Book, string) (PlaceResult, error) {
	return func(ctx context.Context, book model.Book, srcPath string) (PlaceResult, error) {
		return o.PlaceDerived(ctx, book, srcPath, kind)
	}
}
