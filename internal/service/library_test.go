// SPDX-License-Identifier: AGPL-3.0-or-later

package service_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/service"
)

// newLibSvc is a helper that wires up a LibraryService backed by the
// provided test DB. deps.Resolver is intentionally left nil (purge
// tests will override it in their own helper).
func newLibSvc(t *testing.T, deps service.LibraryServiceDeps) *service.LibraryService {
	t.Helper()
	d := repotest.New(t)
	lr := repo.NewLibraryRepo(d)
	bookr := repo.NewBookRepo(d)
	br := repo.NewStorageBackendRepo(d)
	deps.Backends = br
	// The writer is a required argument, so the lifecycle tests build the
	// real one over the same test DB. Nothing here edits a book, but the
	// service cannot be constructed without the edit-side pipeline any
	// more — which is the point: there is no wiring that skips it.
	return service.NewLibraryService(lr, bookr, deps, service.NewMetadataWriter(
		service.MetadataWriterDeps{Books: bookr},
	))
}

// TestLibraryService_Create_local verifies that creating a local library
// derives its path under DataPath, mkdirs it, and sets a nil BackendID.
func TestLibraryService_Create_local(t *testing.T) {
	dataPath := t.TempDir()
	svc := newLibSvc(t, service.LibraryServiceDeps{DataPath: dataPath})
	ctx := context.Background()

	lib, err := svc.Create(ctx, "My Fiction", service.LibraryKindLocal)
	if err != nil {
		t.Fatalf("Create local: %v", err)
	}
	want := filepath.Join(dataPath, "libraries", "my-fiction")
	if lib.Path != want {
		t.Errorf("Path=%q want %q", lib.Path, want)
	}
	if _, err := os.Stat(lib.Path); err != nil {
		t.Errorf("library dir not created: %v", err)
	}
	if lib.BackendID != nil {
		t.Errorf("BackendID should be nil for local library, got %v", lib.BackendID)
	}
	if lib.Slug != "my-fiction" {
		t.Errorf("Slug=%q want my-fiction", lib.Slug)
	}
}

// TestLibraryService_Create_emptyKind verifies that kind="" defaults to local.
func TestLibraryService_Create_emptyKind(t *testing.T) {
	dataPath := t.TempDir()
	svc := newLibSvc(t, service.LibraryServiceDeps{DataPath: dataPath})
	ctx := context.Background()

	lib, err := svc.Create(ctx, "Classics", "")
	if err != nil {
		t.Fatalf("Create empty kind: %v", err)
	}
	if lib.BackendID != nil {
		t.Errorf("BackendID should be nil for default (local) library, got %v", lib.BackendID)
	}
	want := filepath.Join(dataPath, "libraries", "classics")
	if lib.Path != want {
		t.Errorf("Path=%q want %q", lib.Path, want)
	}
	if _, err := os.Stat(lib.Path); err != nil {
		t.Errorf("library dir not created: %v", err)
	}
}

// TestLibraryService_Create_LocalRequiresDataPath verifies that creating
// a local library without DataPath configured returns
// ErrDataPathNotConfigured.
func TestLibraryService_Create_LocalRequiresDataPath(t *testing.T) {
	svc := newLibSvc(t, service.LibraryServiceDeps{}) // DataPath empty
	_, err := svc.Create(context.Background(), "Test", service.LibraryKindLocal)
	if !errors.Is(err, service.ErrDataPathNotConfigured) {
		t.Errorf("err=%v want ErrDataPathNotConfigured", err)
	}
}

// TestLibraryService_Create_s3_notConfigured verifies that requesting
// kind=s3 without a bucket returns ErrS3NotConfigured.
func TestLibraryService_Create_s3_notConfigured(t *testing.T) {
	svc := newLibSvc(t, service.LibraryServiceDeps{
		// SharedS3 has empty Bucket → Configured() == false.
	})
	ctx := context.Background()

	_, err := svc.Create(ctx, "S3 Lib", service.LibraryKindS3)
	if !errors.Is(err, service.ErrS3NotConfigured) {
		t.Fatalf("got err=%v, want ErrS3NotConfigured", err)
	}
}

// TestLibraryService_Create_s3_configured verifies that when the shared
// bucket is set, Create allocates a backend row and a library row
// pointing to it.
func TestLibraryService_Create_s3_configured(t *testing.T) {
	svc := newLibSvc(t, service.LibraryServiceDeps{
		SharedS3: config.SharedS3Config{
			Bucket: "my-bucket",
			Region: "us-east-1",
		},
	})
	ctx := context.Background()

	lib, err := svc.Create(ctx, "Sci Fi", service.LibraryKindS3)
	if err != nil {
		t.Fatalf("Create s3: %v", err)
	}
	if lib.BackendID == nil {
		t.Fatal("BackendID should be non-nil for s3 library")
	}
	if lib.Path != "" {
		t.Errorf("Path should be empty for s3 library, got %q", lib.Path)
	}
	// Slug should be derived from name.
	if lib.Slug != "sci-fi" {
		t.Errorf("Slug=%q want sci-fi", lib.Slug)
	}
}

// TestLibraryService_Create_unknownKind verifies that an unrecognised kind
// returns an error.
func TestLibraryService_Create_unknownKind(t *testing.T) {
	svc := newLibSvc(t, service.LibraryServiceDeps{})
	ctx := context.Background()

	_, err := svc.Create(ctx, "Bad Kind", "nfs")
	if err == nil {
		t.Fatal("expected error for unknown kind, got nil")
	}
}
