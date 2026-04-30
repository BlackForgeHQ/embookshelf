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

	dbh, err := db.Open(ctx, cfg)
	if err != nil {
		fail("db", err)
	}
	defer func() { _ = dbh.Close() }()

	libRepo := repo.NewLibraryRepo(dbh)
	backendRepo := repo.NewStorageBackendRepo(dbh)
	fileRepo := repo.NewFileRepo(dbh)
	progressRepo := repo.NewProgressRepo(dbh)

	resolver, err := storageloader.LoadStorageBackends(ctx, backendRepo, config.Dialect(string(dbh.Dialect)))
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

func fail(stage string, err error) {
	fmt.Fprintf(os.Stderr, "embookshelf-tag: %s: %v\n", stage, err)
	os.Exit(1)
}
