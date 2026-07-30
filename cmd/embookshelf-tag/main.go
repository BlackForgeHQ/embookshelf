// SPDX-License-Identifier: AGPL-3.0-or-later

// Command embookshelf-tag walks every book in the configured libraries,
// classifies it into hot/warm/cold tiers based on the latest
// user_book_progress.last_read_at (max across all users), and writes the
// tier tag onto the corresponding S3 object via PutObjectTagging.
//
// Usage: embookshelf-tag [-dry-run]
//
// The binary reads the same DATABASE_URL environment variable (or .env
// file) as the main server and requires no additional configuration.
// When no S3 backends are present it logs a summary of zero and exits
// cleanly — it is safe to run on local-only deployments.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/joho/godotenv"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/storage"
	s3backend "github.com/blackforge/embookshelf/internal/storage/s3"
	"github.com/blackforge/embookshelf/internal/storageloader"
	"github.com/blackforge/embookshelf/internal/tagging"
)

func main() {
	var dryRun bool
	flag.BoolVar(&dryRun, "dry-run", false, "log decisions but don't call PutObjectTagging")
	flag.Parse()

	_ = godotenv.Load()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		fail("config", err)
	}

	// No AllowSQLite: this reads a live library, so it is covered by the
	// ADR-0023 refusal db.Open applies to every caller that does not name
	// the opt-in.
	dbh, err := db.Open(ctx, cfg)
	if err != nil {
		fail("db", err)
	}
	defer func() { _ = dbh.Close() }()

	libRepo := repo.NewLibraryRepo(dbh)
	backendRepo := repo.NewStorageBackendRepo(dbh)
	fileRepo := repo.NewFileRepo(dbh)
	progressRepo := repo.NewProgressRepo(dbh)

	resolver, err := bootStorage(ctx, backendRepo, cfg.SharedS3)
	if err != nil {
		fail("storage", err)
	}

	libs, err := libRepo.List(ctx)
	if err != nil {
		fail("list libraries", err)
	}

	now := time.Now()
	tagged := 0
	skipped := 0

	for _, lib := range libs {
		if lib.BackendID == nil {
			slog.Debug("library has no backend — skipping", "library", lib.Name)
			continue
		}

		backend, err := resolver.Resolve(*lib.BackendID)
		if err != nil {
			slog.Warn("resolve backend", "library", lib.Name, "backend_id", *lib.BackendID, "err", err)
			continue
		}

		s3b, ok := backend.(*s3backend.Backend)
		if !ok {
			// Not an S3 backend (e.g. local) — tier tagging not applicable.
			slog.Debug("backend is not S3 — skipping", "library", lib.Name)
			continue
		}

		bucket := s3b.Bucket()
		prefix := s3b.Prefix()

		files, err := fileRepo.ListByLibrary(ctx, lib.ID)
		if err != nil {
			slog.Warn("list files", "library", lib.Name, "err", err)
			continue
		}

		for _, f := range files {
			var lastRead time.Time
			if f.BookID != "" {
				lastRead, _ = progressRepo.LatestForBook(ctx, f.BookID)
			}

			tier := tagging.Classify(now, lastRead)

			if dryRun {
				slog.Info("dry-run",
					"library", lib.Name,
					"book_id", f.BookID,
					"key", prefix+f.Location,
					"tier", tier,
				)
				tagged++
				continue
			}

			if err := tagging.Apply(ctx, s3b.Client(), bucket, prefix+f.Location, tier); err != nil {
				slog.Warn("tag failed",
					"library", lib.Name,
					"book_id", f.BookID,
					"key", prefix+f.Location,
					"err", err,
				)
				skipped++
				continue
			}

			slog.Debug("tagged",
				"library", lib.Name,
				"book_id", f.BookID,
				"key", prefix+f.Location,
				"tier", tier,
			)
			tagged++
		}
	}

	slog.Info("tagging done",
		"tagged", tagged,
		"skipped", skipped,
		"libraries", len(libs),
		"dry_run", dryRun,
	)
}

// bootStorage brings the storage_backends rows in line with the
// EMBOOKSHELF_S3_* environment and only then builds the resolver from
// them — the same order app.Build uses, and for the same reason.
//
// The order is the point, not a nicety. A kind=s3 row records the bucket,
// endpoint and credentials it was created with; when the deployment's env
// moves on, the row is stale until something reconciles it. This binary
// used to load the rows as written, so a run after a bucket or key
// rotation built its S3 clients from the old values and wrote tier tags
// against the wrong bucket (or failed to authenticate at all) — exactly
// the staleness ReconcileSharedS3 exists to prevent.
func bootStorage(ctx context.Context, backendRepo *repo.StorageBackendRepo, shared config.SharedS3Config) (storage.Resolver, error) {
	n, err := storageloader.ReconcileSharedS3(ctx, backendRepo, shared)
	if err != nil {
		return nil, fmt.Errorf("reconcile shared s3 backends: %w", err)
	}
	if n > 0 {
		slog.Info("storage backends reconciled from env", "updated", n)
	}
	return storageloader.LoadStorageBackends(ctx, backendRepo)
}

func fail(stage string, err error) {
	fmt.Fprintf(os.Stderr, "embookshelf-tag: %s: %v\n", stage, err)
	os.Exit(1)
}
