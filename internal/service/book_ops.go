// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/layout"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
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
	files DerivedFiles
	hash  func(context.Context, model.Book) []byte
}

// DerivedFiles is the files-table slice RecordDerived needs to land a
// derived artifact's row, reusing the row a previous generation left at
// the same location.
type DerivedFiles interface {
	GetByLocation(ctx context.Context, libraryID, location string) (model.File, error)
	SetContentHash(ctx context.Context, fileID string, hash []byte, size int64, mtime time.Time) error
	Insert(ctx context.Context, f model.File) (model.File, error)
}

func NewBookOps(store LibraryStore, files DerivedFiles) *BookOps {
	return &BookOps{store: store, files: files, hash: NewPrimaryHash(store)}
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

// DerivedRecord is what recording a derived artifact established: where
// the bytes landed and, for kinds the catalog tracks, the files row
// holding them. FileID is empty for markdown — machine feed, no files
// row (ADR-0033 §4).
type DerivedRecord struct {
	FileID   string
	Hash     []byte
	Location string
	Size     int64
	Mtime    time.Time
}

// RecordDerived is the finalize tail every derived-artifact job shares:
// hash the staged bytes, place them in the book's own folder, and land
// the files row — updating the row a previous generation left at the
// same location rather than violating UNIQUE(library_id, location) on
// regeneration (#307).
//
// The order is the module's to own, not the callers': the hash comes
// first because placement consumes the staged file, and content_hash is
// the identity the library scan's rename safety net keys on. A lookup
// failure that is not "no row" is an error — falling through to Insert
// is exactly the constraint violation the lookup exists to prevent.
func (o *BookOps) RecordDerived(
	ctx context.Context, book model.Book, srcPath string, kind DerivedKind,
) (DerivedRecord, error) {
	handle, err := o.store.For(ctx, book.LibraryID)
	if err != nil {
		return DerivedRecord{}, fmt.Errorf("resolve library: %w", err)
	}

	var hash []byte
	if kind != DerivedMarkdown {
		if hash, err = hashStaged(srcPath); err != nil {
			return DerivedRecord{}, fmt.Errorf("hash %s: %w", kind, err)
		}
	}

	placed, err := handle.PlaceDerived(ctx, book, srcPath, kind)
	if err != nil {
		return DerivedRecord{}, fmt.Errorf("place %s: %w", kind, err)
	}
	rec := DerivedRecord{
		Hash:     hash,
		Location: placed.Location,
		Size:     placed.Size,
		Mtime:    placed.Mtime,
	}
	if kind == DerivedMarkdown {
		return rec, nil
	}

	existing, err := o.files.GetByLocation(ctx, book.LibraryID, placed.Location)
	switch {
	case err == nil:
		if err := o.files.SetContentHash(ctx, existing.ID, hash, placed.Size, placed.Mtime); err != nil {
			return DerivedRecord{}, fmt.Errorf("refresh files row %s: %w", existing.ID, err)
		}
		rec.FileID = existing.ID
	case errors.Is(err, repo.ErrNotFound):
		inserted, err := o.files.Insert(ctx, model.File{
			LibraryID:   book.LibraryID,
			BookID:      book.ID,
			Location:    placed.Location,
			Size:        placed.Size,
			Mtime:       placed.Mtime,
			ContentHash: hash,
			Format:      derivedSpecs[kind].format,
		})
		if err != nil {
			return DerivedRecord{}, fmt.Errorf("insert files row at %s: %w", placed.Location, err)
		}
		rec.FileID = inserted.ID
	default:
		return DerivedRecord{}, fmt.Errorf("look up files row at %s: %w", placed.Location, err)
	}
	return rec, nil
}

// Recorder binds RecordDerived to one kind, in the shape worker deps
// take — the record-side twin of Placer.
func (o *BookOps) Recorder(kind DerivedKind) func(context.Context, model.Book, string) (DerivedRecord, error) {
	return func(ctx context.Context, book model.Book, srcPath string) (DerivedRecord, error) {
		return o.RecordDerived(ctx, book, srcPath, kind)
	}
}

// hashStaged streams a staged artifact through sha256.
func hashStaged(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}
