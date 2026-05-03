package handler

import (
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
)

// mimeForFormat returns the response Content-Type for a given book format,
// or "" when the format doesn't have a reader yet.
func mimeForFormat(format string) string {
	switch format {
	case "EPUB":
		return "application/epub+zip"
	case "PDF":
		return "application/pdf"
	case "CBZ":
		return "application/vnd.comicbook+zip"
	case "MP3":
		return "audio/mpeg"
	case "M4B":
		// Apple uses audio/mp4; the m4b container is identical to m4a.
		return "audio/mp4"
	}
	return ""
}

// serveBookFile chooses between a presigned redirect and local serve
// based on the book's backing library. When the library's backend
// advertises CapPresign and the deployment is not forcing stream, a
// 302 redirect to a presigned URL is issued. Otherwise the bytes are
// served via serveLocalBookFile (c.File()).
//
// Falls back to local serve when libStore is unwired (single-backend
// installs that haven't built a LibraryStore).
func (h *Handler) serveBookFile(c *gin.Context, book model.Book, mime string) error {
	if h.libStore == nil {
		return h.serveLocalBookFile(c, book.Path, mime)
	}
	handle, err := h.libStore.For(c.Request.Context(), book.LibraryID)
	if err != nil {
		return h.serveLocalBookFile(c, book.Path, mime)
	}
	src, err := handle.BookSource(c.Request.Context(), book)
	if err != nil {
		return err
	}
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

// serveLocalBookFile validates that path is rooted under either BOOKDROP_PATH
// or one of the registered library_paths, then serves the bytes with the
// given content type via c.File() (honors Range headers natively).
func (h *Handler) serveLocalBookFile(c *gin.Context, path, mime string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return errors.New("bad path")
	}

	roots := []string{}
	if h.cfg.BookDropPath != "" {
		if r, err := filepath.Abs(h.cfg.BookDropPath); err == nil {
			roots = append(roots, r)
		}
	}
	if h.lib != nil {
		if libs, err := h.lib.List(c.Request.Context()); err == nil {
			for _, l := range libs {
				if l.Path == "" {
					continue
				}
				if r, err := filepath.Abs(l.Path); err == nil {
					roots = append(roots, r)
				}
			}
		}
	}
	if len(roots) == 0 {
		return errors.New("no allowed roots configured")
	}

	sep := string(filepath.Separator)
	allowed := false
	for _, root := range roots {
		if absPath == root || strings.HasPrefix(absPath, root+sep) {
			allowed = true
			break
		}
	}
	if !allowed {
		return errors.New("path outside allowed roots")
	}

	c.Header("Content-Type", mime)
	c.Header("Cache-Control", "private, max-age=3600")
	c.File(absPath)
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
