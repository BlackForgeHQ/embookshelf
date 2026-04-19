package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/coverstore"
	"github.com/blackforge/embookshelf/internal/handler"
	"github.com/blackforge/embookshelf/internal/ingest"
	"github.com/blackforge/embookshelf/internal/migrator"
	"github.com/blackforge/embookshelf/internal/provider"
	"github.com/blackforge/embookshelf/internal/queue"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/sse"
	"github.com/blackforge/embookshelf/internal/staticfs"
)

func main() {
	_ = godotenv.Load()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := newPool(ctx, cfg)
	if err != nil {
		slog.Error("db connect", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Apply app schema migrations before any repo runs queries. River's own
	// migrations are applied separately inside queue.New().
	if cfg.MigrateOnStart {
		if err := runAppMigrations(ctx, pool); err != nil {
			slog.Error("migrate", "err", err)
			os.Exit(1)
		}
	}

	// Repositories.
	libRepo := repo.NewLibraryRepo(pool)
	shelfRepo := repo.NewShelfRepo(pool)
	userRepo := repo.NewUserRepo(pool)
	sessionRepo := repo.NewSessionRepo(pool)
	bdropRepo := repo.NewBookDropRepo(pool)
	progressRepo := repo.NewProgressRepo(pool)
	libPathRepo := repo.NewLibraryPathRepo(pool)
	annotationRepo := repo.NewAnnotationRepo(pool)
	statsRepo := repo.NewStatsRepo(pool)
	readingSessionRepo := repo.NewReadingSessionRepo(pool)
	deviceRepo := repo.NewDeviceRepo(pool)

	// SSE hub — shared between services that broadcast and the handler that serves /events.
	hub := sse.NewHub()

	// Cover image store (files on disk under ${DATA_PATH}/covers/).
	covers := coverstore.New(filepath.Join(cfg.DataPath, "covers"))

	// Services.
	libSvc := service.NewLibraryService(libRepo)
	shelfSvc := service.NewShelfService(shelfRepo)
	authSvc := service.NewAuthService(userRepo, sessionRepo)
	bdropSvc := service.NewBookDropService(bdropRepo, libRepo, covers, hub)
	progressSvc := service.NewProgressService(progressRepo, readingSessionRepo)
	libPathSvc := service.NewLibraryPathService(libPathRepo)
	annotationSvc := service.NewAnnotationService(annotationRepo)
	statsSvc := service.NewStatsService(statsRepo)
	readingStatsSvc := service.NewReadingSessionService(readingSessionRepo)
	// Resolve provider list from env. Unknown ids are logged and
	// skipped so a typo never crashes startup; we still want the
	// server up with whatever subset is recognized.
	providers := make([]provider.Provider, 0, len(cfg.EnrichmentProviders))
	for _, name := range cfg.EnrichmentProviders {
		p := provider.Build(provider.Source(name))
		if p == nil {
			slog.Warn("unknown enrichment provider — skipping", "name", name)
			continue
		}
		providers = append(providers, p)
	}
	if len(providers) == 0 {
		slog.Warn("no enrichment providers configured — metadata search will return empty")
	}
	enrichSvc := service.NewEnrichmentService(providers, libRepo, covers)
	deviceSvc := service.NewDeviceService(
		deviceRepo, libRepo,
		service.NewRemarkableDriver(),
	)

	if n, err := authSvc.PurgeExpiredSessions(ctx); err != nil {
		slog.Warn("purge sessions", "err", err)
	} else if n > 0 {
		slog.Info("purged expired sessions", "count", n)
	}

	// Background queue (river). Runs its own migrations, then starts workers.
	q, err := queue.New(ctx, pool, bdropSvc, libPathSvc, libSvc)
	if err != nil {
		slog.Error("queue", "err", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := q.Stop(shutdownCtx); err != nil {
			slog.Warn("queue stop", "err", err)
		}
	}()

	// Requeue anything still mid-flight from a previous process.
	ingest.DiscoverOnStartup(ctx, bdropRepo, q)

	// File watcher goroutine.
	watcher := &ingest.Watcher{
		Path:     cfg.BookDropPath,
		Interval: cfg.BookDropInterval,
		Svc:      bdropSvc,
		Queue:    q,
	}
	go watcher.Run(ctx)

	// HTTP.
	h := handler.New(handler.Deps{
		Cfg:         cfg,
		Static:      staticfs.FS,
		Lib:         libSvc,
		Shelf:       shelfSvc,
		Auth:        authSvc,
		BookDrop:    bdropSvc,
		Progress:    progressSvc,
		Enrich:      enrichSvc,
		LibPath:     libPathSvc,
		Annotations: annotationSvc,
		Stats:        statsSvc,
		ReadingStats: readingStatsSvc,
		Devices:      deviceSvc,
		Covers:       covers,
		Hub:         hub,
		Queue:       q,
	})

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           h.Engine(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("server starting", "addr", srv.Addr, "diskMode", cfg.DiskType, "bookdrop", cfg.BookDropPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "err", err)
	}
}

// runAppMigrations applies every pending schema migration using the embedded
// migration files. Idempotent — a no-op when the DB is already up-to-date.
func runAppMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	m, err := migrator.New(migrator.FS, migrator.Subpath, pool)
	if err != nil {
		return err
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			slog.Warn("migrate source close", "err", srcErr)
		}
		if dbErr != nil {
			slog.Warn("migrate db close", "err", dbErr)
		}
	}()
	v, dirty, _ := m.Version()
	if err := migrator.Up(m); err != nil {
		return err
	}
	newV, _, _ := m.Version()
	if newV != v {
		slog.Info("migrations applied", "from", v, "to", newV, "dirty", dirty)
	}
	_ = ctx
	return nil
}

func newPool(ctx context.Context, cfg config.Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	poolCfg.MaxConns = cfg.DatabaseMaxConns
	poolCfg.MinConns = cfg.DatabaseMinConns

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
