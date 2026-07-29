// SPDX-License-Identifier: AGPL-3.0-or-later

// Package app is the composition root. Everything the process needs is
// wired here, in three phases that are deliberately separate:
//
//   - Build constructs. It opens the database, applies migrations and
//     assembles every repo, service, the queue and the HTTP handler. It
//     performs no startup side effect: nothing is seeded, no goroutine is
//     launched, the queue is not started. Failure is a returned error, so
//     a test can call Build and inspect the wiring without booting.
//   - Start runs the side effects — seeds, the reloads that read them,
//     the queue, and the background sweepers.
//   - Close shuts the whole thing down along one path.
//
// The split exists because construction and side effects used to
// interleave in main, which meant the only way to find out whether a seam
// had been left unassigned was to boot the process and wait for the
// nil dereference (#196).
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"

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
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/provider"
	"github.com/blackforge/embookshelf/internal/queue"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/sidecar"
	"github.com/blackforge/embookshelf/internal/sse"
	"github.com/blackforge/embookshelf/internal/staticfs"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storageloader"
	"github.com/blackforge/embookshelf/internal/task"
)

// App holds the wired process. Every field is assigned by Build; Start
// and Close read them, and so does the boot test, which is the point —
// a seam that Build forgets is a failing assertion rather than a
// production nil dereference.
type App struct {
	cfg config.Config
	db  *db.DB

	// Process-level primitives shared by everything downstream.
	storageResolver storage.Resolver
	cipher          crypto.Cipher
	hub             *sse.Hub
	covers          *coverstore.Store

	// Repositories.
	libRepo              *repo.LibraryRepo
	bookRepo             *repo.BookRepo
	userRepo             *repo.UserRepo
	fileRepo             *repo.FileRepo
	bdropRepo            *repo.BookDropRepo
	appSettingsRepo      *repo.AppSettingsRepo
	providerSettingsRepo *repo.ProviderSettingsRepo
	identityRepo         *repo.IdentityRepo
	inviteRepo           *repo.UserInviteRepo
	guideRepo            *repo.BookReadingGuideRepo
	audiobookRepo        *repo.BookAudiobookRepo
	pendingOrphansRepo   *repo.PendingOrphanRepo

	// Services.
	libStore        service.LibraryStore
	metadataWriter  *service.MetadataWriter
	libSvc          *service.LibraryService
	shelfSvc        *service.ShelfService
	searchSvc       *service.SearchService
	authSvc         *service.AuthService
	bdropSvc        *service.BookDropService
	progressSvc     *service.ProgressService
	annotationSvc   *service.AnnotationService
	statsSvc        *service.StatsService
	readingStatsSvc *service.ReadingSessionService
	enrichSvc       *service.EnrichmentService
	providerCfgSvc  *service.ProviderSettingsService
	deviceSvc       *service.DeviceService
	oidcSvc         *service.OIDCService
	fwdAuthHolder   *auth.ForwardAuthHolder
	fwdAuthSvc      *service.ForwardAuthService
	notifier        *service.Notifier
	resetSvc        *service.PasswordResetService
	guideRunner     *service.GuideRunner
	audiobookSvc    *service.AudiobookService
	emailTpl        *email.Templates

	// Background queue and the late-bound enqueuer the services holding
	// it were built with. They are started together — see startQueue.
	queue queue.Client
	enq   *jobs.Deferred

	// Bookdrop watcher; constructed here, run by Start.
	watcher *ingest.Watcher

	handler *handler.Handler
	engine  *gin.Engine
}

// Build wires the process and returns it unstarted.
//
// Nothing here has a startup side effect: no seed, no reload, no
// goroutine, no queue start. The only writes are the ones that must
// precede reads of the same data — schema migrations, the storage_v2
// backfill and the shared-S3 backend reconcile, all of which the
// constructors below read back.
func Build(ctx context.Context, cfg config.Config, version, commit string) (*App, error) {
	dbh, err := db.Open(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db connect: %w", err)
	}
	// Every failure past this point owns the pool: the caller gets an
	// error rather than an App, so it has no handle to close.
	built := false
	defer func() {
		if !built {
			_ = dbh.Close()
		}
	}()

	// Apply app schema migrations before any repo runs queries. River's own
	// migrations are applied separately inside queue.New().
	if cfg.MigrateOnStart {
		if err := RunMigrations(dbh); err != nil {
			return nil, fmt.Errorf("migrate: %w", err)
		}
		if err := migrator.BackfillStorageV2(ctx, dbh); err != nil {
			return nil, fmt.Errorf("storage_v2 backfill: %w", err)
		}
	}

	// Not a Start-phase reconcile even though it writes: the resolver
	// built from these rows is a constructor argument to half the service
	// tier, so the rows have to be right before anything is built.
	bootBackendRepo := repo.NewStorageBackendRepo(dbh)
	if n, err := storageloader.ReconcileSharedS3(ctx, bootBackendRepo, cfg.SharedS3); err != nil {
		return nil, fmt.Errorf("reconcile shared s3 backends: %w", err)
	} else if n > 0 {
		slog.Info("storage backends reconciled from env", "updated", n)
	}

	storageResolver, err := storageloader.LoadStorageBackends(ctx, bootBackendRepo)
	if err != nil {
		return nil, fmt.Errorf("storage backends: %w", err)
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
			return nil, fmt.Errorf("EMBOOKSHELF_SECRET_KEY invalid — refusing to boot: %w", err)
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
	//
	// One late-bound enqueuer for the whole composition root, created
	// before any of its consumers. bdropSvc, guideRunner and audiobookSvc
	// all take it as an ordinary argument; the queue's own worker
	// registry is assembled inside queue.New out of those very services,
	// so neither the queue nor its consumers can be built first.
	// jobs.Deferred holds that knot alone, and Resolve closes it once the
	// queue exists — see startQueue.
	enq := &jobs.Deferred{}
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
	bdropSvc := service.NewBookDropService(bdropRepo, libRepo, bookRepo, covers, hub, fileRepo, enq).
		WithLibraryStore(libStore).
		WithBookDropPath(cfg.BookDropPath).
		WithAutoEnrichPolicy(appSettingsRepo)
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
	enrichSvc := service.NewEnrichmentService(
		providers, providerSettingsRepo, bookRepo, covers, metadataWriter)
	providerCfgSvc := service.NewProviderSettingsService(providers, providerSettingsRepo)
	deviceSvc := service.NewDeviceService(
		deviceRepo, bookRepo, libStore,
		service.NewRemarkableDriver(),
	)

	// OIDC — settings live in app_settings so three providers
	// (Google, GitHub, generic OIDC) can be toggled independently
	// without a restart. The rows are seeded by Start.
	identityRepo := repo.NewIdentityRepo(dbh)
	oidcSvc := service.NewOIDCService(appSettingsRepo, userRepo, sessionRepo, identityRepo, cfg.AppURL)

	// Forward-auth — reverse-proxy header trust (ADR-0022). Boot refuses
	// to start if the persisted row is inconsistent (Enabled=true with no
	// CIDR), mirroring Cipher's bad-key behavior from ADR-0010. Read
	// before Start seeds the row: a missing FORWARD_AUTH row reads back as
	// the disabled default, which is exactly what the seed writes, so the
	// runtime config is the same on first boot as on every later one.
	fwdAuthCfgRow, err := appSettingsRepo.GetForwardAuth(ctx)
	if err != nil {
		return nil, fmt.Errorf("load forward_auth settings: %w", err)
	}
	if err := repo.ValidateForwardAuth(fwdAuthCfgRow); err != nil {
		return nil, fmt.Errorf("forward_auth config invalid — refusing to start: %w", err)
	}
	fwdAuthRuntime, err := service.NewForwardAuthRuntime(fwdAuthCfgRow)
	if err != nil {
		return nil, fmt.Errorf("forward_auth runtime config: %w", err)
	}
	fwdAuthHolder := auth.NewForwardAuthHolder(fwdAuthRuntime)
	fwdAuthSvc := service.NewForwardAuthService(appSettingsRepo, userRepo, identityRepo)
	if fwdAuthCfgRow.Enabled {
		slog.Info("forward_auth enabled",
			"trustedCIDRs", fwdAuthCfgRow.TrustedProxyCIDRs,
			"userHeader", fwdAuthCfgRow.Headers.User,
		)
	}

	// Email subsystem (ADR-0020). Notifier is always built; its runtime
	// sender is hot-reloadable via Notifier.Reload so admins can flip
	// the EMAIL row from the settings UI without restarting the
	// process. Start's Reload applies the persisted state.
	emailTpl, err := email.NewTemplates()
	if err != nil {
		return nil, fmt.Errorf("email templates: %w", err)
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
	// Reading guide bulk runs dispatch one job per book. Text cap comes
	// from the settings row at start time via the job; the runner only
	// needs it to size the estimate. Same absent-row reasoning as
	// forward-auth above: unseeded reads back as the seeded default.
	guideCfg, err := appSettingsRepo.GetReadingGuide(ctx)
	if err != nil {
		slog.Warn("read reading guide settings", "err", err)
	}
	guideRunner := service.NewGuideRunner(guideRepo, enq, guideCfg.TextCap)
	audiobookRepo := repo.NewBookAudiobookRepo(dbh)
	resetSvc := service.NewPasswordResetService(userRepo, resetRepo, sessionRepo, notifier)

	// Built before the queue: the workers advance a run through this
	// service rather than switching on the transition themselves (#190),
	// so the registry needs it. Its enqueuer is the deferred one, which
	// is what makes that ordering possible at all.
	audiobookSvc := service.NewAudiobookService(
		audiobookRepo,
		service.NewLibraryBookOpener(libStore),
		enq,
	).WithStagingSweeper(func(bookID string) {
		if err := os.RemoveAll(task.StagingDir(cfg.DataPath, bookID)); err != nil {
			slog.Warn("audiobook: sweep staging after cancel", "book", bookID, "err", err)
		}
	}).WithSettings(appSettingsRepo.GetAudiobook).
		// The hash of the book's own file, for provenance and for the
		// staleness comparison. Injected because it lives on the files row
		// behind a library handle, which the service deliberately cannot
		// reach (#191).
		WithContentHash(func(ctx context.Context, book model.Book) []byte {
			handle, err := libStore.For(ctx, book.LibraryID)
			if err != nil {
				return nil
			}
			return handle.PrimaryContentHash(ctx, book)
		}).
		WithNarrationSweeper(func(ctx context.Context, book model.Book, run model.Audiobook) error {
			if run.FileID == nil {
				return nil
			}
			handle, err := libStore.For(ctx, book.LibraryID)
			if err != nil {
				return err
			}
			f, ok := handle.BookFile(ctx, book.ID, *run.FileID)
			if !ok {
				return nil
			}
			return handle.DeleteBookBytes(ctx, book.ID, []string{f.Location})
		})
	if hub != nil {
		audiobookSvc = audiobookSvc.WithPublisher(func(bookID string) {
			_ = hub.Publish(sse.AudiobookUpdated{BookID: bookID})
		})
	}

	// Background queue, backed by River. Constructed, not started —
	// Start owns that, together with resolving enq.
	q, err := queue.New(ctx, dbh, queue.Deps{
		BookDropSvc:  bdropSvc,
		Enrich:       enrichSvc,
		LibSvc:       libSvc,
		Resolver:     storageResolver,
		LibStore:     libStore,
		FileRepo:     fileRepo,
		Books:        bookRepo,
		Users:        userRepo,
		Notifier:     notifier,
		Hub:          hub,
		AppSettings:  appSettingsRepo,
		Guides:       guideRepo,
		Audiobooks:   audiobookRepo,
		AudiobookSvc: audiobookSvc,
		Covers:       covers,
		DataPath:     cfg.DataPath,
		Enqueue:      enq,
	})
	if err != nil {
		return nil, fmt.Errorf("queue: %w", err)
	}

	watcher := &ingest.Watcher{
		Path:     cfg.BookDropPath,
		Interval: cfg.BookDropInterval,
		Svc:      bdropSvc,
	}

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

	built = true
	return &App{
		cfg: cfg,
		db:  dbh,

		storageResolver: storageResolver,
		cipher:          secretCipher,
		hub:             hub,
		covers:          covers,

		libRepo:              libRepo,
		bookRepo:             bookRepo,
		userRepo:             userRepo,
		fileRepo:             fileRepo,
		bdropRepo:            bdropRepo,
		appSettingsRepo:      appSettingsRepo,
		providerSettingsRepo: providerSettingsRepo,
		identityRepo:         identityRepo,
		inviteRepo:           inviteRepo,
		guideRepo:            guideRepo,
		audiobookRepo:        audiobookRepo,
		pendingOrphansRepo:   pendingOrphansRepo,

		libStore:        libStore,
		metadataWriter:  metadataWriter,
		libSvc:          libSvc,
		shelfSvc:        shelfSvc,
		searchSvc:       searchSvc,
		authSvc:         authSvc,
		bdropSvc:        bdropSvc,
		progressSvc:     progressSvc,
		annotationSvc:   annotationSvc,
		statsSvc:        statsSvc,
		readingStatsSvc: readingStatsSvc,
		enrichSvc:       enrichSvc,
		providerCfgSvc:  providerCfgSvc,
		deviceSvc:       deviceSvc,
		oidcSvc:         oidcSvc,
		fwdAuthHolder:   fwdAuthHolder,
		fwdAuthSvc:      fwdAuthSvc,
		notifier:        notifier,
		resetSvc:        resetSvc,
		guideRunner:     guideRunner,
		audiobookSvc:    audiobookSvc,
		emailTpl:        emailTpl,

		queue:   q,
		enq:     enq,
		watcher: watcher,

		handler: h,
		engine:  h.Engine(),
	}, nil
}

// Engine is the HTTP handler main serves. Built by Build so a failure to
// register a route surfaces before the listener opens.
func (a *App) Engine() *gin.Engine { return a.engine }

// Start runs every boot-time side effect, in the order the process has
// always run them: seed the settings rows, apply what they say, bring the
// queue up, then launch the background loops.
//
// Only a failed queue start is fatal. The rest degrade: a settings row
// that cannot be seeded leaves the feature at its defaults, which is the
// same state a fresh install has.
func (a *App) Start(ctx context.Context) error {
	// Seed provider_settings on first boot using catalog defaults.
	// ON CONFLICT DO NOTHING means subsequent restarts leave admin
	// toggles alone — the DB is authoritative after the initial seed.
	defaults := make(map[string]bool, len(provider.Catalog))
	for _, c := range provider.Catalog {
		defaults[string(c.ID)] = c.DefaultEnabled
	}
	if err := a.providerSettingsRepo.SeedIfAbsent(ctx, defaults); err != nil {
		slog.Warn("seed provider settings", "err", err)
	}
	// Push stored per-provider config (API keys, language, …) into the
	// running provider instances. Failure here is non-fatal — providers
	// fall back to their no-config defaults.
	if err := a.providerCfgSvc.LoadConfigs(ctx); err != nil {
		slog.Warn("load provider configs", "err", err)
	}

	// Settings rows seeded so the admin UI has something to render on
	// first boot. Each seeds a documented default: OIDC empty,
	// forward-auth and reading guides disabled.
	if err := a.appSettingsRepo.SeedOIDCIfAbsent(ctx); err != nil {
		slog.Warn("seed oidc settings", "err", err)
	}
	if err := a.appSettingsRepo.SeedForwardAuthIfAbsent(ctx); err != nil {
		slog.Warn("seed forward_auth settings", "err", err)
	}

	if n, err := a.authSvc.PurgeExpiredSessions(ctx); err != nil {
		slog.Warn("purge sessions", "err", err)
	} else if n > 0 {
		slog.Info("purged expired sessions", "count", n)
	}

	if err := a.appSettingsRepo.SeedReadingGuideIfAbsent(ctx); err != nil {
		slog.Warn("seed reading guide settings", "err", err)
	}
	if err := a.appSettingsRepo.SeedAudiobookIfAbsent(ctx); err != nil {
		slog.Warn("seed audiobook settings", "err", err)
	}
	if err := a.appSettingsRepo.SeedEmailIfAbsent(ctx); err != nil {
		slog.Warn("seed email settings", "err", err)
	}
	// Reload after the seed so the notifier picks up the persisted EMAIL
	// row rather than the state it was constructed with. ADR-0020.
	if err := a.notifier.Reload(ctx); err != nil {
		slog.Warn("email subsystem disabled — reload failed", "err", err)
	} else if !a.notifier.Enabled() {
		slog.Info("email subsystem disabled — configure under admin settings to enable")
	}

	if err := a.startQueue(ctx); err != nil {
		return err
	}

	// Staging for abandoned failed or cancelled runs is dead weight after
	// a week. Hourly loop, same shape as the missing-file and
	// orphaned-key sweepers.
	go task.LoopAudiobookStagingSweep(ctx, a.audiobookRepo, a.cfg.DataPath)

	// Requeue anything still mid-flight from a previous process.
	ingest.DiscoverOnStartup(ctx, a.bdropRepo, a.queue)

	// Boot-time files backfill: hash any files rows that are still missing a
	// content_hash. Runs in the background so it doesn't block startup.
	// 1-hour timeout is generous; real deployments have hundreds of files at most.
	go func() {
		slog.Info("files backfill starting")
		backfillCtx, cancel := context.WithTimeout(context.Background(), 1*time.Hour)
		defer cancel()
		if err := task.RunFilesBackfill(backfillCtx, task.FilesBackfillDeps{
			Files:    a.fileRepo,
			LibStore: a.libStore,
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
			Books:  a.bookRepo,
			Covers: a.covers,
		}); err != nil {
			slog.Warn("covers backfill", "err", err)
		}
	}()

	// Missing-files purge sweeper: deletes files rows whose missing_since
	// is older than 24h. Runs hourly until the application shuts down.
	go task.LoopMissingPurge(ctx, a.fileRepo, time.Hour)

	// Orphaned-keys sweeper: drains pending_orphans rows whose grace
	// window has passed, deleting the underlying storage keys. Sources:
	// post-rename old keys (full RenameGrace) and rollback half-rename
	// new keys (RenameRollbackGrace). ADR-0005.
	go task.LoopOrphanedKeys(ctx, task.OrphanedKeysDeps{
		Orphans:  a.pendingOrphansRepo,
		Libs:     a.libRepo,
		Resolver: a.storageResolver,
	}, time.Hour)

	// File watcher goroutine.
	go a.watcher.Run(ctx)

	return nil
}

// startQueue resolves the deferred enqueuer and starts the queue.
//
// These are one operation, not two steps that happen to be adjacent.
// River begins draining jobs the moment Start returns, so a leftover job
// can reach a worker immediately; if enq were still unresolved at that
// instant the worker's follow-on dispatch would fail with ErrNoQueue and
// the run would stall with no error to explain it (#184). Holding both
// calls in one method is what keeps a later edit from putting anything
// between them.
func (a *App) startQueue(ctx context.Context) error {
	a.enq.Resolve(a.queue)
	if err := a.queue.Start(ctx); err != nil {
		return fmt.Errorf("queue start: %w", err)
	}
	return nil
}

// Close is the single shutdown path: drain the queue, then close the
// database. Errors are joined rather than returned at the first failure —
// a queue that refuses to drain must not leave the pool open.
func (a *App) Close(ctx context.Context) error {
	var errs []error

	// The 15s budget is detached from ctx because ctx is normally the
	// signal context, already cancelled by the time we get here — a
	// derived context would expire instantly and the drain would be no
	// drain at all. WithoutCancel keeps the values (trace context) and
	// drops only the cancellation.
	//
	// river.Client.Stop tolerates a client that was never started
	// (returns nil rather than blocking or erroring), so this runs
	// unconditionally even when Start failed before reaching the queue.
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if err := a.queue.Stop(stopCtx); err != nil {
		errs = append(errs, fmt.Errorf("queue stop: %w", err))
	}

	if err := a.db.Close(); err != nil {
		errs = append(errs, fmt.Errorf("db close: %w", err))
	}

	return errors.Join(errs...)
}

// RunMigrations applies every pending schema migration using the embedded
// migration files. Idempotent — a no-op when the DB is already up-to-date.
//
// Used by Build and by `import-sqlite` — the dedicated-connection dance
// below is load bearing, so both paths must go through here rather than
// reaching for migrator.New directly.
func RunMigrations(d *db.DB) error {
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
