// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"errors"
	"fmt"
	"io"
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

// ComicPagesIndex returns the page count for a comic. The reader uses
// this to size its navigation UI before requesting individual pages.
//
// Response: {"count": 142}
func (h *Handler) ComicPagesIndex(c *gin.Context, s bookScope) {
	book := s.Book
	if !comicFormat(book.Format) {
		writeError(c, http.StatusUnsupportedMediaType, "not a comic book")
		return
	}
	set, err := h.openComicPages(c, book)
	if err != nil {
		writeComicError(c, "list comic pages", err)
		return
	}
	defer func() { _ = set.Close() }()

	c.Header("Cache-Control", "private, max-age=3600")
	c.JSON(http.StatusOK, gin.H{"count": set.Len()})
}

// ComicPage streams a single page (image bytes) from a comic archive.
// Pages are 0-indexed in natural sort order (page2.jpg before page10.jpg).
func (h *Handler) ComicPage(c *gin.Context, s bookScope) {
	nStr := strings.TrimSpace(c.Param("n"))
	n, err := strconv.Atoi(nStr)
	if err != nil || n < 0 {
		writeError(c, http.StatusBadRequest, "invalid page number")
		return
	}
	book := s.Book
	if !comicFormat(book.Format) {
		writeError(c, http.StatusUnsupportedMediaType, "not a comic book")
		return
	}
	set, err := h.openComicPages(c, book)
	if err != nil {
		writeComicError(c, "open comic archive", err)
		return
	}
	defer func() { _ = set.Close() }()

	rc, mime, err := set.Page(n)
	if err != nil {
		if errors.Is(err, fileproc.ErrComicPageNotImage) {
			// The archive opened and named this entry a page, but its
			// own bytes disagree — a different sentence from "missing"
			// and routed through the same mapping every other
			// page-set-level failure uses.
			writeComicError(c, "serve comic page", err)
			return
		}
		// The archive opened and this page did not: out of range, or an
		// entry the extraction could not decode. Either way it is a
		// missing page, not a broken comic.
		writeError(c, http.StatusNotFound, err.Error())
		return
	}
	defer func() { _ = rc.Close() }()

	// Long cache: page bytes within an archive are immutable for the
	// life of the underlying file. ETag would be more correct but the
	// content rarely changes — a 1-day private cache is plenty.
	c.Header("Cache-Control", "private, max-age=86400, immutable")
	// Every container types its page from the page's own leading bytes
	// now (#331): the extracted arms always did, and Page's ZIP arm
	// sniffs its window before returning, so mime is never a guess from
	// the entry's name here.
	c.Header("Content-Type", mime)

	if _, cerr := io.Copy(c.Writer, rc); cerr != nil {
		// Headers, and possibly bytes, are already on the wire. This is
		// a best effort at saying so rather than truncating in silence.
		writeServerError(c, "stream comic page", cerr)
		return
	}
}

// comicFormat is the gate's rule stated as the table's, not as a
// literal: any format the table maps to the comic reader pages here.
// The literal `!= "CBZ"` it replaces silently 415'd rows an older
// release stamped CBR — a format the table names and the download path
// already handles (#336).
func comicFormat(format string) bool {
	return model.ReaderForFormat(format) == model.ReaderComic
}

// openComicPages resolves a book to its pages, through the page cache.
func (h *Handler) openComicPages(c *gin.Context, book model.Book) (*fileproc.ComicPageSet, error) {
	ref, err := h.comicArchive(c, book)
	if err != nil {
		return nil, err
	}
	return fileproc.OpenComicPages(c.Request.Context(), h.comics, ref.key, ref.open)
}

// comicArchiveRef is a comic's bytes: a stable identity for them, and a way
// to get at them that is only used if the identity is not already known
// to the page cache.
//
// The two halves answer different questions and it matters that they are
// separate. The key says *which bytes*, so it can be produced from the
// catalog alone — a warm comic then serves page 400 without a single
// read of the archive, which on an object-store library is a network
// round trip saved per page. The opener says *where the bytes are*, and
// is called only on a miss.
type comicArchiveRef struct {
	key  string
	open fileproc.SourceOpener
}

// comicArchive resolves a book to its archive reference.
//
// It goes through LibraryHandle — the byte-access seam every other
// in-process reader uses — rather than opening books.path itself. That is
// not a style preference: the two previous callers that reached around
// this seam with a direct file open are the two known object-store
// outages, device push (CONTEXT, "OpenBook") and the library scan. Comic
// pagination was the third, and it never worked on an object-store
// library at all (#240).
//
// BookSource is deliberately not what is called here. It answers "what do
// I tell the browser" — a presigned URL is a fine answer to that and a
// useless one to a reader that has to seek into an archive.
//
// LocateBook gives both halves of the reference from one files lookup,
// and owns every rule this handler used to restate: the Book file sandbox
// for a legacy path, "not placed" kept apart from a failed lookup, and
// the Bytes identity the page cache keys on.
func (h *Handler) comicArchive(c *gin.Context, book model.Book) (comicArchiveRef, error) {
	ctx := c.Request.Context()
	if h.libStore == nil {
		return comicArchiveRef{}, errors.New("comic reader: no library store")
	}
	handle, err := h.libStore.For(ctx, book.LibraryID)
	if err != nil {
		// A resolve that broke is a failure, not a reason to read this
		// machine's disk for a library whose bytes may not be on it.
		return comicArchiveRef{}, fmt.Errorf("resolve library: %w", err)
	}
	located, err := handle.LocateBook(ctx, book)
	if err != nil {
		return comicArchiveRef{}, err
	}
	return comicArchiveRef{key: located.Identity, open: located.Open}, nil
}

// writeComicError renders a failure to get at the comic's pages. "The
// book has no file", "the backend is unreachable" and "this file is not
// a comic archive at all" are different answers and the reader UI treats
// them differently.
func writeComicError(c *gin.Context, op string, err error) {
	switch {
	case errors.Is(err, fileproc.ErrComicContainer):
		// The book is on the shelf as a comic (every comic extension
		// stamps CBZ) but its bytes are none of the containers the
		// reader pages. A fact about the file, and one the UI can say.
		writeError(c, http.StatusUnsupportedMediaType, err.Error())
	case errors.Is(err, fileproc.ErrComicPageNotImage):
		// The archive is a comic the reader pages, and this entry is
		// one of its pages by name, but its own bytes are not a
		// recognized image (#331). Same shape as ErrComicContainer one
		// level up — a declared type the bytes do not back up — so it
		// gets the same status rather than the generic 500 a decoder
		// failure would.
		writeError(c, http.StatusUnsupportedMediaType, err.Error())
	case errors.Is(err, fileproc.ErrPageCacheFull):
		// Nothing is wrong with the book or the server: every slot in
		// the page cache is being read by somebody else right now. A
		// retry, and the status that says so — plus how long to wait,
		// since the reader has no other way to guess and hammering the
		// endpoint is what keeps the cache full.
		c.Header("Retry-After", "5")
		writeError(c, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, service.ErrPathOutsideRoots):
		writeError(c, http.StatusForbidden, err.Error())
	case errors.Is(err, storage.ErrNotFound), errors.Is(err, os.ErrNotExist):
		writeError(c, http.StatusNotFound, "no file stored for this book")
	default:
		writeServerError(c, op, err)
	}
}
