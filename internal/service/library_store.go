// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/sidecar"
	"github.com/blackforge/embookshelf/internal/storage"
)

// Delivery modes for BookSource.Kind and the EMBOOKSHELF_PRESIGN_FALLBACK
// env var. The env var name predates the inversion; the string values
// here are the canonical delivery-mode identifiers.
const (
	BookDeliveryPresign = "presign"
	BookDeliveryStream  = "stream"
	BookDeliveryLocal   = "local"
)

// LibraryStore turns a libraryID into a ready-to-use view of that
// Library: the row, the Storage it lives in, the Placer that knows how
// to write into it, and the delivery decision for serving its bytes to
// clients.
//
// Deep seam: callers asking "give me access to library X" — Approve,
// the file-serve handler, audio re-extract, and (eventually) library
// scan + files backfill — all route through For. The Library + its
// Backend (CONTEXT.md: Library is "pinned to one Backend via
// libraries.backend_id") travel together because they always do.
//
// Stateless. Each For() does a fresh LibraryRepo lookup + Resolver
// call. Library mutations are admin-rare; the per-call cost is one
// indexed PK lookup. Cache only on profiling signal.
type LibraryStore interface {
	For(ctx context.Context, libraryID string) (*LibraryHandle, error)
}

// LibraryHandle bundles the Library row with the seams it pins to
// (Storage, Placer) plus the delivery glue. Methods deliver the
// answers callers actually want — relativize, open, deliver — so they
// do not reach back into Resolver / Storage themselves.
//
// Storage may be nil when the Resolver could not resolve the library's
// backend (transient backend error; library still useful for metadata
// reads). Placer may be nil for libraries that have no path AND no
// backend. Callers that require either field check for nil.
type LibraryHandle struct {
	Library model.Library
	Storage storage.Storage
	Placer  Placer

	// PlacerErr holds the reason Placer is nil. Callers that require a
	// Placer (Approve) surface this so admins see the real cause —
	// missing backend, deleted backend_id, library with no path —
	// instead of an opaque "no placer" message.
	PlacerErr error

	files           bookFileLister
	orphans         PendingOrphansEnqueuer
	presignTTL      time.Duration
	presignFallback string
}

// bookFileLister is the slice of FileRepo a handle actually needs to
// find a book's bytes. Narrow so the delivery logic is testable
// without a database.
type bookFileLister interface {
	ListByBook(ctx context.Context, bookID string) ([]model.File, error)
}

// IsBackendBacked reports whether this library's bytes live in a
// Storage backend rather than on the local filesystem. Names the
// question once so callers stop peeking at libraries.backend_id: the
// in-file metadata embed (ADR-0001) and the folder-rename strategy
// (ADR-0005) both branch on it.
func (h *LibraryHandle) IsBackendBacked() bool {
	return h.Library.BackendID != nil
}

// storageKey turns a files.location into the key this library's Storage
// actually answers to.
//
// files.location is relative to the library root (CONTEXT, "Files row"),
// but a local install's LocalFS is rooted at "/" and expects absolute
// keys — it is deliberately not rooted per library, because the scan
// worker and bookdrop ingest hand it absolute paths. Nothing reconciled
// the two, so every read of a locally-placed book asked the filesystem
// for "/Author/Title/book.epub" and got nothing.
//
// The symptom was quiet in one direction and loud in the other: reading
// a book failed outright, while reading-guide generation degrades an
// unreadable book to a metadata-only guide by design, so every EPUB on a
// local library silently produced the weaker guide ADR-0024 §2 reserves
// for formats with no extractable text.
//
// Backend-backed libraries are untouched: their keys are already the
// object keys, and the backend encodes its own prefix.
func (h *LibraryHandle) storageKey(location string) string {
	if h.IsBackendBacked() {
		return location
	}
	// Already absolute: a legacy row. books.path predates storage-v2, and
	// the storage-v2 backfill wrote files.location verbatim whenever the
	// library root was unknown at seed time (migrator.seedFilesFromBooks,
	// which its own tests pin). Such a string is already the key a
	// "/"-rooted LocalFS wants; joining it onto the root asks for
	// /lib/root/lib/root/… and finds nothing.
	//
	// This is what makes the shim total over both shapes, which is what
	// lets the edit-side write pipeline read books.path — mixed to this
	// day — through it (#168).
	if filepath.IsAbs(location) {
		return location
	}
	if abs := h.LocalPath(location); abs != "" {
		return abs
	}
	return location
}

// OpenBook returns the book's bytes, wherever they live. Callers that
// want content — Send-to-Kindle, device push — use this and never
// learn the delivery vocabulary; BookSource stays for the file-serve
// handler, which genuinely needs a routing answer.
//
// Deliberately never presigns: a presigned URL is an answer for the
// browser, useless to a caller that needs the bytes in-process.
//
// The returned Closer is always non-nil on success and must be closed.
func (h *LibraryHandle) OpenBook(ctx context.Context, book model.Book) (io.Reader, int64, io.Closer, error) {
	if h.Storage != nil && h.files != nil {
		if f, err := primaryFile(ctx, h.files, book); err == nil {
			key := h.storageKey(f.Location)
			src, oerr := h.Storage.Open(ctx, key)
			if oerr != nil {
				return nil, 0, nil, fmt.Errorf("open %s: %w", key, oerr)
			}
			return io.NewSectionReader(src, 0, src.Size()), src.Size(), src, nil
		}
	}

	if book.Path == "" {
		return nil, 0, nil, errors.New("book has no stored file to open")
	}
	file, err := os.Open(book.Path)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("open book file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, nil, err
	}
	return file, info.Size(), file, nil
}

// BookDeleteGrace is how long a deleted book's bytes linger on a
// backend-backed library before the sweeper removes them. Comfortably
// longer than any presigned URL this application issues, so a download
// already in flight when the row went finishes rather than 404s.
const BookDeleteGrace = time.Hour

// BookFileLocations lists the storage keys belonging to a book.
//
// Split from DeleteBookBytes on purpose. Deleting a book cascades its
// files rows, so the keys must be read *before* the row goes and the
// bytes removed *after* it has — a single method taking only a book
// would have to pick one side of that and would be wrong either way.
// A nil error with no keys is the normal answer for a book whose files
// were never backfilled.
func (h *LibraryHandle) BookFileLocations(ctx context.Context, bookID string) ([]string, error) {
	if h.files == nil {
		return nil, nil
	}
	list, err := h.files.ListByBook(ctx, bookID)
	if err != nil {
		return nil, fmt.Errorf("list files for book %s: %w", bookID, err)
	}
	out := make([]string, 0, len(list))
	for _, f := range list {
		if f.Location != "" {
			out = append(out, f.Location)
		}
	}
	return out, nil
}

// DeleteBookBytes removes the objects a book owned, given the keys
// BookFileLocations returned before the row was deleted.
//
// Exists because deleting a book used to unlink only books.path — the
// legacy single-path field — so a book on a backend-backed library left
// its objects behind entirely, and a book with more than one file left
// all but one. Tolerable while a book was one 2 MB EPUB; not once a
// generated narration is half a gigabyte (ADR-0025 §6).
//
// Local libraries delete inline. Backend-backed ones enqueue the keys
// for the orphan sweeper instead (ADR-0005), because the bytes may still
// be serving a presigned URL. Best-effort per key: one unreachable
// object does not strand the rest, and every failure is reported in the
// returned error. Callers treat that error as a warning — the row is
// already gone and lying about it would be worse.
func (h *LibraryHandle) DeleteBookBytes(ctx context.Context, bookID string, locations []string) error {
	if len(locations) == 0 {
		return nil
	}

	if h.IsBackendBacked() {
		if h.orphans == nil {
			return nil
		}
		id := bookID
		rows := make([]repo.PendingOrphanInsert, 0, len(locations))
		eligible := time.Now().Add(BookDeleteGrace)
		for _, key := range locations {
			rows = append(rows, repo.PendingOrphanInsert{
				LibraryID:  h.Library.ID,
				Key:        key,
				EligibleAt: eligible,
				Reason:     repo.ReasonOrphanBookDelete,
				BookID:     &id,
			})
		}
		if err := h.orphans.Insert(ctx, rows); err != nil {
			return fmt.Errorf("enqueue orphans for book %s: %w", bookID, err)
		}
		return nil
	}

	if h.Storage == nil {
		return nil
	}
	var failures []error
	for _, location := range locations {
		// Through storageKey, not the raw location: a local install's
		// LocalFS is rooted at "/" and answers to absolute keys, so
		// handing it the library-relative files.location asked the
		// filesystem to delete "/Author/Title/book.epub" and quietly
		// removed nothing. The same reason OpenBook goes through it.
		key := h.storageKey(location)
		if derr := h.Storage.Delete(ctx, key); derr != nil && !errors.Is(derr, storage.ErrNotFound) {
			failures = append(failures, fmt.Errorf("delete %s: %w", key, derr))
		}
	}
	return errors.Join(failures...)
}

// OpenBookSource is OpenBook for callers that need random access — the
// EPUB text extractor reads a zip central directory and cannot work from
// a stream. Resolution is not duplicated: it delegates to OpenBook and
// adapts, so the "never reach around this with os.Open(book.Path)" rule
// holds here too.
//
// The returned Source must be closed.
func (h *LibraryHandle) OpenBookSource(ctx context.Context, book model.Book) (storage.Source, error) {
	r, size, closer, err := h.OpenBook(ctx, book)
	if err != nil {
		return nil, err
	}
	// Backend path: the Closer is the Source OpenBook opened.
	if src, ok := closer.(storage.Source); ok {
		return src, nil
	}
	// Local path: *os.File is both ReaderAt and Closer.
	if ra, ok := r.(io.ReaderAt); ok {
		return readerAtSource{ReaderAt: ra, size: size, closer: closer}, nil
	}
	_ = closer.Close()
	return nil, errors.New("book bytes are not randomly accessible")
}

// readerAtSource adapts a ReaderAt plus a size into a storage.Source.
type readerAtSource struct {
	io.ReaderAt
	size   int64
	closer io.Closer
}

func (s readerAtSource) Size() int64  { return s.size }
func (s readerAtSource) Close() error { return s.closer.Close() }

// SidecarKey returns the paired JSON sidecar storage key for a book
// file's storage key. Delegates to sidecar.KeyFor so the derivation
// rule lives in one place.
func (h *LibraryHandle) SidecarKey(bookLocation string) string {
	return sidecar.KeyFor(bookLocation)
}

// Relativize strips the library root from abs. Returns abs unchanged
// when it doesn't sit under the root — defensive fallback for callers
// that pass an already-relative location.
func (h *LibraryHandle) Relativize(abs string) string {
	root := ""
	if h.Library.Root != nil {
		root = *h.Library.Root
	}
	if root == "" {
		root = h.Library.Path
	}
	if root == "" {
		return abs
	}
	prefix := root
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if strings.HasPrefix(abs, prefix) {
		return abs[len(prefix):]
	}
	return abs
}

// Open opens a Source against the library's storage at the given
// library-relative location. Errors when the handle has no Storage
// (resolver failed at construction).
func (h *LibraryHandle) Open(ctx context.Context, location string) (storage.Source, error) {
	if h.Storage == nil {
		return nil, errors.New("library handle: no storage")
	}
	return h.Storage.Open(ctx, location)
}

// BookSource returns the delivery decision for serving a book through
// this library. Picks one of three modes based on storage capabilities
// and config:
//
//   - "presign" — 302 redirect to a pre-signed URL. Opt-in via
//     EMBOOKSHELF_PRESIGN_FALLBACK="presign" (the env var name
//     predates the inversion; treat it as the delivery mode). Requires
//     the bucket to be configured with CORS rules that allow the SPA
//     origin, otherwise browser XHR (epub.js, pdf.js) will fail on
//     the cross-origin redirect.
//   - "stream" — open the bytes via Storage.Get/Open and pipe them
//     through the app server. Default for any backend that has Storage
//     wired (LocalFS or S3). Avoids the CORS/redirect pitfalls of
//     presign at the cost of routing bytes through the app.
//   - "local" — legacy fallback for installs where Storage / files
//     wiring is missing; the handler streams book.Path off disk.
func (h *LibraryHandle) BookSource(ctx context.Context, book model.Book) (BookSource, error) {
	if book.Path == "" && (h.Storage == nil || h.files == nil) {
		return BookSource{}, errors.New("book has no path and storage/files are unavailable")
	}

	if h.Storage == nil || h.files == nil {
		return BookSource{Kind: BookDeliveryLocal, Path: book.Path}, nil
	}

	f, ferr := primaryFile(ctx, h.files, book)

	if h.presignFallback == BookDeliveryPresign && h.Storage.Capabilities()&storage.CapPresign != 0 {
		if ps, ok := h.Storage.(Presigner); ok && ferr == nil {
			// A no-op today — only backend-backed libraries advertise
			// CapPresign, and storageKey passes their keys through
			// untouched — but the raw location was the odd one out among
			// this file's four key resolutions (#168).
			if url, err := ps.PresignGet(ctx, h.storageKey(f.Location), h.presignTTL); err == nil {
				return BookSource{Kind: BookDeliveryPresign, URL: url, TTL: h.presignTTL}, nil
			}
		}
	}

	if ferr == nil {
		return BookSource{
			Kind:    BookDeliveryStream,
			Storage: h.Storage,
			Key:     h.storageKey(f.Location),
		}, nil
	}

	if book.Path != "" {
		return BookSource{Kind: BookDeliveryLocal, Path: book.Path}, nil
	}
	return BookSource{}, fmt.Errorf("book source: %w", ferr)
}

// BookSource describes how a book file should be delivered.
//
//   - Kind=="local"   → stream Path through the app server (filesystem path).
//   - Kind=="presign" → 302 redirect to URL, valid for TTL.
//   - Kind=="stream"  → open Key on Storage and pipe through the app.
type BookSource struct {
	Kind    string
	Path    string
	URL     string
	TTL     time.Duration
	Storage storage.Storage
	Key     string
}

// Presigner is the capability-gated interface CapPresign-bearing
// backends satisfy. Backends are probed by type assertion so the
// storage interface stays minimal.
type Presigner interface {
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// LibraryStoreDeps groups everything LibraryStore needs to build a
// LibraryHandle. PresignTTL and PresignFallback feed BookSource; pass
// zero values to disable presign (handle will always pick local).
type LibraryStoreDeps struct {
	Libs      *repo.LibraryRepo
	Resolver  storage.Resolver
	NewPlacer PlacerBuilder
	Files     *repo.FileRepo
	// Orphans defers byte deletion on backend-backed libraries. Optional:
	// when nil, DeleteBookBytes degrades to leaving the bytes for a human
	// rather than deleting something a presigned URL may still be serving.
	Orphans         PendingOrphansEnqueuer
	PresignTTL      time.Duration
	PresignFallback string
}

// NewLibraryStore builds the default LibraryStore — stateless,
// fresh-lookup-per-call.
func NewLibraryStore(deps LibraryStoreDeps) LibraryStore {
	return &defaultLibraryStore{deps: deps}
}

type defaultLibraryStore struct {
	deps LibraryStoreDeps
}

func (s *defaultLibraryStore) For(ctx context.Context, libraryID string) (*LibraryHandle, error) {
	if s.deps.Libs == nil {
		return nil, errors.New("library store: no library repo")
	}
	lib, err := s.deps.Libs.GetByID(ctx, libraryID)
	if err != nil {
		return nil, fmt.Errorf("library store: lookup: %w", err)
	}

	var store storage.Storage
	if s.deps.Resolver != nil {
		backendID := ""
		if lib.BackendID != nil {
			backendID = *lib.BackendID
		}
		// Resolve failure is non-fatal: caller checks Storage for nil
		// before reaching for bytes. Library-only callers (e.g. just
		// reading the row) still get a usable handle.
		if st, rerr := s.deps.Resolver.Resolve(backendID); rerr == nil {
			store = st
		}
	}

	var placer Placer
	var placerErr error
	if s.deps.NewPlacer != nil {
		// Placer build failure is non-fatal at handle construction —
		// metadata-only callers still get a usable handle. The error is
		// captured so write-path callers (Approve) surface the real
		// reason instead of "no placer".
		if p, perr := s.deps.NewPlacer(lib); perr == nil {
			placer = p
		} else {
			placerErr = perr
		}
	} else {
		placerErr = errors.New("no placer builder configured")
	}

	// Assign through a nil check: a nil *repo.FileRepo stored in the
	// interface field would be a non-nil interface, and every
	// h.files == nil guard below would silently stop working.
	var files bookFileLister
	if s.deps.Files != nil {
		files = s.deps.Files
	}

	return &LibraryHandle{
		Library:         lib,
		Storage:         store,
		Placer:          placer,
		PlacerErr:       placerErr,
		files:           files,
		orphans:         s.deps.Orphans,
		presignTTL:      s.deps.PresignTTL,
		presignFallback: s.deps.PresignFallback,
	}, nil
}

// primaryFile finds the canonical files row for a book — the row whose
// format matches books.format. Falls back to the first row when no
// format match exists; returns an error when there are no rows yet
// (pre-files-backfill installs).
func primaryFile(ctx context.Context, files bookFileLister, book model.Book) (model.File, error) {
	list, err := files.ListByBook(ctx, book.ID)
	if err != nil {
		return model.File{}, err
	}
	for _, f := range list {
		if f.Format == book.Format {
			return f, nil
		}
	}
	if len(list) > 0 {
		return list[0], nil
	}
	return model.File{}, fmt.Errorf("no files row for book %s", book.ID)
}
