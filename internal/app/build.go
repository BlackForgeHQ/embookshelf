// SPDX-License-Identifier: AGPL-3.0-or-later

package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

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
	"github.com/blackforge/embookshelf/internal/storage"
	s3storage "github.com/blackforge/embookshelf/internal/storage/s3"
	"github.com/blackforge/embookshelf/internal/storageloader"
	"github.com/blackforge/embookshelf/internal/task"
)

// The build stages (#304, #254). Build delegates to them in order —
// prepare → repos → wiring → services → queue → watchers — each taking
// the bundles of the stages before it and returning its own, which the
// App holds. A stage that needs a value cannot be called before the
// stage producing it: the ordering constraints that used to live in
// comments are arguments now, and violating one is a compile error.
//
// Ordering constraints. This is the one place they are listed (#254).
// Enforced by stage signatures — misordering fails to compile:
//
//   - migrations, the storage_v2 backfill and the shared-S3 reconcile
//     before any repo query: buildRepos takes the prepared bundle.
//   - the cipher before the two settings repos: both take r.cipher.
//   - the enqueuer before its three consumers (bdrop, guide runner,
//     audiobook): all take w.enq.
//   - the library store before the library service, and the metadata
//     writer before the library and enrichment services: constructor
//     arguments inside buildServices.
//   - the built providers before the provider-settings repo: the
//     secret-slot lookup is derived from them.
//   - the audiobook service before the queue: buildQueue takes s.
//   - everything before the handler: buildHTTP takes every bundle.
//
// Still prose, because types cannot see inside a function:
//
//   - within prepare: migrations, then the storage_v2 backfill over the
//     migrated schema, then the shared-S3 reconcile, then the resolver
//     load over the reconciled rows. One function, four sequential
//     statements — swap the reconcile and the load and the S3 backends
//     build from stale rows, silently.
//   - the enqueuer resolve immediately before the queue start: held in
//     one method, startQueue — see its comment (#184).

// wiring carries the process-level primitives every stage shares. It
// embeds prepared rather than holding the pool directly, so the only
// way to give a stage a database is through the bundle prepare
// returned — w.dbh and w.resolver are the promoted fields.
type wiring struct {
	prepared
	cfg     config.Config
	hub     *sse.Hub
	covers  *coverstore.Store
	comics  *fileproc.PageCache
	staging task.Staging
	// enq is the one late-bound enqueuer for the whole composition root,
	// created before any of its consumers. bdropSvc, guideRunner and
	// audiobookSvc all take it as an ordinary argument; the queue's own
	// worker registry is assembled inside queue.New out of those very
	// services, so neither the queue nor its consumers can be built
	// first. jobs.Deferred holds that knot alone, and Resolve closes it
	// once the queue exists — see startQueue.
	enq *jobs.Deferred
}

// prepared is the proof the database is ready to be read: the schema is
// migrated, storage_v2 rows are backfilled, the shared-S3 backend rows
// are reconciled with the environment, and the resolver is built from
// those reconciled rows. It is the only carrier of the pool past
// prepare — buildRepos takes it directly, every other stage reaches it
// through the wiring that embeds it — which is what makes "migrations
// before any repo query" a compile error rather than a comment.
type prepared struct {
	dbh      *db.DB
	resolver storage.Resolver
}

// prepare runs the writes construction depends on. These are the only
// side effects in the build phase, and they exist because the
// constructors read the data back: repos query the migrated schema, and
// the resolver is built from the reconciled backend rows. Startup side
// effects that nothing in Build reads — seeds, reloads, the queue —
// belong to Start.
//
// The four statements are order-load-bearing; see the ordering list at
// the top of this file.
func prepare(ctx context.Context, dbh *db.DB, cfg config.Config) (prepared, error) {
	// App schema migrations before any repo runs queries. River's own
	// migrations are applied separately inside queue.New().
	if cfg.MigrateOnStart {
		if err := RunMigrations(dbh); err != nil {
			return prepared{}, fmt.Errorf("migrate: %w", err)
		}
		if err := migrator.BackfillStorageV2(ctx, dbh); err != nil {
			return prepared{}, fmt.Errorf("storage_v2 backfill: %w", err)
		}
	}

	// Reconcile before load: a kind=s3 row records the bucket, endpoint
	// and credentials it was created with, and after a rotation the row
	// is stale until this brings it in line with the environment. The
	// resolver is a constructor argument to half the service tier, so
	// the rows have to be right before anything is built.
	bootBackendRepo := repo.NewStorageBackendRepo(dbh)
	if n, err := storageloader.ReconcileSharedS3(ctx, bootBackendRepo, cfg.SharedS3); err != nil {
		return prepared{}, fmt.Errorf("reconcile shared s3 backends: %w", err)
	} else if n > 0 {
		slog.Info("storage backends reconciled from env", "updated", n)
	}
	resolver, err := storageloader.LoadStorageBackends(ctx, bootBackendRepo)
	if err != nil {
		return prepared{}, fmt.Errorf("storage backends: %w", err)
	}

	return prepared{dbh: dbh, resolver: resolver}, nil
}

// buildWiring assembles the process-level primitives over the prepared
// database. Its own construction can only fail on a data root that
// cannot yield a covers directory.
func buildWiring(p prepared, cfg config.Config) (wiring, error) {
	// Cover image store (files on disk under ${DATA_PATH}/covers/). The
	// root derives the location; joining it here is how a deployment with
	// no data root ended up caching covers into ./covers under whatever
	// the working directory happened to be. Staging (#251) is the other
	// on-disk area for derived bytes, built once and handed to everything
	// that touches it.
	coversDir, err := cfg.DataPath.Covers()
	if err != nil {
		return wiring{}, fmt.Errorf("cover store: %w", err)
	}
	// The comic reader's page cache (#329) is the third on-disk area for
	// derived bytes, and derives its location the same way for the same
	// reason. Disposable: wiped on first use, capped on admission, no
	// sweeper.
	comicPagesDir, err := cfg.DataPath.ComicPages()
	if err != nil {
		return wiring{}, fmt.Errorf("comic page cache: %w", err)
	}
	return wiring{
		prepared: p,
		cfg:      cfg,
		hub:      sse.NewHub(),
		covers:   coverstore.New(coversDir),
		comics:   fileproc.NewPageCache(comicPagesDir, fileproc.DefaultPageCacheBytes),
		staging:  task.NewStaging(cfg.DataPath),
		enq:      &jobs.Deferred{},
	}, nil
}

// repos is every repository over the pool, plus the at-rest cipher the
// settings repos encrypt secrets with.
type repos struct {
	cipher         crypto.Cipher
	lib            *repo.LibraryRepo
	book           *repo.BookRepo
	shelf          *repo.ShelfRepo
	user           *repo.UserRepo
	session        *repo.SessionRepo
	bdrop          *repo.BookDropRepo
	progress       *repo.ProgressRepo
	annotation     *repo.AnnotationRepo
	stats          *repo.StatsRepo
	readingSession *repo.ReadingSessionRepo
	device         *repo.DeviceRepo
	appSettings    *repo.AppSettingsRepo
	file           *repo.FileRepo
	backend        *repo.StorageBackendRepo
	pendingOrphans *repo.PendingOrphanRepo
	identity       *repo.IdentityRepo
	reset          *repo.PasswordResetTokenRepo
	invite         *repo.UserInviteRepo
	guide          *repo.BookReadingGuideRepo
	rendition      *repo.BookMarkdownRenditionRepo
	epubRendition  *repo.BookEpubRenditionRepo
	audiobook      *repo.BookAudiobookRepo
}

// buildRepos constructs the repository tier. It takes prepared, not the
// raw pool: a repo constructed before the schema it queries exists is
// the misordering this signature forbids.
func buildRepos(p prepared, cfg config.Config) (repos, error) {
	dbh := p.dbh
	// At-rest cipher for secrets in settings rows (SMTP password, OIDC
	// client secrets, metadata provider keys). Unset KEK falls back to
	// passthrough with a loud warning — secrets on disk stay plaintext
	// but the server still boots. A malformed key is fatal so admins
	// don't think encryption is on when it isn't. ADR-0010.
	var secretCipher crypto.Cipher
	if cfg.SecretKey != "" {
		ac, err := crypto.NewAESGCM(cfg.SecretKey)
		if err != nil {
			return repos{}, fmt.Errorf("EMBOOKSHELF_SECRET_KEY invalid — refusing to boot: %w", err)
		}
		secretCipher = ac
		slog.Info("secrets encryption enabled (AES-256-GCM)")
	} else {
		secretCipher = crypto.Noop{}
		slog.Warn("EMBOOKSHELF_SECRET_KEY unset — settings secrets stored in plaintext. " +
			"Set a base64-encoded 32-byte key for at-rest encryption.")
	}

	return repos{
		cipher:         secretCipher,
		lib:            repo.NewLibraryRepo(dbh),
		book:           repo.NewBookRepo(dbh),
		shelf:          repo.NewShelfRepo(dbh),
		user:           repo.NewUserRepo(dbh),
		session:        repo.NewSessionRepo(dbh),
		bdrop:          repo.NewBookDropRepo(dbh),
		progress:       repo.NewProgressRepo(dbh),
		annotation:     repo.NewAnnotationRepo(dbh),
		stats:          repo.NewStatsRepo(dbh),
		readingSession: repo.NewReadingSessionRepo(dbh),
		device:         repo.NewDeviceRepo(dbh),
		appSettings:    repo.NewAppSettingsRepo(dbh, secretCipher),
		file:           repo.NewFileRepo(dbh),
		backend:        repo.NewStorageBackendRepo(dbh),
		pendingOrphans: repo.NewPendingOrphanRepo(dbh),
		identity:       repo.NewIdentityRepo(dbh),
		reset:          repo.NewPasswordResetTokenRepo(dbh),
		invite:         repo.NewUserInviteRepo(dbh),
		guide:          repo.NewBookReadingGuideRepo(dbh),
		rendition:      repo.NewBookMarkdownRenditionRepo(dbh),
		epubRendition:  repo.NewBookEpubRenditionRepo(dbh),
		audiobook:      repo.NewBookAudiobookRepo(dbh),
	}, nil
}

// services is the service tier, plus the two runtime values built
// beside it (the provider-settings repo, whose secret-slot lookup is
// derived from the built providers, and the email templates).
type services struct {
	libStore             service.LibraryStore
	metadataWriter       *service.MetadataWriter
	lib                  *service.LibraryService
	shelf                *service.ShelfService
	search               *service.SearchService
	auth                 *service.AuthService
	bdrop                *service.BookDropService
	progress             *service.ProgressService
	annotation           *service.AnnotationService
	stats                *service.StatsService
	readingStats         *service.ReadingSessionService
	enrich               *service.EnrichmentService
	providerCfg          *service.ProviderSettingsService
	providerSettingsRepo *repo.ProviderSettingsRepo
	device               *service.DeviceService
	oidc                 *service.OIDCService
	fwdAuthHolder        *auth.ForwardAuthHolder
	fwdAuth              *service.ForwardAuthService
	notifier             *service.Notifier
	reset                *service.PasswordResetService
	guideRunner          *service.GuideRunner
	audiobook            *service.AudiobookService
	emailTpl             *email.Templates
}

// buildServices constructs the service tier over the repos.
func buildServices(ctx context.Context, w wiring, r repos) (services, error) {
	// Built before LibraryService: book deletion needs a LibraryHandle to
	// find the bytes a book owned, and the handle has to exist before the
	// row it describes is gone.
	libStore := service.NewLibraryStore(service.LibraryStoreDeps{
		Libs:            r.lib,
		Resolver:        w.resolver,
		NewPlacer:       service.DefaultPlacerBuilder(w.resolver),
		Files:           r.file,
		Orphans:         r.pendingOrphans,
		PresignTTL:      w.cfg.PresignTTL,
		PresignFallback: w.cfg.PresignFallback,
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
	renameGrace := w.cfg.S3RenameGrace
	if renameGrace <= 0 {
		// ADR-0005: 2× PresignTTL covers any URL the client could
		// still hold. Floor at 1h so very short PresignTTL (manual
		// override) doesn't make the sweeper too aggressive.
		renameGrace = max(2*w.cfg.PresignTTL, time.Hour)
	}
	folderRenamer := service.NewFolderRenamer(service.FolderRenamerDeps{
		Store:   r.book,
		Files:   r.file,
		Orphans: r.pendingOrphans,
		Grace:   renameGrace,
	})
	metadataWriter := service.NewMetadataWriter(service.MetadataWriterDeps{
		Books:    r.book,
		LibStore: libStore,
		Sidecar:  sidecarWriter,
		Dispatch: fileproc.DispatchEmbedder,
		Files:    r.file,
		Renamer:  folderRenamer,
	})
	libSvc := service.NewLibraryService(r.lib, r.book, service.LibraryServiceDeps{
		Backends:     r.backend,
		SharedS3:     w.cfg.SharedS3,
		Resolver:     w.resolver,
		DataPath:     w.cfg.DataPath,
		LibStore:     libStore,
		Covers:       w.covers,
		BookDropPath: w.cfg.BookDropPath,
	}, metadataWriter)
	bdropSvc := service.NewBookDropService(r.bdrop, r.lib, r.book, w.covers, w.hub, r.file, w.enq).
		WithLibraryStore(libStore).
		WithBookDropPath(w.cfg.BookDropPath).
		WithAutoEnrichPolicy(r.appSettings)

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
		w.dbh, r.cipher, provider.SecretKeyLookup(providers))

	// Forward-auth — reverse-proxy header trust (ADR-0022). Boot refuses
	// to start if the persisted row is inconsistent (Enabled=true with no
	// CIDR), mirroring Cipher's bad-key behavior from ADR-0010. Read
	// before Start seeds the row: a missing FORWARD_AUTH row reads back as
	// the disabled default, which is exactly what the seed writes, so the
	// runtime config is the same on first boot as on every later one.
	fwdAuthCfgRow, err := r.appSettings.GetForwardAuth(ctx)
	if err != nil {
		return services{}, fmt.Errorf("load forward_auth settings: %w", err)
	}
	if err := repo.ValidateForwardAuth(fwdAuthCfgRow); err != nil {
		return services{}, fmt.Errorf("forward_auth config invalid — refusing to start: %w", err)
	}
	fwdAuthRuntime, err := service.NewForwardAuthRuntime(fwdAuthCfgRow)
	if err != nil {
		return services{}, fmt.Errorf("forward_auth runtime config: %w", err)
	}
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
		return services{}, fmt.Errorf("email templates: %w", err)
	}
	notifier := service.NewNotifier(service.NotifierDeps{
		Templates:   emailTpl,
		Resets:      r.reset,
		Invites:     r.invite,
		Users:       r.user,
		LibStore:    libStore,
		AppSettings: r.appSettings,
	})

	// Reading guide bulk runs dispatch one job per book. Text cap comes
	// from the settings row at start time via the job; the runner only
	// needs it to size the estimate. Same absent-row reasoning as
	// forward-auth above: unseeded reads back as the seeded default.
	guideCfg, err := r.appSettings.GetReadingGuide(ctx)
	if err != nil {
		slog.Warn("read reading guide settings", "err", err)
	}

	// Built before the queue: the workers advance a run through this
	// service rather than switching on the transition themselves (#190),
	// so the registry needs it. Its enqueuer is the deferred one, which
	// is what makes that ordering possible at all.
	audiobookDeps := service.AudiobookDeps{
		Store:   r.audiobook,
		Books:   service.NewLibraryBookOpener(libStore),
		Enqueue: w.enq,

		Settings: r.appSettings.GetAudiobook,

		// Cancel discards the run's staged segments. A method value, not a
		// closure that deletes: the staging area is the only thing that
		// knows where its files are, so it is the only thing that removes
		// them.
		SweepStaging: w.staging.Clean,

		// The hash of the book's own file, for provenance and for the
		// staleness comparison. Injected because it lives on the files row
		// behind a library handle, which the service deliberately cannot
		// reach (#191). The shared constructor warns on an unresolvable
		// library instead of silently reading fresh (#297).
		ContentHash: service.NewPrimaryHash(libStore),

		Artifacts: service.RepoNarrationArtifacts{Files: r.file, Books: r.book},

		// The row delete and the byte delete are one operation, so there is
		// nothing left here to sequence — only the library handle to resolve,
		// which is all this adapter does. The closure that used to stand here
		// composed the order itself and got it wrong: it asked the handle for
		// the book's file after Artifacts had deleted that row, was told "not
		// found", and returned nil, so every deleted narration kept its bytes
		// (#267).
		NarrationBytes: service.NewLibraryNarrationBytes(libStore),
	}
	if w.hub != nil {
		audiobookDeps.Publish = func(bookID string) {
			_ = w.hub.Publish(sse.AudiobookUpdated{BookID: bookID})
		}
	}

	return services{
		libStore:       libStore,
		metadataWriter: metadataWriter,
		lib:            libSvc,
		shelf:          service.NewShelfService(r.shelf, w.hub),
		search:         service.NewSearchService(r.lib, r.book, r.shelf),
		auth:           service.NewAuthService(r.user, r.session, w.hub),
		bdrop:          bdropSvc,
		progress:       service.NewProgressService(r.progress, r.readingSession),
		annotation:     service.NewAnnotationService(r.annotation),
		stats:          service.NewStatsService(r.stats),
		readingStats:   service.NewReadingSessionService(r.readingSession),
		enrich: service.NewEnrichmentService(
			providers, providerSettingsRepo, r.book, w.covers, metadataWriter),
		providerCfg:          service.NewProviderSettingsService(providers, providerSettingsRepo),
		providerSettingsRepo: providerSettingsRepo,
		device: service.NewDeviceService(
			r.device, r.book, libStore,
			service.NewRemarkableDriver(),
		),
		// OIDC — settings live in app_settings so three providers
		// (Google, GitHub, generic OIDC) can be toggled independently
		// without a restart. The rows are seeded by Start.
		oidc:          service.NewOIDCService(r.appSettings, r.user, r.session, r.identity, w.cfg.AppURL),
		fwdAuthHolder: auth.NewForwardAuthHolder(fwdAuthRuntime),
		fwdAuth:       service.NewForwardAuthService(r.appSettings, r.user, r.identity),
		notifier:      notifier,
		reset:         service.NewPasswordResetService(r.user, r.reset, r.session, notifier),
		guideRunner:   service.NewGuideRunner(r.guide, w.enq, guideCfg.TextCap),
		audiobook:     service.NewAudiobookService(audiobookDeps),
		emailTpl:      emailTpl,
	}, nil
}

// buildQueue constructs the background queue, backed by River — not
// started; Start owns that, together with resolving enq.
func buildQueue(ctx context.Context, w wiring, r repos, s services) (queue.Client, error) {
	return queue.New(ctx, w.dbh, queue.Deps{
		BookDropSvc:    s.bdrop,
		Enrich:         s.enrich,
		LibSvc:         s.lib,
		Resolver:       w.resolver,
		LibStore:       s.libStore,
		FileRepo:       r.file,
		Books:          r.book,
		Users:          r.user,
		Notifier:       s.notifier,
		Hub:            w.hub,
		AppSettings:    r.appSettings,
		Guides:         r.guide,
		Renditions:     r.rendition,
		EpubRenditions: r.epubRendition,
		Enqueuer:       w.enq,
		Audiobooks:     r.audiobook,
		AudiobookSvc:   s.audiobook,
		Covers:         w.covers,
		Staging:        w.staging,
	})
}

// buildWatchers constructs the two BookDrop watchers. Both are always
// constructed — Run self-disables — which is what lets the wiring
// parity test hold every App field non-nil.
func buildWatchers(ctx context.Context, w wiring, r repos, s services) (*ingest.Watcher, *ingest.S3Watcher) {
	watcher := &ingest.Watcher{
		Path:     w.cfg.BookDropPath,
		Interval: w.cfg.BookDropInterval,
		Svc:      s.bdrop,
	}

	// S3 BookDrop: a drop zone inside the shared bucket, pulled through
	// the same Accept seam uploads use. Wired only when both the bucket
	// and the prefix are configured, and refused when the prefix would
	// overlap a library's own prefix in the same bucket — the
	// self-eating loop where the watcher ingests library files as drops.
	// A refusal is carried to the watcher via Disable, so its own
	// "disabled" line names the cause (#304).
	s3Watcher := &ingest.S3Watcher{
		Prefix:   w.cfg.SharedS3.BookDropPrefix,
		Interval: w.cfg.SharedS3.BookDropInterval,
		Accept:   s.bdrop.Accept,
	}
	if !w.cfg.SharedS3.Configured() || w.cfg.SharedS3.BookDropPrefix == "" {
		return watcher, s3Watcher
	}

	collision := ""
	if backendRows, err := r.backend.List(ctx); err == nil {
		for _, row := range backendRows {
			if row.Kind != "s3" {
				continue
			}
			bucket, _ := row.Config["bucket"].(string)
			prefix, _ := row.Config["prefix"].(string)
			if bucket == w.cfg.SharedS3.Bucket && ingest.DropPrefixCollides(w.cfg.SharedS3.BookDropPrefix, prefix) {
				collision = prefix
				break
			}
		}
	}
	if collision != "" {
		s3Watcher.Disable(fmt.Sprintf(
			"drop prefix %q overlaps library prefix %q in the same bucket",
			w.cfg.SharedS3.BookDropPrefix, collision))
		return watcher, s3Watcher
	}

	// Rooted at the bucket, not the drop prefix: the watcher passes the
	// prefix to List and works with full keys, so the log lines name the
	// real object paths.
	dropStore, err := s3storage.New(ctx, s3storage.Config{
		Endpoint:        w.cfg.SharedS3.Endpoint,
		Region:          w.cfg.SharedS3.Region,
		Bucket:          w.cfg.SharedS3.Bucket,
		AccessKeyID:     w.cfg.SharedS3.AccessKeyID,
		SecretAccessKey: w.cfg.SharedS3.SecretAccessKey,
		ForcePathStyle:  w.cfg.SharedS3.ForcePathStyle,
		SkipValidation:  true,
	})
	if err != nil {
		s3Watcher.Disable("backend construction failed: " + err.Error())
		return watcher, s3Watcher
	}
	s3Watcher.Store = dropStore
	return watcher, s3Watcher
}

// buildHTTP assembles the gin handler over everything the stages
// built. The five required groups are positional — adding a seam to
// any of them breaks the build here until it is supplied.
func buildHTTP(w wiring, r repos, s services, q queue.Client, version, commit string) *handler.Handler {
	return handler.New(
		handler.NewPlatformDeps(w.cfg, staticfs.FS, version, commit, w.hub, service.NewPlatformService(w.dbh)),
		handler.NewLibraryDeps(s.lib, s.shelf, r.book, s.bdrop, s.progress),
		handler.NewDiscoveryDeps(
			s.enrich, s.providerCfg, s.search,
			s.stats, s.readingStats, s.annotation,
			r.guide, s.guideRunner,
			handler.RenditionDeps{
				Markdown:         r.rendition,
				Epub:             r.epubRendition,
				MarkdownRequests: service.NewMarkdownRequests(r.rendition, w.enq),
				EpubRequests:     service.NewEpubRequests(r.epubRendition, w.enq),
			},
			s.audiobook,
		),
		handler.NewAccountDeps(s.auth, r.user, s.device, r.appSettings),
		handler.NewEmailDeps(s.notifier, s.reset, r.invite, r.cipher, s.emailTpl),
		handler.Options{
			LibStore:      s.libStore,
			OIDC:          s.oidc,
			Identities:    r.identity,
			Covers:        w.covers,
			Comics:        w.comics,
			Queue:         q,
			FwdAuthHolder: s.fwdAuthHolder,
			FwdAuth:       s.fwdAuth,
		},
	)
}
