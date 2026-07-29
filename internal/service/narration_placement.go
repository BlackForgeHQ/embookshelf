// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/blackforge/embookshelf/internal/layout"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
)

// LocalPath resolves a library-relative location to an absolute path on
// a local library. Empty for backend-backed libraries, which have no
// filesystem to resolve against.
func (h *LibraryHandle) LocalPath(location string) string {
	root := h.localRoot()
	if root == "" || h.IsObjectStore() {
		return ""
	}
	return filepath.Join(root, filepath.FromSlash(location))
}

// BookFile returns one of a book's files rows by id. Used by callers
// that hold a pointer to a specific rendition rather than a format.
func (h *LibraryHandle) BookFile(ctx context.Context, bookID, fileID string) (model.File, bool) {
	if h.files == nil || fileID == "" {
		return model.File{}, false
	}
	list, err := h.files.ListByBook(ctx, bookID)
	if err != nil {
		return model.File{}, false
	}
	for _, f := range list {
		if f.ID == fileID {
			return f, true
		}
	}
	return model.File{}, false
}

// PrimaryContentHash is the hash of the book's own file — the one whose
// format matches books.format, i.e. the thing a narration is made from
// rather than the narration itself.
func (h *LibraryHandle) PrimaryContentHash(ctx context.Context, book model.Book) []byte {
	if h.files == nil {
		return nil
	}
	f, err := primaryFile(ctx, h.files, book)
	if err != nil {
		return nil
	}
	return f.ContentHash
}

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
// Deliberately not service.Placer. That seam exists for BookDrop approve,
// where a *new* book needs a *new* folder, so it runs the source through
// uniqueDirectory / uniqueBackendFolder and keeps the source's basename.
// Pointed at a book that already exists, both behaviours are wrong: the
// collision suffix drops the narration into a sibling "Title (2)" folder
// — which Library scan would later read as a second book, the exact
// outcome ADR-0025 exists to prevent — and the basename would be the
// temp file's, embookshelf-audiobook-1234567.mp3.
//
// Overwrite rather than suffix is the other half of that: regeneration is
// destructive by design (ADR-0025 §4), so a second run must land on the
// same key rather than accumulate half-gigabyte renditions.
func (h *LibraryHandle) PlaceNarration(ctx context.Context, book model.Book, srcPath string) (PlaceResult, error) {
	key := h.NarrationKey(book)

	info, err := os.Stat(srcPath)
	if err != nil {
		return PlaceResult{}, fmt.Errorf("stat narration: %w", err)
	}

	if h.IsObjectStore() {
		if h.Storage == nil {
			return PlaceResult{}, errors.New("library has no storage backend")
		}
		f, oerr := os.Open(srcPath)
		if oerr != nil {
			return PlaceResult{}, fmt.Errorf("open narration: %w", oerr)
		}
		defer func() { _ = f.Close() }()

		opts := []storage.PutOption{storage.WithContentType("audio/mpeg")}
		if _, perr := h.Storage.Put(ctx, key, f, opts...); perr != nil {
			return PlaceResult{}, fmt.Errorf("upload narration: %w", perr)
		}
		// The bytes are durable in the backend; the temp file is not
		// worth keeping and would otherwise fill the data volume one
		// half-gigabyte narration at a time.
		_ = f.Close()
		if rerr := os.Remove(srcPath); rerr != nil {
			slog.Warn("narration: remove local after upload", "path", srcPath, "err", rerr)
		}
		return PlaceResult{
			Location:   key,
			FolderPath: path.Dir(key),
			Size:       info.Size(),
			Mtime:      info.ModTime(),
		}, nil
	}

	root := h.localRoot()
	if root == "" {
		return PlaceResult{}, errors.New("library has no local root")
	}
	dest := filepath.Join(root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return PlaceResult{}, fmt.Errorf("mkdir narration folder: %w", err)
	}
	if err := moveFile(srcPath, dest); err != nil {
		return PlaceResult{}, fmt.Errorf("move narration: %w", err)
	}
	return PlaceResult{
		Location:   key,
		FolderPath: path.Dir(key),
		Size:       info.Size(),
		Mtime:      info.ModTime(),
	}, nil
}

// localRoot is the library's on-disk root, preferring the storage-v2
// Root column and falling back to the legacy Path.
func (h *LibraryHandle) localRoot() string {
	if h.Library.Root != nil && *h.Library.Root != "" {
		return *h.Library.Root
	}
	return h.Library.Path
}
