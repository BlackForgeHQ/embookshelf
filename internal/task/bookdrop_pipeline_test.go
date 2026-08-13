// SPDX-License-Identifier: AGPL-3.0-or-later

package task

// bookDropPipeline is the harness the BookDrop end-to-end tests share
// across every format they cover (comic — #310, MOBI/AZW3 — #311, FB2 —
// #312): one library, one local backend wired to it the way a real boot
// leaves things (migrator.seedStorageBackends + wireLibraries), and a
// BookDrop service pointed at a staging directory. Each format's test
// stages its own fixture bytes and drives Intake → BookDropIngest →
// Approve through it — the real fileproc.Dispatch + ExtractBook pass,
// not just the format processor's own unit tests.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
)

type bookDropPipeline struct {
	svc      *service.BookDropService
	resolver *storage.MapResolver
	lib      model.Library
	libRoot  string
	staging  string
}

func newBookDropPipeline(t *testing.T) bookDropPipeline {
	t.Helper()
	ctx := context.Background()
	d := repotest.New(t)

	libRoot := filepath.Join(t.TempDir(), "library")
	if err := os.MkdirAll(libRoot, 0o755); err != nil {
		t.Fatalf("mkdir library root: %v", err)
	}

	libRepo := repo.NewLibraryRepo(d)
	lib, err := libRepo.CreateLibrary(ctx, "Test Library", "test-library", libRoot, nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	// migrator.seedStorageBackends + wireLibraries: one kind=local backend
	// per distinct path, wired onto the library.
	backend, err := repo.NewStorageBackendRepo(d).Create(ctx, "local", map[string]any{"root": libRoot})
	if err != nil {
		t.Fatalf("Create backend: %v", err)
	}
	if _, err := d.SQL.ExecContext(ctx,
		`UPDATE libraries SET backend_id = $1, root = path WHERE id = $2`, backend.ID, lib.ID,
	); err != nil {
		t.Fatalf("wire library to backend: %v", err)
	}

	// Every local backend is constructed rooted at "/" for the whole
	// instance (storageloader.buildBackend); a bookdrop item's path is an
	// absolute filesystem path outside the library root, and ingest's
	// resolver.Resolve("") needs to reach it the same way a real boot
	// would.
	fs, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	resolver := &storage.MapResolver{
		Default:  fs,
		Backends: map[string]storage.Storage{backend.ID: fs},
	}

	fileRepo := repo.NewFileRepo(d)
	staging := t.TempDir()
	svc := service.NewBookDropService(repo.NewBookDropRepo(d), libRepo, repo.NewBookRepo(d),
		nil, nil, fileRepo, &jobs.Deferred{}).
		WithLibraryStore(service.NewLibraryStore(service.LibraryStoreDeps{
			Libs:      libRepo,
			Resolver:  resolver,
			NewPlacer: service.DefaultPlacerBuilder(resolver),
			Files:     fileRepo,
		})).
		WithBookDropPath(staging)

	return bookDropPipeline{svc: svc, resolver: resolver, lib: lib, libRoot: libRoot, staging: staging}
}

// stage writes raw into the drop directory under name and returns the
// staged path, the way a watcher would find it.
func (p bookDropPipeline) stage(t *testing.T, name string, raw []byte) string {
	t.Helper()
	path := filepath.Join(p.staging, name)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write staged %s: %v", name, err)
	}
	return path
}

// readFixtureFile reads a checked-in fixture from disk (the comic
// archives, which live in internal/fileproc/testdata rather than as
// in-memory builders — see bookdrop_comic_test.go for why).
func readFixtureFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}
