package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/service"
)

// newLibSvc is a helper that wires up a LibraryService backed by the
// provided test DB. deps.Resolver is intentionally left nil (purge
// tests will override it in their own helper).
func newLibSvc(t *testing.T, dialect string, deps service.LibraryServiceDeps) *service.LibraryService {
	t.Helper()
	d := repotest.NewWithDialect(t, dialect)
	lr := repo.NewLibraryRepo(d)
	br := repo.NewStorageBackendRepo(d)
	deps.Backends = br
	deps.Dialect = config.Dialect(dialect)
	return service.NewLibraryService(lr, deps)
}

// TestLibraryService_Create_local verifies that creating a local library
// sets path + nil BackendID, and that kind="" defaults to local.
func TestLibraryService_Create_local(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		dialect := dialect
		t.Run(dialect, func(t *testing.T) {
			svc := newLibSvc(t, dialect, service.LibraryServiceDeps{})
			ctx := context.Background()

			lib, err := svc.Create(ctx, "My Fiction", service.LibraryKindLocal, "/tmp/fiction")
			if err != nil {
				t.Fatalf("Create local: %v", err)
			}
			if lib.Path != "/tmp/fiction" {
				t.Errorf("Path=%q want /tmp/fiction", lib.Path)
			}
			if lib.BackendID != nil {
				t.Errorf("BackendID should be nil for local library, got %v", lib.BackendID)
			}
			if lib.Slug != "my-fiction" {
				t.Errorf("Slug=%q want my-fiction", lib.Slug)
			}
		})
	}
}

// TestLibraryService_Create_emptyKind verifies that kind="" defaults to local.
func TestLibraryService_Create_emptyKind(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		dialect := dialect
		t.Run(dialect, func(t *testing.T) {
			svc := newLibSvc(t, dialect, service.LibraryServiceDeps{})
			ctx := context.Background()

			lib, err := svc.Create(ctx, "Classics", "", "/tmp/classics")
			if err != nil {
				t.Fatalf("Create empty kind: %v", err)
			}
			if lib.BackendID != nil {
				t.Errorf("BackendID should be nil for default (local) library, got %v", lib.BackendID)
			}
		})
	}
}

// TestLibraryService_Create_s3_notConfigured verifies that requesting
// kind=s3 without a bucket returns ErrS3NotConfigured.
func TestLibraryService_Create_s3_notConfigured(t *testing.T) {
	// Use postgres dialect to bypass the SQLite guard.
	svc := newLibSvc(t, "postgres", service.LibraryServiceDeps{
		// SharedS3 has empty Bucket → Configured() == false.
	})
	ctx := context.Background()

	_, err := svc.Create(ctx, "S3 Lib", service.LibraryKindS3, "")
	if !errors.Is(err, service.ErrS3NotConfigured) {
		t.Fatalf("got err=%v, want ErrS3NotConfigured", err)
	}
}

// TestLibraryService_Create_s3_sqliteGuard verifies that creating an s3
// library on a SQLite install returns an error.
func TestLibraryService_Create_s3_sqliteGuard(t *testing.T) {
	svc := newLibSvc(t, "sqlite", service.LibraryServiceDeps{
		SharedS3: config.SharedS3Config{Bucket: "test-bucket", Region: "us-east-1"},
	})
	ctx := context.Background()

	_, err := svc.Create(ctx, "S3 Lib", service.LibraryKindS3, "")
	if err == nil {
		t.Fatal("expected error creating s3 library on SQLite, got nil")
	}
}

// TestLibraryService_Create_s3_configured verifies that when the shared
// bucket is set and the dialect is postgres, Create allocates a backend row
// and a library row pointing to it.
func TestLibraryService_Create_s3_configured(t *testing.T) {
	svc := newLibSvc(t, "postgres", service.LibraryServiceDeps{
		SharedS3: config.SharedS3Config{
			Bucket: "my-bucket",
			Region: "us-east-1",
		},
	})
	ctx := context.Background()

	lib, err := svc.Create(ctx, "Sci Fi", service.LibraryKindS3, "")
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
	svc := newLibSvc(t, "sqlite", service.LibraryServiceDeps{})
	ctx := context.Background()

	_, err := svc.Create(ctx, "Bad Kind", "nfs", "/tmp/bad")
	if err == nil {
		t.Fatal("expected error for unknown kind, got nil")
	}
}
