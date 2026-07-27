// SPDX-License-Identifier: AGPL-3.0-or-later

// Package coverstore persists extracted cover images on the local filesystem.
//
// Files are stored with an extension that carries no meaning — the MIME type
// lives in the DB row alongside has_cover, so on serve we set Content-Type
// from the row and hand off the bytes. Three on-disk namespaces:
//
//	${root}/bookdrop/{bookdropID}        — pre-approval, shown in the queue preview
//	${root}/covers/<hex[:2]>/<hex><ext>  — content-addressed, current
//	${root}/books/{bookID}               — legacy id-keyed, being retired
//
// Only the first is one a caller names, because a staged item is not a book
// yet. An approved book's cover is reached with Open, which takes the book
// and consults the hash-keyed namespace before the legacy one.
//
// That fallback rule lives here rather than at the surfaces that serve
// covers. It used to be hand-written at three of them — the SPA cover
// route, the OPDS cover route, and the Covers backfill — which meant every
// caller had to know that a migration was mid-flight, and none of them
// tested that it knew correctly. Keeping it inside the module makes the
// migration state invisible from outside and makes retiring `books/` an
// edit to this file.
//
// Writes use a temp-file-and-rename dance so a crash never leaves
// half-written bytes behind.
package coverstore

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/blackforge/embookshelf/internal/model"
)

type Store struct {
	root string
}

// New returns a Store rooted at path. The directory tree is created on first
// write.
func New(path string) *Store {
	return &Store{root: path}
}

// The three path builders are unexported on purpose: a caller that can
// name a key can implement its own resolution order, which is the thing
// this module exists to prevent.
func (s *Store) bookDropPath(id string) string { return filepath.Join(s.root, "bookdrop", id) }
func (s *Store) legacyPath(id string) string   { return filepath.Join(s.root, "books", id) }

// hashedPath returns the disk path for a cover keyed by content hash.
// Layout: covers/<hash[0:2]>/<hash><ext>. Hash is hex-encoded.
func (s *Store) hashedPath(hash []byte, mime string) string {
	if len(hash) == 0 {
		return ""
	}
	hex := fmt.Sprintf("%x", hash)
	return filepath.Join(s.root, "covers", hex[:2], hex+extForMIME(mime))
}

// ---------------------------------------------------------------------------
// Pre-approval staging
// ---------------------------------------------------------------------------

// SaveBookDrop writes bytes to bookdrop/{id}. Atomic: writes to a .tmp
// sibling, then renames.
func (s *Store) SaveBookDrop(id string, data []byte) error {
	return writeAtomic(s.bookDropPath(id), data)
}

// OpenBookDrop returns a handle to the bookdrop item's cover preview.
// Keyed by bookdrop id rather than by book, because at this point there is
// no books row to key on.
func (s *Store) OpenBookDrop(id string) (io.ReadCloser, error) {
	return os.Open(s.bookDropPath(id))
}

// DeleteBookDrop removes a pre-approval cover (called when a bookdrop item is
// rejected).
func (s *Store) DeleteBookDrop(id string) error {
	return removeIfExists(s.bookDropPath(id))
}

// PromoteBookDrop reads a staged cover at bookdrop/{id}, hashes it,
// writes the bytes under the hash-keyed namespace, and best-effort
// deletes the bookdrop staging file. Returns the sha256 digest the caller
// should record on the books row's cover_hash column.
//
// Each step short-circuits on failure: if Open fails, SaveBookHashed
// never runs; if SaveBookHashed fails, DeleteBookDrop never runs. The
// staging file survives any failure so a future retry can pick up where
// this one left off.
func (s *Store) PromoteBookDrop(bookdropID, mime string) ([]byte, error) {
	rc, err := s.OpenBookDrop(bookdropID)
	if err != nil {
		return nil, fmt.Errorf("open bookdrop cover: %w", err)
	}
	data, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		return nil, fmt.Errorf("read bookdrop cover: %w", err)
	}
	sum := sha256.Sum256(data)
	if err := s.SaveBookHashed(sum[:], mime, data); err != nil {
		return nil, fmt.Errorf("save hashed cover: %w", err)
	}
	// Best-effort cleanup; non-fatal — the bytes are already promoted.
	_ = s.DeleteBookDrop(bookdropID)
	return sum[:], nil
}

// ---------------------------------------------------------------------------
// Approved books
// ---------------------------------------------------------------------------

// Open returns the bytes of an approved book's cover, from whichever
// namespace currently holds them. The caller must Close the reader.
//
// The hash-keyed copy wins when the row has a cover_hash and the file is
// there; everything else falls through to the legacy id-keyed path, which
// is where covers live until the Covers backfill reaches them. A book that
// has neither surfaces as os.ErrNotExist, so callers keep one branch for
// "no cover" and one for "the disk is broken".
//
// A hashed read that fails for any reason *other* than absence is returned
// as it is. Falling through on a permissions or I/O fault would report a
// server fault as a missing cover, and would serve pre-backfill bytes for
// a book whose current cover we simply failed to read.
func (s *Store) Open(book model.Book) (io.ReadCloser, error) {
	if len(book.CoverHash) > 0 {
		rc, err := os.Open(s.hashedPath(book.CoverHash, book.CoverMime))
		if err == nil {
			return rc, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return os.Open(s.legacyPath(book.ID))
}

// SaveBookHashed writes data atomically to the hash-keyed path.
// Idempotent: re-saving identical bytes is a no-op (Stat skip).
//
// Takes the digest rather than computing it because every caller already
// has one — it is also the value they write to books.cover_hash, and
// hashing the same bytes twice to agree with themselves is how the two
// drift.
func (s *Store) SaveBookHashed(hash []byte, mime string, data []byte) error {
	p := s.hashedPath(hash, mime)
	if p == "" {
		return errors.New("coverstore: empty hash")
	}
	if _, err := os.Stat(p); err == nil {
		return nil // already there
	}
	return writeAtomic(p, data)
}

// MigrateLegacy re-keys one book's legacy id-keyed cover into the
// hash-keyed namespace and returns the digest the caller records on
// books.cover_hash. This is the byte half of the Covers backfill; the
// mirror image of PromoteBookDrop, and here for the same reason — so the
// only code that names `books/` is in this file.
//
// Deliberately does *not* delete the legacy file. The DB write that makes
// the new key reachable belongs to the caller, and a cover whose only copy
// is gone before cover_hash lands is a cover nobody can serve. The caller
// sweeps with DeleteBook once the row is updated.
//
// A missing source returns os.ErrNotExist unwrapped, which is what lets
// the backfill tell a hand-deleted file from a broken disk.
func (s *Store) MigrateLegacy(book model.Book) ([]byte, error) {
	data, err := os.ReadFile(s.legacyPath(book.ID))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if err := s.SaveBookHashed(sum[:], book.CoverMime, data); err != nil {
		return nil, err
	}
	return sum[:], nil
}

// DeleteBook removes a book's legacy id-keyed cover. Missing is not an
// error.
//
// The last method still shaped by a key scheme, and it stays that way
// because its callers only ever have an id: a book being deleted, a
// library being deleted, a cover being replaced or cleared. The hashed
// copy is intentionally left alone in every one of those cases — those
// bytes are content-addressed and may be shared with another book, so
// there is no per-book delete for them at all.
//
// Goes away with the legacy namespace.
func (s *Store) DeleteBook(id string) error {
	return removeIfExists(s.legacyPath(id))
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
