// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
)

// mimeForFormat returns the response Content-Type for a given book
// format, or "" when the format has no reader — which every caller here
// turns into a 415.
//
// The table is model.FormatSpecs. This name survives because five call
// sites read better for it, but the fact lives with the format (#194).
func mimeForFormat(format string) string {
	return model.MIMEForFormat(format)
}

// serveBookFile chooses between a presigned redirect, a stream through
// the app and a local serve, by asking the book's library
// (LibraryHandle.BookSource). A library that cannot be resolved is an
// error: falling back to books.path would read this machine's disk for a
// library whose bytes may not be on it.
func (h *Handler) serveBookFile(c *gin.Context, book model.Book, mime string) error {
	if h.libStore == nil {
		return errors.New("file serve: no library store")
	}
	handle, err := h.libStore.For(c.Request.Context(), book.LibraryID)
	if err != nil {
		return fmt.Errorf("resolve library: %w", err)
	}
	src, err := handle.BookSource(c.Request.Context(), book)
	if err != nil {
		return err
	}
	return h.serveBookSource(c, src, mime)
}

// serveBookSource carries out a delivery decision. The switch is here
// once, for every rendition of a book: the primary file above and the
// generated narration, which used to re-derive the local-versus-object-
// store split for itself and so could not honour presign at all.
//
// Kind is the whole input — a BookSource carries what its own mode needs
// and nothing else, so this cannot consult the library again.
func (h *Handler) serveBookSource(c *gin.Context, src service.BookSource, mime string) error {
	switch src.Kind {
	case service.BookDeliveryPresign:
		c.Redirect(http.StatusFound, src.URL)
		return nil
	case service.BookDeliveryStream:
		return h.serveStreamedBookFile(c, src, mime)
	}
	return h.serveLocalBookFile(c, src.Path, mime)
}

// serveStreamedBookFile pipes object bytes from a backend Storage
// through the app server. Uses Storage.Open when available so
// http.ServeContent can honour Range / If-Modified-Since natively;
// falls back to Storage.Get + io.Copy when the backend can't seek.
func (h *Handler) serveStreamedBookFile(c *gin.Context, src service.BookSource, mime string) error {
	if src.Storage == nil || src.Key == "" {
		return errors.New("stream source: missing storage or key")
	}
	c.Header("Content-Type", mime)
	c.Header("Cache-Control", "private, max-age=3600")

	source, err := src.Storage.Open(c.Request.Context(), src.Key)
	if err == nil {
		defer func() { _ = source.Close() }()
		http.ServeContent(c.Writer, c.Request, "", time.Time{}, &storageSourceSeeker{src: source})
		return nil
	}

	// Open failed (backend without efficient ReaderAt, or transient).
	// Fall back to a sequential stream — Range requests degrade to
	// full-body but reading still works.
	rc, gerr := src.Storage.Get(c.Request.Context(), src.Key)
	if gerr != nil {
		return gerr
	}
	defer func() { _ = rc.Close() }()
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, rc); err != nil {
		return err
	}
	return nil
}

// storageSourceSeeker adapts a storage.Source (ReaderAt + Size) to the
// io.ReadSeeker http.ServeContent expects. Read calls advance an
// internal cursor; Seek resets it.
type storageSourceSeeker struct {
	src storage.Source
	pos int64
}

func (s *storageSourceSeeker) Read(p []byte) (int, error) {
	size := s.src.Size()
	if s.pos >= size {
		return 0, io.EOF
	}
	n, err := s.src.ReadAt(p, s.pos)
	s.pos += int64(n)
	if err == io.EOF && n > 0 {
		err = nil
	}
	return n, err
}

func (s *storageSourceSeeker) Seek(offset int64, whence int) (int64, error) {
	size := s.src.Size()
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = s.pos + offset
	case io.SeekEnd:
		abs = size + offset
	default:
		return 0, errors.New("storageSourceSeeker: invalid whence")
	}
	if abs < 0 {
		return 0, errors.New("storageSourceSeeker: negative position")
	}
	s.pos = abs
	return abs, nil
}

// serveLocalBookFile serves a local delivery answer via c.File() (honors
// Range headers natively). The path has already passed the Book file
// sandbox: a "local" BookSource only ever leaves the LibraryHandle that
// way.
func (h *Handler) serveLocalBookFile(c *gin.Context, path, mime string) error {
	c.Header("Content-Type", mime)
	c.Header("Cache-Control", "private, max-age=3600")
	c.File(path)
	return nil
}

func parseIntOr(s string, def int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
