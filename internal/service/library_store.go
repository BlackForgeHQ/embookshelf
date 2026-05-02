package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/sidecar"
	"github.com/blackforge/embookshelf/internal/storage"
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

	files           *repo.FileRepo
	presignTTL      time.Duration
	presignFallback string
}

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
// this library. Picks presign (when the backend advertises CapPresign
// AND the deployment is not forcing stream) or falls back to local.
//
// Falls back to local on every failure mode along the chain — a
// missing files row, a presign error, an unsupported cast — because
// the legacy local-serve path always works for single-backend installs
// (book.Path is still populated alongside files.location).
func (h *LibraryHandle) BookSource(ctx context.Context, book model.Book) (BookSource, error) {
	if book.Path == "" {
		return BookSource{}, errors.New("book has no path")
	}
	src := BookSource{Kind: "local", Path: book.Path}

	if h.Storage == nil || h.files == nil {
		return src, nil
	}
	if h.Storage.Capabilities()&storage.CapPresign == 0 || h.presignFallback == "stream" {
		return src, nil
	}
	ps, ok := h.Storage.(Presigner)
	if !ok {
		return src, nil
	}
	f, err := primaryFile(ctx, h.files, book)
	if err != nil {
		return src, nil
	}
	url, err := ps.PresignGet(ctx, f.Location, h.presignTTL)
	if err != nil {
		return src, nil
	}
	return BookSource{Kind: "presign", URL: url, TTL: h.presignTTL}, nil
}

// BookSource describes how a book file should be delivered. Kind is
// either "local" (stream Path through the app server) or "presign"
// (302 redirect to URL valid for TTL).
type BookSource struct {
	Kind string
	Path string
	URL  string
	TTL  time.Duration
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
	Libs            *repo.LibraryRepo
	Resolver        storage.Resolver
	NewPlacer       PlacerBuilder
	Files           *repo.FileRepo
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
	if s.deps.NewPlacer != nil {
		// Placer build failure is non-fatal for the same reason.
		if p, perr := s.deps.NewPlacer(lib); perr == nil {
			placer = p
		}
	}

	return &LibraryHandle{
		Library:         lib,
		Storage:         store,
		Placer:          placer,
		files:           s.deps.Files,
		presignTTL:      s.deps.PresignTTL,
		presignFallback: s.deps.PresignFallback,
	}, nil
}

// primaryFile finds the canonical files row for a book — the row whose
// format matches books.format. Falls back to the first row when no
// format match exists; returns an error when there are no rows yet
// (pre-files-backfill installs).
func primaryFile(ctx context.Context, files *repo.FileRepo, book model.Book) (model.File, error) {
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
