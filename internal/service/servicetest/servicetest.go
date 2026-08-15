// SPDX-License-Identifier: AGPL-3.0-or-later

// Package servicetest is the service tier's sibling of storagetest and
// repotest: in-memory adapters for the LibraryStore seam, so handler,
// task and recovery tests can build a working LibraryHandle without a
// Postgres schema (#338).
//
// The store this package hands out is the real one — service.
// NewLibraryStore over in-memory Libs and Files — so a handle behaves
// exactly as production's does, absence policies included. The escape
// hatch it replaces, a bare &service.LibraryHandle{}, was a handle whose
// nil files axis silently answered "no files" to every question.
package servicetest

import (
	"context"
	"sync"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
)

// Libraries is an in-memory service.LibraryByIDReader.
type Libraries struct {
	mu   sync.Mutex
	rows map[string]model.Library
}

// NewLibraries indexes the given rows by ID.
func NewLibraries(libs ...model.Library) *Libraries {
	l := &Libraries{rows: make(map[string]model.Library, len(libs))}
	for _, lib := range libs {
		l.rows[lib.ID] = lib
	}
	return l
}

func (l *Libraries) GetByID(_ context.Context, id string) (model.Library, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	lib, ok := l.rows[id]
	if !ok {
		return model.Library{}, repo.ErrNotFound
	}
	return lib, nil
}

// Files is an in-memory service.BookFileLister: rows keyed by book id.
type Files struct {
	mu   sync.Mutex
	rows map[string][]model.File
	// Err, when set, fails every list — the "files table unreachable"
	// arm no fake used to be able to reach.
	Err error
}

// NewFiles starts empty; Add rows per book.
func NewFiles() *Files {
	return &Files{rows: make(map[string][]model.File)}
}

// Add appends rows for a book and returns the same *Files for chaining.
func (f *Files) Add(bookID string, rows ...model.File) *Files {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[bookID] = append(f.rows[bookID], rows...)
	return f
}

// Replace swaps a book's rows wholesale — how a test says "the bytes
// changed" (a new content hash, a new location) without a second fake.
func (f *Files) Replace(bookID string, rows ...model.File) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[bookID] = rows
}

func (f *Files) ListByBook(_ context.Context, bookID string) ([]model.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return nil, f.Err
	}
	out := make([]model.File, len(f.rows[bookID]))
	copy(out, f.rows[bookID])
	return out, nil
}

// ObjectStore wraps a Storage (usually local.New over a t.TempDir()) so
// it advertises the object-store capability the key rule branches on —
// the one fake three packages each hand-rolled.
type ObjectStore struct{ storage.Storage }

func (ObjectStore) Capabilities() storage.Capability { return storage.CapObjectStore }

// StoreOptions configures a servicetest LibraryStore.
type StoreOptions struct {
	// Libraries the store can resolve. A For on any other id answers
	// repo.ErrNotFound, like the real repo.
	Libraries []model.Library
	// Storage every library's backend resolves to. Nil leaves handles
	// without Storage — the degraded shape production gets on a resolve
	// failure.
	Storage storage.Storage
	// Files backs the handles' files axis. Nil means "no files table
	// wired", the same absence LibraryStoreDeps.Files == nil produces.
	Files *Files
	// NewPlacer, PresignTTL and PresignFallback pass through to the real
	// deps; all optional.
	NewPlacer       service.PlacerBuilder
	Orphans         service.PendingOrphansEnqueuer
	PresignTTL      time.Duration
	PresignFallback string
}

// NewStore builds a real service.LibraryStore over in-memory adapters.
func NewStore(opts StoreOptions) service.LibraryStore {
	deps := service.LibraryStoreDeps{
		Libs:            NewLibraries(opts.Libraries...),
		NewPlacer:       opts.NewPlacer,
		Orphans:         opts.Orphans,
		PresignTTL:      opts.PresignTTL,
		PresignFallback: opts.PresignFallback,
	}
	if opts.Storage != nil {
		deps.Resolver = storage.ConstantResolver{S: opts.Storage}
	}
	if opts.Files != nil {
		deps.Files = opts.Files
	}
	return service.NewLibraryStore(deps)
}

// Handle resolves one library through NewStore — the shortest route
// from "a test needs a working handle" to having one.
func Handle(ctx context.Context, opts StoreOptions, libraryID string) (*service.LibraryHandle, error) {
	return NewStore(opts).For(ctx, libraryID)
}
