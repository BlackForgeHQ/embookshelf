// SPDX-License-Identifier: AGPL-3.0-or-later

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
	d := repotest.New(t)
	backendRepo := repo.NewStorageBackendRepo(d)

	resolver, err := storageloader.LoadStorageBackends(context.Background(), backendRepo)
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
	d := repotest.New(t)
	backendRepo := repo.NewStorageBackendRepo(d)

	root := t.TempDir()
	row, err := backendRepo.Create(context.Background(), "local", map[string]any{"root": root})
	if err != nil {
		t.Fatalf("create backend row: %v", err)
	}

	resolver, err := storageloader.LoadStorageBackends(context.Background(), backendRepo)
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
		resolver, err := storageloader.LoadStorageBackends(context.Background(), backendRepo)
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

// TestReconcileSharedS3 verifies env-derived S3 fields are pushed into
// pre-existing rows while preserving per-library prefix.
func TestReconcileSharedS3(t *testing.T) {
	d := repotest.New(t)
	backendRepo := repo.NewStorageBackendRepo(d)
	ctx := context.Background()

	row, err := backendRepo.Create(ctx, "s3", map[string]any{
		"bucket":            "old-bucket",
		"region":            "us-east-1",
		"endpoint":          "fsn1.your-objectstorage.com",
		"prefix":            "libraries/main/",
		"access_key_id":     "OLDKEY",
		"secret_access_key": "OLDSECRET",
		"force_path_style":  false,
	})
	if err != nil {
		t.Fatalf("create row: %v", err)
	}

	shared := config.SharedS3Config{
		Bucket:          "new-bucket",
		Region:          "eu-central-1",
		Endpoint:        "https://fsn1.your-objectstorage.com",
		AccessKeyID:     "NEWKEY",
		SecretAccessKey: "NEWSECRET",
		ForcePathStyle:  true,
	}
	n, err := storageloader.ReconcileSharedS3(ctx, backendRepo, shared)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 1 {
		t.Errorf("updated=%d want 1", n)
	}
	got, err := backendRepo.Get(ctx, row.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Config["bucket"] != "new-bucket" {
		t.Errorf("bucket=%v want new-bucket", got.Config["bucket"])
	}
	if got.Config["endpoint"] != "https://fsn1.your-objectstorage.com" {
		t.Errorf("endpoint=%v want scheme-prefixed", got.Config["endpoint"])
	}
	if got.Config["prefix"] != "libraries/main/" {
		t.Errorf("prefix=%v want libraries/main/ (must be preserved)", got.Config["prefix"])
	}
	if got.Config["access_key_id"] != "NEWKEY" {
		t.Errorf("access_key_id=%v want NEWKEY", got.Config["access_key_id"])
	}
	if got.Config["force_path_style"] != true {
		t.Errorf("force_path_style=%v want true", got.Config["force_path_style"])
	}

	// Second reconcile is a no-op when nothing changed.
	n2, err := storageloader.ReconcileSharedS3(ctx, backendRepo, shared)
	if err != nil {
		t.Fatalf("reconcile-2: %v", err)
	}
	if n2 != 0 {
		t.Errorf("second pass updated=%d want 0", n2)
	}
}

// TestReconcileSharedS3_Unconfigured ensures empty env doesn't wipe rows.
func TestReconcileSharedS3_Unconfigured(t *testing.T) {
	d := repotest.New(t)
	backendRepo := repo.NewStorageBackendRepo(d)
	ctx := context.Background()

	row, err := backendRepo.Create(ctx, "s3", map[string]any{
		"bucket":            "kept",
		"endpoint":          "kept.example.com",
		"prefix":            "libraries/x/",
		"access_key_id":     "K",
		"secret_access_key": "S",
	})
	if err != nil {
		t.Fatalf("create row: %v", err)
	}

	n, err := storageloader.ReconcileSharedS3(ctx, backendRepo, config.SharedS3Config{})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 0 {
		t.Errorf("updated=%d want 0 when env unconfigured", n)
	}
	got, err := backendRepo.Get(ctx, row.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Config["bucket"] != "kept" {
		t.Errorf("bucket=%v want kept (must not be wiped)", got.Config["bucket"])
	}
}
