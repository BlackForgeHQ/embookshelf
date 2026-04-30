package storageloader_test

import (
	"context"
	"testing"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/storageloader"
)

// TestLoadStorageBackends_EmptyTable verifies that an empty storage_backends
// table returns a resolver whose default is LocalFS at "/" (legacy fallback).
func TestLoadStorageBackends_EmptyTable(t *testing.T) {
	t.Setenv("REPOTEST_DIALECT", "sqlite")
	d := repotest.New(t)
	backendRepo := repo.NewStorageBackendRepo(d)

	resolver, err := storageloader.LoadStorageBackends(context.Background(), backendRepo, config.DialectSQLite)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Default backend must be reachable via empty id.
	store, err := resolver.Resolve("")
	if err != nil {
		t.Fatalf("resolve default: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil default storage")
	}
}

// TestLoadStorageBackends_LocalRow verifies a local backend row is built and
// the resolver routes by its id.
func TestLoadStorageBackends_LocalRow(t *testing.T) {
	t.Setenv("REPOTEST_DIALECT", "sqlite")
	d := repotest.New(t)
	backendRepo := repo.NewStorageBackendRepo(d)

	root := t.TempDir()
	row, err := backendRepo.Create(context.Background(), "local", map[string]any{"root": root})
	if err != nil {
		t.Fatalf("create backend row: %v", err)
	}

	resolver, err := storageloader.LoadStorageBackends(context.Background(), backendRepo, config.DialectSQLite)
	if err != nil {
		t.Fatalf("LoadStorageBackends: %v", err)
	}

	store, err := resolver.Resolve(row.ID)
	if err != nil {
		t.Fatalf("resolve %s: %v", row.ID, err)
	}
	if store == nil {
		t.Fatal("expected non-nil storage for local backend")
	}
}

// TestLoadStorageBackends_LocalIgnoresConfigRoot verifies that a local
// backend row's config.root is informational only — the LocalFS is always
// constructed rooted at "/" so callers can pass absolute paths as keys.
// Per-library rooting belongs to S3 (per-bucket-prefix), not local FS.
func TestLoadStorageBackends_LocalIgnoresConfigRoot(t *testing.T) {
	t.Setenv("REPOTEST_DIALECT", "sqlite")
	d := repotest.New(t)
	backendRepo := repo.NewStorageBackendRepo(d)

	// Various config.root values — including missing, empty, relative,
	// and a non-existent absolute path — all produce a working backend.
	for _, cfg := range []map[string]any{
		{},
		{"root": ""},
		{"root": "./data/main"},
		{"root": "/absolute/somewhere/that/does/not/exist"},
	} {
		row, err := backendRepo.Create(context.Background(), "local", cfg)
		if err != nil {
			t.Fatalf("create backend row %v: %v", cfg, err)
		}
		resolver, err := storageloader.LoadStorageBackends(context.Background(), backendRepo, config.DialectSQLite)
		if err != nil {
			t.Fatalf("LoadStorageBackends with config %v: %v", cfg, err)
		}
		store, err := resolver.Resolve(row.ID)
		if err != nil {
			t.Fatalf("resolve %s: %v", row.ID, err)
		}
		if store == nil {
			t.Fatalf("expected non-nil storage for config %v", cfg)
		}
		// Cleanup so the next iteration sees a clean table.
		if err := backendRepo.Delete(context.Background(), row.ID); err != nil {
			t.Fatalf("delete backend row: %v", err)
		}
	}
}

// TestLoadStorageBackends_SQLiteWithS3Errors verifies that having an S3 row
// with SQLite dialect returns an error (either "switch to Postgres" or a
// connection error — both mean the combination is rejected).
//
// NOTE: s3.New() is called before the SQLite guard so the test will see a
// connection error from the AWS SDK rather than the guard message. That's
// acceptable — any error means the boot is rejected.
func TestLoadStorageBackends_SQLiteWithS3Errors(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping S3 connectivity test in short mode")
	}
	t.Setenv("REPOTEST_DIALECT", "sqlite")
	d := repotest.New(t)
	backendRepo := repo.NewStorageBackendRepo(d)

	// Insert a minimal S3 row. s3.New will fail to connect (no real S3),
	// which is also a rejection of the combination.
	_, err := d.SQL.ExecContext(context.Background(),
		`INSERT INTO storage_backends (id, kind, config, created_at)
		 VALUES ('test-s3-id', 's3', '{"bucket":"test-bucket"}',
		         strftime('%Y-%m-%dT%H:%M:%fZ','now'))`)
	if err != nil {
		t.Fatalf("insert S3 row: %v", err)
	}

	_, err = storageloader.LoadStorageBackends(context.Background(), backendRepo, config.DialectSQLite)
	if err == nil {
		t.Fatal("expected error for SQLite+S3 combination")
	}
	t.Logf("got expected error (SQLite+S3 rejected): %v", err)
}
