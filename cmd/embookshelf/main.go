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

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/coverstore"
	"github.com/blackforge/embookshelf/internal/crypto"
	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/handler"
	"github.com/blackforge/embookshelf/internal/ingest"
	"github.com/blackforge/embookshelf/internal/migrator"
	"github.com/blackforge/embookshelf/internal/provider"
	"github.com/blackforge/embookshelf/internal/queue"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/sidecar"
	"github.com/blackforge/embookshelf/internal/sse"
	"github.com/blackforge/embookshelf/internal/staticfs"
	s3backend "github.com/blackforge/embookshelf/internal/storage/s3"
	"github.com/blackforge/embookshelf/internal/storageloader"
	"github.com/blackforge/embookshelf/internal/task"
	"github.com/blackforge/embookshelf/internal/telemetry"
)

// Build-time identity. Overridden via
//
//	-ldflags "-X main.version=$VERSION -X main.commit=$COMMIT"
//
// in the Dockerfile and goreleaser. Defaults keep `go run` readable.
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	_ = godotenv.Load()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// Tagged builds run gin in release mode (silences debug logs, skips
	// trusted-proxy warnings). `go run` and dev images keep DebugMode for
	// route logs. Explicit GIN_MODE env still wins via gin's own init.
	if version != "dev" && os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}

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

	bootBackendRepo := repo.NewStorageBackendRepo(dbh)
	if n, err := storageloader.ReconcileSharedS3(ctx, bootBackendRepo, cfg.SharedS3); err != nil {
		slog.Error("reconcile shared s3 backends", "err", err)
		os.Exit(1)
	} else if n > 0 {
		slog.Info("storage backends reconciled from env", "updated", n)
	}

	storageResolver, err := storageloader.LoadStorageBackends(
		ctx,
		bootBackendRepo,
		config.Dialect(string(dbh.Dialect)),
	)
	if err != nil {
		slog.Error("storage backends", "err", err)
		os.Exit(1)
	}

	// Repositories.
	libRepo := repo.NewLibraryRepo(dbh)
	bookRepo := repo.NewBookRepo(dbh)
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
	backendRepo := repo.NewStorageBackendRepo(dbh)
	libSvc := service.NewLibraryService(libRepo, bookRepo, service.LibraryServiceDeps{
		Backends: backendRepo,
		SharedS3: cfg.SharedS3,
		Resolver: storageResolver,
		Dialect:  config.Dialect(string(dbh.Dialect)),
		DataPath: cfg.DataPath,
	})
	shelfSvc := service.NewShelfService(shelfRepo)
	searchSvc := service.NewSearchService(libRepo, bookRepo, shelfRepo)
	authSvc := service.NewAuthService(userRepo, sessionRepo, hub)
	libStore := service.NewLibraryStore(service.LibraryStoreDeps{
		Libs:            libRepo,
		Resolver:        storageResolver,
		NewPlacer:       service.DefaultPlacerBuilder(storageResolver),
		Files:           fileRepo,
		PresignTTL:      cfg.PresignTTL,
		PresignFallback: cfg.PresignFallback,
	})
	bdropSvc := service.NewBookDropService(bdropRepo, libRepo, bookRepo, appSettingsRepo, covers, hub, fileRepo).
		WithLibraryStore(libStore)
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
	enrichSvc := service.NewEnrichmentService(providers, providerSettingsRepo, libRepo, bookRepo, covers, secretCipher)
	// Push stored per-provider config (API keys, language, …) into the
	// running provider instances. Failure here is non-fatal — providers
	// fall back to their no-config defaults.
	if err := enrichSvc.LoadConfigs(ctx); err != nil {
		slog.Warn("load provider configs", "err", err)
	}
	// MetadataWriter coordinates DB → sidecar → file pipeline writes
	// for metadata edits. Wired into LibraryService + EnrichmentService
	// so manual edits and apply-match flows route through it; the
	// auto-enrich background path passes TriggerAutoEnrichment to skip
	// the side-effect steps and only persist the DB row.
	sidecarWriter := sidecar.NewWriter()
	pendingOrphansRepo := repo.NewPendingOrphanRepo(dbh)
	renameGrace := cfg.S3RenameGrace
	if renameGrace <= 0 {
		// ADR-0005: 2× PresignTTL covers any URL the client could
		// still hold. Floor at 1h so very short PresignTTL (manual
		// override) doesn't make the sweeper too aggressive.
		renameGrace = max(2*cfg.PresignTTL, time.Hour)
	}
	metadataWriter := service.NewMetadataWriter(service.MetadataWriterDeps{
		Books:       bookRepo,
		LibStore:    libStore,
		Sidecar:     sidecarWriter,
		Dispatch:    fileproc.DispatchEmbedder,
		Files:       fileRepo,
		Orphans:     pendingOrphansRepo,
		RenameGrace: renameGrace,
	})
	libSvc.WithMetadataWriter(metadataWriter)
	enrichSvc.WithMetadataWriter(metadataWriter)
	deviceSvc := service.NewDeviceService(
		deviceRepo, bookRepo,
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
	identityRepo := repo.NewIdentityRepo(dbh)
	oidcSvc := service.NewOIDCService(appSettingsRepo, userRepo, sessionRepo, identityRepo, cfg.AppURL)

	if n, err := authSvc.PurgeExpiredSessions(ctx); err != nil {
		slog.Warn("purge sessions", "err", err)
	} else if n > 0 {
		slog.Info("purged expired sessions", "count", n)
	}

	// Background queue. PG → River; SQLite → polling worker (queue.New dispatches by dialect).
	scanImportDeps := service.ScanImportLeafBookDeps{
		LibStore: libStore,
		Books:    bookRepo,
		Files:    fileRepo,
		Covers:   covers,
	}
	q, err := queue.New(ctx, dbh, bdropSvc, libSvc, storageResolver, libStore, fileRepo, bookRepo, scanImportDeps)
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
			Files:    fileRepo,
			LibStore: libStore,
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
			Books:  bookRepo,
			Covers: covers,
		}); err != nil {
			slog.Warn("covers backfill", "err", err)
		}
	}()

	// Missing-files purge sweeper: deletes files rows whose missing_since
	// is older than 24h. Runs hourly until the application shuts down.
	go task.LoopMissingPurge(ctx, fileRepo, time.Hour)

	// Orphaned-keys sweeper: drains pending_orphans rows whose grace
	// window has passed, deleting the underlying storage keys. Sources:
	// post-rename old keys (full RenameGrace) and rollback half-rename
	// new keys (RenameRollbackGrace). ADR-0005.
	go task.LoopOrphanedKeys(ctx, task.OrphanedKeysDeps{
		Orphans:  pendingOrphansRepo,
		Libs:     libRepo,
		Resolver: storageResolver,
	}, time.Hour)

	// S3 event loop: poll an SQS queue for S3 event notifications and
	// reconcile them into the files table without waiting for the next
	// full library scan. Disabled when EMBOOKSHELF_S3_EVENT_QUEUE is unset.
	if cfg.S3EventQueueURL != "" {
		sqsAwsCfg, sqsErr := awsconfig.LoadDefaultConfig(ctx,
			awsconfig.WithRegion(cfg.S3EventQueueRegion),
		)
		if sqsErr != nil {
			slog.Error("s3 events: build SQS client", "err", sqsErr)
			os.Exit(1)
		}
		sqsCli := awssqs.NewFromConfig(sqsAwsCfg)

		// Build the bucket→library map by walking libraries + resolving backends.
		bucketToLibrary := make(map[string]string)
		allLibs, libsErr := libRepo.List(ctx)
		if libsErr != nil {
			slog.Warn("s3 events: list libraries for bucket map", "err", libsErr)
		} else {
			for _, lib := range allLibs {
				if lib.BackendID == nil {
					continue
				}
				be, beErr := storageResolver.Resolve(*lib.BackendID)
				if beErr != nil {
					continue
				}
				if s3b, ok := be.(*s3backend.Backend); ok {
					bucketToLibrary[s3b.Bucket()] = lib.ID
				}
			}
		}

		slog.Info("s3 events loop starting",
			"queue", cfg.S3EventQueueURL,
			"region", cfg.S3EventQueueRegion,
			"poll_interval", cfg.S3EventPollInterval,
			"bucket_map", bucketToLibrary,
		)
		go task.RunS3EventLoop(ctx, task.S3EventLoopDeps{
			SQS:             sqsCli,
			QueueURL:        cfg.S3EventQueueURL,
			Files:           fileRepo,
			BucketToLibrary: bucketToLibrary,
			PollInterval:    cfg.S3EventPollInterval,
		})
	}

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
		Version:      version,
		Commit:       commit,
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
		Identities:   identityRepo,
		Search:       searchSvc,
		AppSettings:  appSettingsRepo,
		Covers:       covers,
		Hub:          hub,
		Queue:        q,
		LibStore:     libStore,
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
