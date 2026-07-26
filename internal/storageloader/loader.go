// SPDX-License-Identifier: AGPL-3.0-or-later

// Package storageloader provides LoadStorageBackends, the boot-time
// helper that reads storage_backends rows from the database and
// constructs the appropriate Storage implementation for each. It lives
// in its own package (not internal/config) to avoid the import cycle
// that arises because db imports config.
package storageloader

import (
	"context"
	"errors"
	"fmt"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
	s3backend "github.com/blackforge/embookshelf/internal/storage/s3"
)

// LoadStorageBackends reads storage_backends rows from the database and
// constructs a Storage instance per row. Returns a Resolver that maps
// backend IDs to Storage instances; the first backend found is also the
// default (used by libraries without a backend_id assignment).
//
// When the storage_backends table is empty (pre-Plan-B deployments),
// the resolver's default falls back to a LocalFS rooted at "/" so
// existing single-library installs keep booting unchanged.
func LoadStorageBackends(ctx context.Context, backendRepo *repo.StorageBackendRepo) (storage.Resolver, error) {
	rows, err := backendRepo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list storage backends: %w", err)
	}

	backends := make(map[string]storage.Storage, len(rows))
	var defaultStore storage.Storage

	for _, row := range rows {
		store, err := buildBackend(ctx, row)
		if err != nil {
			return nil, fmt.Errorf("storage_backends/%s (%s): %w", row.ID, row.Kind, err)
		}
		backends[row.ID] = store
		if defaultStore == nil {
			defaultStore = store
		}
	}

	// Legacy fallback: single-library installs that predate the Plan-B
	// storage_backends table keep booting with LocalFS at "/".
	if defaultStore == nil {
		fallback, err := local.New("/")
		if err != nil {
			return nil, fmt.Errorf("storage: default fallback: %w", err)
		}
		defaultStore = fallback
	}

	return &storage.MapResolver{Default: defaultStore, Backends: backends}, nil
}

// buildBackend constructs a Storage for one storage_backends row.
func buildBackend(ctx context.Context, row model.StorageBackend) (storage.Storage, error) {
	switch row.Kind {
	case "local":
		// LocalFS is always rooted at "/". The library's actual root
		// path lives in libraries.root; callers (scan worker, bookdrop
		// ingest, file handler) pass absolute paths as keys, which the
		// "/"-rooted LocalFS resolves correctly. Per-library rooting
		// for LocalFS was an over-application of Plan F's S3 bucket
		// model — S3 needs per-bucket-prefix rooting, but the local
		// filesystem doesn't, and rooting per-library broke every
		// caller that passes absolute paths.
		//
		// row.Config["root"] is informational only for the local kind.
		ls, err := local.New("/")
		if err != nil {
			return nil, err
		}
		return ls, nil

	case "s3":
		cfg, err := s3ConfigFromRow(row)
		if err != nil {
			return nil, err
		}
		sb, err := s3backend.New(ctx, cfg)
		if err != nil {
			return nil, err
		}
		return sb, nil

	default:
		return nil, fmt.Errorf("unknown kind %q", row.Kind)
	}
}

// ReconcileSharedS3 walks every kind=s3 storage_backends row and rewrites
// the bucket-level connection fields (bucket, region, endpoint,
// access_key_id, secret_access_key, force_path_style) from the
// EMBOOKSHELF_S3_* env values. The per-library `prefix` is preserved so
// each backend keeps pointing at its own slice of the bucket.
//
// Skips when SharedS3 isn't configured (no env values to push) so empty
// env doesn't blow away a manually-edited row. A row whose config
// already matches is left untouched to avoid pointless writes.
//
// Called once at boot, before LoadStorageBackends, so the loader sees
// the fresh values.
func ReconcileSharedS3(ctx context.Context, backendRepo *repo.StorageBackendRepo, shared config.SharedS3Config) (int, error) {
	if !shared.Configured() {
		return 0, nil
	}
	rows, err := backendRepo.List(ctx)
	if err != nil {
		return 0, fmt.Errorf("list storage backends: %w", err)
	}
	updated := 0
	for _, row := range rows {
		if row.Kind != "s3" {
			continue
		}
		prefix, _ := row.Config["prefix"].(string)
		desired := map[string]any{
			"bucket":            shared.Bucket,
			"region":            shared.Region,
			"endpoint":          shared.Endpoint,
			"prefix":            prefix,
			"access_key_id":     shared.AccessKeyID,
			"secret_access_key": shared.SecretAccessKey,
			"force_path_style":  shared.ForcePathStyle,
		}
		if configsEqual(row.Config, desired) {
			continue
		}
		if err := backendRepo.UpdateConfig(ctx, row.ID, desired); err != nil {
			return updated, fmt.Errorf("update backend %s: %w", row.ID, err)
		}
		updated++
	}
	return updated, nil
}

// configsEqual compares two backend config maps on the keys
// ReconcileSharedS3 manages. Other keys are ignored — a future caller
// that adds extra config (e.g. SSE algorithm) can extend this.
func configsEqual(a, b map[string]any) bool {
	keys := []string{"bucket", "region", "endpoint", "prefix", "access_key_id", "secret_access_key", "force_path_style"}
	for _, k := range keys {
		if a[k] != b[k] {
			return false
		}
	}
	return true
}

// s3ConfigFromRow extracts S3 connection parameters from the
// storage_backends.config JSONB field (arrived as map[string]any).
func s3ConfigFromRow(row model.StorageBackend) (s3backend.Config, error) {
	cfg := s3backend.Config{}

	cfg.Bucket, _ = row.Config["bucket"].(string)
	cfg.Region, _ = row.Config["region"].(string)
	cfg.Endpoint, _ = row.Config["endpoint"].(string)
	cfg.Prefix, _ = row.Config["prefix"].(string)
	cfg.AccessKeyID, _ = row.Config["access_key_id"].(string)
	cfg.SecretAccessKey, _ = row.Config["secret_access_key"].(string)
	if v, ok := row.Config["force_path_style"].(bool); ok {
		cfg.ForcePathStyle = v
	}

	if cfg.Bucket == "" {
		return cfg, errors.New("missing config.bucket")
	}
	return cfg, nil
}
