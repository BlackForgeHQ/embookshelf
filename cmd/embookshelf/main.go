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

	// Repositories.
	libRepo := repo.NewLibraryRepo(pool)
	shelfRepo := repo.NewShelfRepo(pool)
	userRepo := repo.NewUserRepo(pool)
	sessionRepo := repo.NewSessionRepo(pool)
	bdropRepo := repo.NewBookDropRepo(pool)
	progressRepo := repo.NewProgressRepo(pool)
	libPathRepo := repo.NewLibraryPathRepo(pool)

	// SSE hub — shared between services that broadcast and the handler that serves /events.
	hub := sse.NewHub()

	// Cover image store (files on disk under ${DATA_PATH}/covers/).
	covers := coverstore.New(filepath.Join(cfg.DataPath, "covers"))

	// Services.
	libSvc := service.NewLibraryService(libRepo)
	shelfSvc := service.NewShelfService(shelfRepo)
	authSvc := service.NewAuthService(userRepo, sessionRepo)
	bdropSvc := service.NewBookDropService(bdropRepo, libRepo, covers, hub)
	progressSvc := service.NewProgressService(progressRepo)
	libPathSvc := service.NewLibraryPathService(libPathRepo)
	enrichSvc := service.NewEnrichmentService(
		[]provider.Provider{provider.NewGoogleBooks(), provider.NewOpenLibrary()},
		libRepo,
		covers,
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
		Cfg:      cfg,
		Static:   staticfs.FS,
		Lib:      libSvc,
		Shelf:    shelfSvc,
		Auth:     authSvc,
		BookDrop: bdropSvc,
		Progress: progressSvc,
		Enrich:   enrichSvc,
		LibPath:  libPathSvc,
		Covers:   covers,
		Hub:      hub,
		Queue:    q,
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
