// SPDX-License-Identifier: AGPL-3.0-or-later

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

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/coverstore"
	"github.com/blackforge/embookshelf/internal/crypto"
	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/email"
	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/handler"
	"github.com/blackforge/embookshelf/internal/ingest"
	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/migrator"
	"github.com/blackforge/embookshelf/internal/provider"
	"github.com/blackforge/embookshelf/internal/queue"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/sidecar"
	"github.com/blackforge/embookshelf/internal/sse"
	"github.com/blackforge/embookshelf/internal/staticfs"
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

	// Subcommands run and exit; no argument means "serve".
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "import-sqlite":
			os.Exit(importSQLiteCmd(os.Args[2:]))
		case "-h", "--help", "help":
			fmt.Fprintf(os.Stderr, `embookshelf %s (%s)

Usage:
  embookshelf                      serve (default)
  embookshelf import-sqlite ...    import an existing SQLite library into Postgres
`, version, commit)
			os.Exit(0)
		}
	}

	// Default to release mode (silences debug logs, skips trusted-proxy
	// warnings). Set GIN_MODE=debug to opt back into route logs.
	if os.Getenv("GIN_MODE") == "" {
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

	// Refuse a SQLite DSN before opening it (ADR-0023). Detecting from the
	// string rather than the handle avoids creating an empty database file
	// on the way to rejecting it.
	if dialect, derr := db.DetectDialect(cfg.DatabaseURL); derr == nil && dialect == db.DialectSQLite {
		slog.Error("SQLite is no longer supported — embookshelf requires Postgres")
		fmt.Fprintf(os.Stderr, `
DATABASE_URL points at SQLite, which this version cannot serve (ADR-0023).

Migrate the library into Postgres with:

  DATABASE_URL='postgres://user:pass@host:5432/embookshelf' \
    embookshelf import-sqlite --from <path to your .db file>

Then set DATABASE_URL to that Postgres DSN and start again. The target
database must be empty; migrations are applied to it automatically.
`)
		os.Exit(1)
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
		if err := runAppMigrations(dbh); err != nil {
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

	storageResolver, err := storageloader.LoadStorageBackends(ctx, bootBackendRepo)
	if err != nil {
		slog.Error("storage backends", "err", err)
		os.Exit(1)
	}

	// At-rest cipher for secrets in settings rows (SMTP password, OIDC
	// client secrets, metadata provider keys). Unset KEK falls back to
	// passthrough with a loud warning — secrets on disk stay plaintext
	// but the server still boots. A malformed key is fatal so admins
	// don't think encryption is on when it isn't. ADR-0010.
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
		slog.Warn("EMBOOKSHELF_SECRET_KEY unset — settings secrets stored in plaintext. " +
			"Set a base64-encoded 32-byte key for at-rest encryption.")
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
	appSettingsRepo := repo.NewAppSettingsRepo(dbh, secretCipher)
	fileRepo := repo.NewFileRepo(dbh)

	// SSE hub — shared between services that broadcast and the handler that serves /events.
	hub := sse.NewHub()

	// Cover image store (files on disk under ${DATA_PATH}/covers/).
	covers := coverstore.New(filepath.Join(cfg.DataPath, "covers"))

	// Services.
	backendRepo := repo.NewStorageBackendRepo(dbh)
	pendingOrphansRepo := repo.NewPendingOrphanRepo(dbh)
	// Built before LibraryService: book deletion needs a LibraryHandle to
	// find the bytes a book owned, and the handle has to exist before the
	// row it describes is gone.
	libStore := service.NewLibraryStore(service.LibraryStoreDeps{
		Libs:            libRepo,
		Resolver:        storageResolver,
		NewPlacer:       service.DefaultPlacerBuilder(storageResolver),
		Files:           fileRepo,
		Orphans:         pendingOrphansRepo,
		PresignTTL:      cfg.PresignTTL,
		PresignFallback: cfg.PresignFallback,
	})
	// MetadataWriter coordinates the ADR-0001 edit-side pipeline —
	// DB → in-file embed → sidecar → folder rename — for metadata edits.
	// It is built here, ahead of every service that performs an edit,
	// because LibraryService and EnrichmentService both take it as a
	// constructor argument: an edit that reaches only the books row is a
	// half-written edit, so there is no wiring in which they should run
	// without it. The auto-enrich background path passes
	// TriggerAutoEnrichment to skip the side-effect steps and only
	// persist the DB row.
	sidecarWriter := sidecar.NewWriter()
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
	libSvc := service.NewLibraryService(libRepo, bookRepo, service.LibraryServiceDeps{
		Backends:     backendRepo,
		SharedS3:     cfg.SharedS3,
		Resolver:     storageResolver,
		DataPath:     cfg.DataPath,
		LibStore:     libStore,
		Covers:       covers,
		BookDropPath: cfg.BookDropPath,
	}, metadataWriter)
	shelfSvc := service.NewShelfService(shelfRepo, hub)
	searchSvc := service.NewSearchService(libRepo, bookRepo, shelfRepo)
	authSvc := service.NewAuthService(userRepo, sessionRepo, hub)
	bdropSvc := service.NewBookDropService(bdropRepo, libRepo, bookRepo, covers, hub, fileRepo).
		WithLibraryStore(libStore).
		WithBookDropPath(cfg.BookDropPath)
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
	// The repo owns provider-config encryption (ADR-0010 §4), so it needs
	// the Cipher and a way to find each provider's secret slots. The
	// lookup is derived from the built providers' schemas here, at the
	// composition root, so the repo stays free of the provider catalog.
	providerSettingsRepo := repo.NewProviderSettingsRepo(
		dbh, secretCipher, provider.SecretKeyLookup(providers))
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
	enrichSvc := service.NewEnrichmentService(
		providers, providerSettingsRepo, bookRepo, covers, metadataWriter)
	providerCfgSvc := service.NewProviderSettingsService(providers, providerSettingsRepo)
	// Push stored per-provider config (API keys, language, …) into the
	// running provider instances. Failure here is non-fatal — providers
	// fall back to their no-config defaults.
	if err := providerCfgSvc.LoadConfigs(ctx); err != nil {
		slog.Warn("load provider configs", "err", err)
	}
	deviceSvc := service.NewDeviceService(
		deviceRepo, bookRepo, libStore,
		service.NewRemarkableDriver(),
	)

	// OIDC — settings live in app_settings so three providers
	// (Google, GitHub, generic OIDC) can be toggled independently
	// without a restart. Seed empty rows on first boot.
	if err := appSettingsRepo.SeedOIDCIfAbsent(ctx); err != nil {
		slog.Warn("seed oidc settings", "err", err)
	}
	identityRepo := repo.NewIdentityRepo(dbh)
	oidcSvc := service.NewOIDCService(appSettingsRepo, userRepo, sessionRepo, identityRepo, cfg.AppURL)

	// Forward-auth — reverse-proxy header trust (ADR-0022). Settings
	// row seeded with a disabled default; admins flip Enabled and
	// list TrustedProxyCIDRs from the settings UI. Boot refuses to
	// start if the persisted row is inconsistent (Enabled=true with
	// no CIDR), mirroring Cipher's bad-key behavior from ADR-0010.
	if err := appSettingsRepo.SeedForwardAuthIfAbsent(ctx); err != nil {
		slog.Warn("seed forward_auth settings", "err", err)
	}
	fwdAuthCfgRow, err := appSettingsRepo.GetForwardAuth(ctx)
	if err != nil {
		slog.Error("load forward_auth settings", "err", err)
		os.Exit(1)
	}
	if err := repo.ValidateForwardAuth(fwdAuthCfgRow); err != nil {
		slog.Error("forward_auth config invalid — refusing to start", "err", err)
		os.Exit(1)
	}
	fwdAuthRuntime, err := service.NewForwardAuthRuntime(fwdAuthCfgRow)
	if err != nil {
		slog.Error("forward_auth runtime config", "err", err)
		os.Exit(1)
	}
	fwdAuthHolder := auth.NewForwardAuthHolder(fwdAuthRuntime)
	fwdAuthSvc := service.NewForwardAuthService(appSettingsRepo, userRepo, identityRepo)
	if fwdAuthCfgRow.Enabled {
		slog.Info("forward_auth enabled",
			"trustedCIDRs", fwdAuthCfgRow.TrustedProxyCIDRs,
			"userHeader", fwdAuthCfgRow.Headers.User,
		)
	}

	if n, err := authSvc.PurgeExpiredSessions(ctx); err != nil {
		slog.Warn("purge sessions", "err", err)
	} else if n > 0 {
		slog.Info("purged expired sessions", "count", n)
	}

	// Email subsystem (ADR-0020). Notifier is always built; its runtime
	// sender is hot-reloadable via Notifier.Reload so admins can flip
	// the EMAIL row from the settings UI without restarting the
	// process. Reload at boot applies the persisted state.
	// Reading guides (ADR-0024). Seeded disabled, so the settings panel
	// has a row to edit and nothing generates until an admin turns it on.
	if err := appSettingsRepo.SeedReadingGuideIfAbsent(ctx); err != nil {
		slog.Warn("seed reading guide settings", "err", err)
	}
	if err := appSettingsRepo.SeedAudiobookIfAbsent(ctx); err != nil {
		slog.Warn("seed audiobook settings", "err", err)
	}
	if err := appSettingsRepo.SeedEmailIfAbsent(ctx); err != nil {
		slog.Warn("seed email settings", "err", err)
	}
	emailTpl, err := email.NewTemplates()
	if err != nil {
		slog.Error("email templates", "err", err)
		os.Exit(1)
	}
	resetRepo := repo.NewPasswordResetTokenRepo(dbh)
	inviteRepo := repo.NewUserInviteRepo(dbh)
	notifier := service.NewNotifier(service.NotifierDeps{
		Templates:   emailTpl,
		Resets:      resetRepo,
		Invites:     inviteRepo,
		Users:       userRepo,
		LibStore:    libStore,
		AppSettings: appSettingsRepo,
	})
	guideRepo := repo.NewBookReadingGuideRepo(dbh)
	audiobookRepo := repo.NewBookAudiobookRepo(dbh)
	resetSvc := service.NewPasswordResetService(userRepo, resetRepo, sessionRepo, notifier)
	if err := notifier.Reload(ctx); err != nil {
		slog.Warn("email subsystem disabled — reload failed", "err", err)
	} else if !notifier.Enabled() {
		slog.Info("email subsystem disabled — configure under admin settings to enable")
	}

	// Background queue. PG → River; SQLite → polling worker (queue.New dispatches by dialect).
	// One late-bound enqueuer for the whole composition root. The queue's
	// worker registry is built out of the services that need to enqueue,
	// so neither can exist first; jobs.Deferred holds that knot alone and
	// every service takes it as an ordinary argument.
	enq := &jobs.Deferred{}
	q, err := queue.New(ctx, dbh, queue.Deps{
		BookDropSvc: bdropSvc,
		Enrich:      enrichSvc,
		LibSvc:      libSvc,
		Resolver:    storageResolver,
		LibStore:    libStore,
		FileRepo:    fileRepo,
		Books:       bookRepo,
		Users:       userRepo,
		Notifier:    notifier,
		Hub:         hub,
		AppSettings: appSettingsRepo,
		Guides:      guideRepo,
		Audiobooks:  audiobookRepo,
		Covers:      covers,
		DataPath:    cfg.DataPath,
		Enqueue:     enq,
	})
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
	// The queue exists; everything holding the deferred enqueuer can now
	// reach it.
	enq.Resolve(q)

	// Close the intake loop: the service inserts the row and hands the item
	// straight to the worker pool. Wired after queue.New because the queue
	// takes bdropSvc as a dependency, so the two can only be joined here.
	bdropSvc.WithIngestDispatcher(func(ctx context.Context, itemID string) error {
		return q.Enqueue(ctx, jobs.BookDropIngestArgs{ItemID: itemID})
	})

	// Close the approve loop the same way: Approve reads the Auto-enrich
	// setting and hands the new book to the pool, so the gap-fill runs in
	// the background rather than inside whatever called Approve (ADR-0012).
	bdropSvc.WithAutoEnrich(appSettingsRepo, func(ctx context.Context, bookID string) error {
		return q.Enqueue(ctx, jobs.BookDropAutoEnrichArgs{BookID: bookID})
	})

	audiobookSvc := service.NewAudiobookService(
		audiobookRepo,
		service.NewLibraryBookOpener(libStore),
		enq,
	).WithStagingSweeper(func(bookID string) {
		if err := os.RemoveAll(task.StagingDir(cfg.DataPath, bookID)); err != nil {
			slog.Warn("audiobook: sweep staging after cancel", "book", bookID, "err", err)
		}
	})

	// Staging for abandoned failed or cancelled runs is dead weight after
	// a week. Hourly loop, same shape as the missing-file and
	// orphaned-key sweepers.
	go task.LoopAudiobookStagingSweep(ctx, audiobookRepo, cfg.DataPath)

	// Reading guide bulk runs dispatch one job per book, so the runner is
	// built after the queue. Text cap comes from the settings row at start
	// time via the job; the runner only needs it to size the estimate.
	guideCfg, err := appSettingsRepo.GetReadingGuide(ctx)
	if err != nil {
		slog.Warn("read reading guide settings", "err", err)
	}
	guideRunner := service.NewGuideRunner(guideRepo,
		func(ctx context.Context, bookID string) error {
			return q.Enqueue(ctx, jobs.ReadingGuideArgs{BookID: bookID})
		}, guideCfg.TextCap)

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

	// File watcher goroutine.
	watcher := &ingest.Watcher{
		Path:     cfg.BookDropPath,
		Interval: cfg.BookDropInterval,
		Svc:      bdropSvc,
	}
	go watcher.Run(ctx)

	// HTTP. The five required groups are positional — adding a seam to
	// any of them breaks the build here until it is supplied.
	h := handler.New(
		handler.NewPlatformDeps(cfg, staticfs.FS, version, commit, hub),
		handler.NewLibraryDeps(libSvc, shelfSvc, bookRepo, bdropSvc, progressSvc),
		handler.NewDiscoveryDeps(
			enrichSvc, providerCfgSvc, searchSvc,
			statsSvc, readingStatsSvc, annotationSvc,
			guideRepo, guideRunner,
			audiobookSvc, audiobookRepo,
		),
		handler.NewAccountDeps(authSvc, userRepo, deviceSvc, appSettingsRepo),
		handler.NewEmailDeps(notifier, resetSvc, inviteRepo, secretCipher, emailTpl),
		handler.Options{
			LibStore:      libStore,
			OIDC:          oidcSvc,
			Identities:    identityRepo,
			Covers:        covers,
			Queue:         q,
			FwdAuthHolder: fwdAuthHolder,
			FwdAuth:       fwdAuthSvc,
		},
	)

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
// runAppMigrations brings d up to the current schema. Used by boot and
// by `import-sqlite` — the dedicated-connection dance below is load
// bearing, so both paths must go through here rather than reaching for
// migrator.New directly.
func runAppMigrations(d *db.DB) error {
	// Open a short-lived, dedicated connection for the migrator so that
	// m.Close() (which golang-migrate's Postgres driver calls sql.DB.Close on)
	// does not close the shared application pool. Skipping this deadlocks
	// db.Close(): the migrator keeps a pool connection checked out and
	// pgxpool.Close waits for it forever.
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
	return nil
}
