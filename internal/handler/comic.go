// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
)

// ComicPagesIndex returns the page count for a CBZ book. The reader uses
// this to size its navigation UI before requesting individual pages.
//
// Response: {"count": 142}
func (h *Handler) ComicPagesIndex(c *gin.Context, s bookScope) {
	book := s.Book
	if book.Format != "CBZ" {
		writeError(c, http.StatusUnsupportedMediaType, "not a comic book")
		return
	}
	src, err := h.openComicSource(c, book)
	if err != nil {
		writeComicSourceError(c, err)
		return
	}
	defer func() { _ = src.Close() }()

	pages, err := fileproc.CBZPages(src)
	if err != nil {
		writeServerError(c, "list comic pages", err)
		return
	}
	c.Header("Cache-Control", "private, max-age=3600")
	c.JSON(http.StatusOK, gin.H{"count": len(pages)})
}

// ComicPage streams a single page (image bytes) from a CBZ archive.
// Pages are 0-indexed in natural sort order (page2.jpg before page10.jpg).
func (h *Handler) ComicPage(c *gin.Context, s bookScope) {
	nStr := strings.TrimSpace(c.Param("n"))
	n, err := strconv.Atoi(nStr)
	if err != nil || n < 0 {
		writeError(c, http.StatusBadRequest, "invalid page number")
		return
	}
	book := s.Book
	if book.Format != "CBZ" {
		writeError(c, http.StatusUnsupportedMediaType, "not a comic book")
		return
	}
	src, err := h.openComicSource(c, book)
	if err != nil {
		writeComicSourceError(c, err)
		return
	}
	defer func() { _ = src.Close() }()

	// Long cache: page bytes within an archive are immutable for the
	// life of the underlying file. ETag would be more correct but the
	// content rarely changes — a 1-day private cache is plenty.
	c.Header("Cache-Control", "private, max-age=86400, immutable")

	// We can't know the MIME type without reading the archive's directory.
	// CBZPage does that and writes the entry straight into the response
	// body — one entry, not the whole archive, so an object-store-backed
	// comic costs a range read per page rather than a download.
	mime, err := fileproc.CBZPage(src, n, c.Writer)
	if err != nil {
		// Headers may already be on the wire if the archive opened but
		// the entry was bad mid-stream. We do our best to surface a
		// clean error before any body bytes were written.
		if c.Writer.Written() {
			writeServerError(c, "stream comic page", err)
			return
		}
		writeError(c, http.StatusNotFound, err.Error())
		return
	}
	if !c.Writer.Written() {
		// Belt-and-suspenders: if Copy wrote zero bytes (empty entry),
		// still set the type so the browser knows what it got.
		c.Header("Content-Type", mime)
	}
}

// openComicSource hands back the comic's bytes as a random-access Source,
// wherever the book's library keeps them.
//
// It goes through LibraryHandle.OpenBookSource — the byte-access seam
// every other in-process reader uses — rather than opening books.path
// itself. That is not a style preference: the two previous callers that
// reached around this seam with a direct file open are the two known
// object-store outages, device push (CONTEXT, "OpenBook") and the library
// scan. Comic pagination was the third, and it never worked on an
// object-store library at all (#240).
//
// BookSource is deliberately not what is called here. It answers "what do
// I tell the browser" — a presigned URL is a fine answer to that and a
// useless one to a reader that has to seek into a zip.
//
// The fallback is the one handler.Options documents: an install with no
// LibraryStore wired has no seam to route through, so the bytes come off
// disk through the shared sandbox gate, exactly as serveBookFile
// degrades. It is the same rule, not a second copy of it — the hand-rolled
// third copy this handler used to carry is gone.
func (h *Handler) openComicSource(c *gin.Context, book model.Book) (storage.Source, error) {
	ctx := c.Request.Context()
	if h.libStore != nil {
		handle, err := h.libStore.For(ctx, book.LibraryID)
		if err != nil {
			// Not the documented fallback. That one is "no LibraryStore
			// wired", which is the nil check above; a resolve that broke
			// is a failure, and falling through would read this machine's
			// disk for a library whose bytes may not be on it.
			return nil, fmt.Errorf("resolve library: %w", err)
		}
		src, oerr := handle.OpenBookSource(ctx, book)
		if oerr != nil {
			return nil, notePlacedFile(ctx, handle, book, oerr)
		}
		return src, nil
	}
	return h.openSandboxedSource(c, book.Path)
}

// notePlacedFile separates "this book was never placed" from "its bytes
// would not open", which the seam reports as one error and which are
// different answers to a reader: 404 for a row with no file, 500 for a
// backend that would not answer.
//
// Asked only when an open has already failed, so the extra lookup is off
// the success path entirely.
func notePlacedFile(
	ctx context.Context, handle *service.LibraryHandle, book model.Book, err error,
) error {
	if book.Path != "" {
		return err
	}
	if keys, kerr := handle.BookFileLocations(ctx, book.ID); kerr == nil && len(keys) == 0 {
		return fmt.Errorf("book has no stored file: %w", os.ErrNotExist)
	}
	return err
}

// openSandboxedSource opens a book file named by a books.path value,
// through the sandbox every handler-side filesystem read passes.
func (h *Handler) openSandboxedSource(c *gin.Context, path string) (storage.Source, error) {
	if path == "" {
		return nil, fmt.Errorf("book has no stored file: %w", os.ErrNotExist)
	}
	abs, err := h.sandboxedBookPath(c, path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return fileSource{File: f, size: info.Size()}, nil
}

// fileSource is an open file as a storage.Source. *os.File is already a
// ReaderAt and a Closer; only the size is missing.
type fileSource struct {
	*os.File
	size int64
}

func (f fileSource) Size() int64 { return f.size }

// writeComicSourceError renders a failure to get at the archive's bytes.
// "The book has no file" and "the backend is unreachable" are different
// answers and the reader UI treats them differently.
func writeComicSourceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrPathOutsideRoots):
		writeError(c, http.StatusForbidden, err.Error())
	case errors.Is(err, storage.ErrNotFound), errors.Is(err, os.ErrNotExist):
		writeError(c, http.StatusNotFound, "no file stored for this book")
	default:
		writeServerError(c, "open comic archive", err)
	}
}
