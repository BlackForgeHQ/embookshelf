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

	"github.com/joho/godotenv"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/coverstore"
	"github.com/blackforge/embookshelf/internal/crypto"
	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/handler"
	"github.com/blackforge/embookshelf/internal/ingest"
	"github.com/blackforge/embookshelf/internal/migrator"
	"github.com/blackforge/embookshelf/internal/provider"
	"github.com/blackforge/embookshelf/internal/queue"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/sse"
	"github.com/blackforge/embookshelf/internal/staticfs"
	"github.com/blackforge/embookshelf/internal/storage/local"
	"github.com/blackforge/embookshelf/internal/task"
	"github.com/blackforge/embookshelf/internal/telemetry"
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

	// OpenTelemetry — must be set up before services start so spans and
	// metrics from DB, queue, and HTTP paths end up in the pipeline.
	// No-op when OTEL_ENABLED is false.
	otelShutdown, err := telemetry.Setup(ctx, telemetry.Config{
		Enabled:     cfg.OTELEnabled,
		ServiceName: cfg.OTELServiceName,
		Endpoint:    cfg.OTELEndpoint,
		Protocol:    cfg.OTELProtocol,
		Insecure:    cfg.OTELInsecure,
		SampleRatio: cfg.OTELSampleRatio,
	})
	if err != nil {
		slog.Error("telemetry setup", "err", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := otelShutdown(shutdownCtx); err != nil {
			slog.Warn("telemetry shutdown", "err", err)
		}
	}()
	if cfg.OTELEnabled {
		slog.Info("OpenTelemetry enabled", "endpoint", cfg.OTELEndpoint, "protocol", cfg.OTELProtocol, "service", cfg.OTELServiceName)
	}

	dbh, err := db.Open(ctx, cfg)
	if err != nil {
		slog.Error("db connect", "err", err)
		os.Exit(1)
	}
	defer func() { _ = dbh.Close() }()

	// Apply app schema migrations before any repo runs queries. River's own
	// migrations are applied separately inside queue.New().
	if cfg.MigrateOnStart {
		if err := runAppMigrations(ctx, dbh); err != nil {
			slog.Error("migrate", "err", err)
			os.Exit(1)
		}
		if err := migrator.BackfillStorageV2(ctx, dbh); err != nil {
			slog.Error("storage_v2 backfill", "err", err)
			os.Exit(1)
		}
	}

	fileStorage, err := local.New("/")
	if err != nil {
		slog.Error("storage init", "err", err)
		os.Exit(1)
	}

	// Repositories.
	libRepo := repo.NewLibraryRepo(dbh)
	shelfRepo := repo.NewShelfRepo(dbh)
	userRepo := repo.NewUserRepo(dbh)
	sessionRepo := repo.NewSessionRepo(dbh)
	bdropRepo := repo.NewBookDropRepo(dbh)
	progressRepo := repo.NewProgressRepo(dbh)
	annotationRepo := repo.NewAnnotationRepo(dbh)
	statsRepo := repo.NewStatsRepo(dbh)
	readingSessionRepo := repo.NewReadingSessionRepo(dbh)
	deviceRepo := repo.NewDeviceRepo(dbh)
	appSettingsRepo := repo.NewAppSettingsRepo(dbh)
	fileRepo := repo.NewFileRepo(dbh)

	// SSE hub — shared between services that broadcast and the handler that serves /events.
	hub := sse.NewHub()

	// Cover image store (files on disk under ${DATA_PATH}/covers/).
	covers := coverstore.New(filepath.Join(cfg.DataPath, "covers"))

	// Services.
	libSvc := service.NewLibraryService(libRepo)
	shelfSvc := service.NewShelfService(shelfRepo)
	searchSvc := service.NewSearchService(libRepo, shelfRepo)
	authSvc := service.NewAuthService(userRepo, sessionRepo, hub)
	bdropSvc := service.NewBookDropService(bdropRepo, libRepo, appSettingsRepo, covers, hub, fileRepo)
	progressSvc := service.NewProgressService(progressRepo, readingSessionRepo)
	annotationSvc := service.NewAnnotationService(annotationRepo)
	statsSvc := service.NewStatsService(statsRepo)
	readingStatsSvc := service.NewReadingSessionService(readingSessionRepo)
	// Build every provider in the static catalog so the service can
	// dispatch to any of them at runtime. Which subset is actually
	// queried per request is decided by provider_settings (DB).
	providers := make([]provider.Provider, 0, len(provider.Catalog))
	for _, c := range provider.Catalog {
		p := provider.Build(c.ID)
		if p == nil {
			slog.Warn("catalog provider has no Build() — skipping", "id", c.ID)
			continue
		}
		providers = append(providers, p)
	}
	providerSettingsRepo := repo.NewProviderSettingsRepo(dbh)
	// Seed provider_settings on first boot using catalog defaults.
	// ON CONFLICT DO NOTHING means subsequent restarts leave admin
	// toggles alone — the DB is authoritative after the initial seed.
	defaults := make(map[string]bool, len(provider.Catalog))
	for _, c := range provider.Catalog {
		defaults[string(c.ID)] = c.DefaultEnabled
	}
	if err := providerSettingsRepo.SeedIfAbsent(ctx, defaults); err != nil {
		slog.Warn("seed provider settings", "err", err)
	}
	// Build the at-rest cipher for provider secrets. Unset KEK falls
	// back to passthrough with a loud warning — secrets on disk stay
	// plaintext but the server still boots. A malformed key is fatal
	// so admins don't think encryption is on when it isn't.
	var secretCipher crypto.Cipher
	if cfg.SecretKey != "" {
		ac, err := crypto.NewAESGCM(cfg.SecretKey)
		if err != nil {
			slog.Error("EMBOOKSHELF_SECRET_KEY invalid — refusing to boot", "err", err)
			os.Exit(1)
		}
		secretCipher = ac
		slog.Info("secrets encryption enabled (AES-256-GCM)")
	} else {
		secretCipher = crypto.Noop{}
		slog.Warn("EMBOOKSHELF_SECRET_KEY unset — provider secrets stored in plaintext. " +
			"Set a base64-encoded 32-byte key for at-rest encryption.")
	}
	enrichSvc := service.NewEnrichmentService(providers, providerSettingsRepo, libRepo, covers, secretCipher)
	// Push stored per-provider config (API keys, language, …) into the
	// running provider instances. Failure here is non-fatal — providers
	// fall back to their no-config defaults.
	if err := enrichSvc.LoadConfigs(ctx); err != nil {
		slog.Warn("load provider configs", "err", err)
	}
	deviceSvc := service.NewDeviceService(
		deviceRepo, libRepo,
		service.NewRemarkableDriver(),
	)

	// OIDC — settings live in app_settings now so three providers
	// (Google, GitHub, generic OIDC) can be toggled independently
	// without a restart. Seed empty rows on first boot, and back-fill
	// the generic-OIDC row from the legacy OIDC_* env vars when the
	// DB is still empty (migration aid for existing deployments).
	if err := appSettingsRepo.SeedOIDCIfAbsent(ctx); err != nil {
		slog.Warn("seed oidc settings", "err", err)
	}
	if err := appSettingsRepo.SeedDefaultNamingPatternIfAbsent(ctx); err != nil {
		slog.Warn("seed default naming pattern", "err", err)
	}
	if cfg.HasOIDCEnvSeed() {
		existing, err := appSettingsRepo.GetGenericOIDC(ctx)
		if err == nil && existing.IssuerURI == "" {
			seed := repo.DefaultGenericOIDCConfig()
			seed.Enabled = true
			seed.IssuerURI = cfg.OIDCIssuerURL
			seed.ClientID = cfg.OIDCClientID
			seed.ClientSecret = cfg.OIDCClientSecret
			if err := appSettingsRepo.SetGenericOIDC(ctx, seed); err != nil {
				slog.Warn("seed oidc provider from env", "err", err)
			} else {
				slog.Info("seeded generic OIDC provider from env", "issuer", cfg.OIDCIssuerURL)
			}
		}
	}
	oidcSvc := service.NewOIDCService(appSettingsRepo, userRepo, sessionRepo, cfg.AppURL)

	if n, err := authSvc.PurgeExpiredSessions(ctx); err != nil {
		slog.Warn("purge sessions", "err", err)
	} else if n > 0 {
		slog.Info("purged expired sessions", "count", n)
	}

	// Background queue. PG → River; SQLite → polling worker (queue.New dispatches by dialect).
	q, err := queue.New(ctx, dbh, bdropSvc, libSvc, fileStorage, fileRepo)
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

	// Boot-time files backfill: hash any files rows that are still missing a
	// content_hash. Runs in the background so it doesn't block startup.
	// 1-hour timeout is generous; real deployments have hundreds of files at most.
	go func() {
		slog.Info("files backfill starting")
		backfillCtx, cancel := context.WithTimeout(context.Background(), 1*time.Hour)
		defer cancel()
		if err := task.RunFilesBackfill(backfillCtx, task.FilesBackfillDeps{
			Files:     fileRepo,
			Libraries: libRepo,
			Backends:  repo.NewStorageBackendRepo(dbh),
			Storage:   fileStorage,
		}); err != nil {
			slog.Warn("files backfill", "err", err)
		}
	}()

	// Boot-time covers backfill: migrate legacy book-id-keyed covers to the
	// hash-keyed path (covers/<hash[0:2]>/<hash>.<ext>). Idempotent and
	// best-effort — errors per-book are logged and retried on next boot.
	go func() {
		backfillCoversCtx, cancel := context.WithTimeout(context.Background(), 1*time.Hour)
		defer cancel()
		if err := task.RunCoversBackfill(backfillCoversCtx, task.CoversBackfillDeps{
			Library: libRepo,
			Covers:  covers,
		}); err != nil {
			slog.Warn("covers backfill", "err", err)
		}
	}()

	// Missing-files purge sweeper: deletes files rows whose missing_since
	// is older than 24h. Runs hourly until the application shuts down.
	go task.LoopMissingPurge(ctx, fileRepo, time.Hour)

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
		Cfg:          cfg,
		Static:       staticfs.FS,
		Lib:          libSvc,
		Shelf:        shelfSvc,
		Auth:         authSvc,
		BookDrop:     bdropSvc,
		Progress:     progressSvc,
		Enrich:       enrichSvc,
		Annotations:  annotationSvc,
		Stats:        statsSvc,
		ReadingStats: readingStatsSvc,
		Devices:      deviceSvc,
		OIDC:         oidcSvc,
		Search:       searchSvc,
		AppSettings:  appSettingsRepo,
		Covers:       covers,
		Hub:          hub,
		Queue:        q,
	})

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           h.Engine(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Track whether ListenAndServe died on its own (bind error, etc.) so
	// we can propagate a non-zero exit — otherwise air sees exit 0 and
	// won't flag the failure, and the operator only learns the port is
	// taken from a silent stop.
	var serveErr error
	go func() {
		slog.Info("server starting", "addr", srv.Addr, "bookdrop", cfg.BookDropPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server failed", "err", err)
			serveErr = err
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

	if serveErr != nil {
		os.Exit(1)
	}
}

// runAppMigrations applies every pending schema migration using the embedded
// migration files. Idempotent — a no-op when the DB is already up-to-date.
//
// It opens a dedicated *sql.DB for migrations and closes it at the end via
// m.Close() (the golang-migrate postgres driver closes the sql.DB it owns).
// This keeps the shared dbh.SQL alive for the rest of the application.
func runAppMigrations(ctx context.Context, d *db.DB) error {
	// Open a short-lived, dedicated connection for the migrator so that
	// m.Close() (which golang-migrate's Postgres driver calls sql.DB.Close on)
	// does not close the shared application pool.
	migDB, err := d.OpenMigrationDB()
	if err != nil {
		return fmt.Errorf("migration db: %w", err)
	}

	m, err := migrator.New(d.Dialect, migDB)
	if err != nil {
		// migDB not yet owned by migrator — close it ourselves.
		_ = migDB.Close()
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
