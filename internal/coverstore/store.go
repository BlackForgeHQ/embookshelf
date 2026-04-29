// Package coverstore persists extracted cover images on the local filesystem.
//
// Files are stored without an extension — the MIME type lives in the DB row
// alongside has_cover, so on serve we just set Content-Type and hand off to
// http.ServeFile. Two logical namespaces:
//
//	${root}/bookdrop/{bookdropID}  — pre-approval, shown in the queue preview
//	${root}/books/{bookID}         — post-approval, shown everywhere else
//
// On approve, the file moves from bookdrop/ to books/ (rename on the same
// filesystem = atomic). Writes use a temp-file-and-rename dance so a crash
// never leaves half-written bytes behind.
package coverstore

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Store struct {
	root string
}

// New returns a Store rooted at path. The directory tree is created on first
// write.
func New(path string) *Store {
	return &Store{root: path}
}

func (s *Store) bookdropDir() string { return filepath.Join(s.root, "bookdrop") }
func (s *Store) bookDir() string     { return filepath.Join(s.root, "books") }

func (s *Store) BookDropPath(id string) string { return filepath.Join(s.bookdropDir(), id) }
func (s *Store) BookPath(id string) string     { return filepath.Join(s.bookDir(), id) }

// SaveBookDrop writes bytes to bookdrop/{id}. Atomic: writes to a .tmp
// sibling, then renames.
func (s *Store) SaveBookDrop(id string, data []byte) error {
	return writeAtomic(s.BookDropPath(id), data)
}

// SaveBook writes bytes to books/{id}. Used for manual uploads (not wired to
// a UI yet, but symmetric with SaveBookDrop).
func (s *Store) SaveBook(id string, data []byte) error {
	return writeAtomic(s.BookPath(id), data)
}

// PromoteBookDropToBook moves a bookdrop cover into the book namespace once
// the import is approved. Missing source is not an error — callers may pass
// through even when the processor didn't find a cover.
func (s *Store) PromoteBookDropToBook(bookdropID, bookID string) error {
	src := s.BookDropPath(bookdropID)
	dst := s.BookPath(bookID)
	if _, err := os.Stat(src); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// os.Rename on the same filesystem is atomic; across filesystems it
	// falls back to copy + unlink at the kernel level on most platforms.
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("promote cover: %w", err)
	}
	return nil
}

// OpenBook returns a handle to the book's cover. The caller must Close it.
func (s *Store) OpenBook(id string) (io.ReadCloser, error) {
	return os.Open(s.BookPath(id))
}

// OpenBookDrop returns a handle to the bookdrop item's cover preview.
func (s *Store) OpenBookDrop(id string) (io.ReadCloser, error) {
	return os.Open(s.BookDropPath(id))
}

// DeleteBook removes the on-disk cover for an approved book.
func (s *Store) DeleteBook(id string) error {
	return removeIfExists(s.BookPath(id))
}

// DeleteBookDrop removes a pre-approval cover (called when a bookdrop item is
// rejected).
func (s *Store) DeleteBookDrop(id string) error {
	return removeIfExists(s.BookDropPath(id))
}

// extForMIME maps a cover's MIME type to a filename suffix. Unknown
// MIMEs default to ".bin" — the cover still serves correctly because
// the response Content-Type comes from the DB row, not the path.
func extForMIME(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/avif":
		return ".avif"
	default:
		return ".bin"
	}
}

// hashedDir returns ${root}/covers (the new hash-keyed namespace).
func (s *Store) hashedDir() string { return filepath.Join(s.root, "covers") }

// HashedPath returns the disk path for a cover keyed by content hash.
// Layout: covers/<hash[0:2]>/<hash><ext>. Hash is hex-encoded.
func (s *Store) HashedPath(hash []byte, mime string) string {
	if len(hash) == 0 {
		return ""
	}
	hex := fmt.Sprintf("%x", hash)
	bucket := hex[:2]
	return filepath.Join(s.hashedDir(), bucket, hex+extForMIME(mime))
}

// SaveBookHashed writes data atomically to the hash-keyed path.
// Idempotent: re-saving identical bytes is a no-op (Stat skip).
func (s *Store) SaveBookHashed(hash []byte, mime string, data []byte) error {
	p := s.HashedPath(hash, mime)
	if p == "" {
		return errors.New("coverstore: empty hash")
	}
	if _, err := os.Stat(p); err == nil {
		return nil // already there
	}
	return writeAtomic(p, data)
}

// OpenBookHashed returns a reader for the cover at the hashed path.
func (s *Store) OpenBookHashed(hash []byte, mime string) (io.ReadCloser, error) {
	p := s.HashedPath(hash, mime)
	if p == "" {
		return nil, os.ErrNotExist
	}
	return os.Open(p)
}

// DeleteBookHashed removes the cover at the hashed path. Missing is
// not an error.
func (s *Store) DeleteBookHashed(hash []byte, mime string) error {
	p := s.HashedPath(hash, mime)
	if p == "" {
		return nil
	}
	return removeIfExists(p)
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		// Best-effort cleanup on failure.
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
