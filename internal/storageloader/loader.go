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
// Refuses to start when any S3 backend is present with a SQLite
// database, per spec §10 (multi-instance S3 requires Postgres for
// reliable job coordination).
//
// When the storage_backends table is empty (pre-Plan-B deployments),
// the resolver's default falls back to a LocalFS rooted at "/" so
// existing single-library installs keep booting unchanged.
func LoadStorageBackends(ctx context.Context, backendRepo *repo.StorageBackendRepo, dialect config.Dialect) (storage.Resolver, error) {
	rows, err := backendRepo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list storage backends: %w", err)
	}

	backends := make(map[string]storage.Storage, len(rows))
	var defaultStore storage.Storage
	var hasS3 bool

	for _, row := range rows {
		store, err := buildBackend(ctx, row)
		if err != nil {
			return nil, fmt.Errorf("storage_backends/%s (%s): %w", row.ID, row.Kind, err)
		}
		backends[row.ID] = store
		if defaultStore == nil {
			defaultStore = store
		}
		if row.Kind == "s3" {
			hasS3 = true
		}
	}

	// Spec §10: SQLite + S3 is an unsupported combination. S3 requires
	// Postgres for reliable distributed coordination (River queue).
	if hasS3 && dialect == config.DialectSQLite {
		return nil, errors.New("storage: SQLite cannot host S3 backends — switch to Postgres (spec §10)")
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
		root, ok := row.Config["root"].(string)
		if !ok || root == "" {
			return nil, fmt.Errorf("missing or invalid config.root")
		}
		ls, err := local.New(root)
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
