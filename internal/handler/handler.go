// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"embed"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/coverstore"
	"github.com/blackforge/embookshelf/internal/crypto"
	"github.com/blackforge/embookshelf/internal/email"
	"github.com/blackforge/embookshelf/internal/queue"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/sse"
)

// Ensure *service.OIDCService satisfies the nil-safe pattern used in the handler.

type Handler struct {
	cfg           config.Config
	static        embed.FS
	version       string
	commit        string
	lib           *service.LibraryService
	shelf         *service.ShelfService
	auth          *service.AuthService
	bookdrop      *service.BookDropService
	progress      *service.ProgressService
	enrich        *service.EnrichmentService
	providerCfg   *service.ProviderSettingsService
	annotations   *service.AnnotationService
	guides        *repo.BookReadingGuideRepo
	guideRunner   *service.GuideRunner
	audiobooks    *service.AudiobookService
	audiobookRepo *repo.BookAudiobookRepo
	// oidcSettings applies a settings submission as one decision (#195).
	oidcSettings *service.OIDCSettingsService
	stats        *service.StatsService
	readingStats *service.ReadingSessionService
	devices      *service.DeviceService
	oidc         *service.OIDCService
	identities   *repo.IdentityRepo
	search       *service.SearchService
	appSettings  *repo.AppSettingsRepo
	covers       *coverstore.Store
	hub          *sse.Hub
	queue        queue.Client
	// libStore powers the file-serve path's BookSource decision
	// (presign vs. local) and any other library-aware lookup. nil on
	// installs that haven't configured a storage backend — serveBookFile
	// falls back to local serving.
	libStore service.LibraryStore
	// Email subsystem seams. notifier is always non-nil — its runtime
	// state holds whether SMTP is wired. emailEnabled() consults
	// notifier.Enabled() so admin edits to the EMAIL row hot-reload
	// without a restart. resets + inviteRepo + users back the token
	// flows; cipher + emailTpl back the admin "send test email"
	// endpoint and any handler-side render. ADR-0020.
	users *repo.UserRepo
	// books is the catalog behind the book-scoped seam. Held as the
	// bookStore interface rather than *repo.BookRepo so a book-scoped
	// handler body can be driven against a fake.
	books      bookStore
	notifier   *service.Notifier
	resets     *service.PasswordResetService
	inviteRepo *repo.UserInviteRepo
	cipher     crypto.Cipher
	emailTpl   *email.Templates
	// Forward-auth (ADR-0022). fwdAuthHolder carries the runtime
	// CIDR/header config — atomically swappable so settings saves
	// hot-reload without a restart. fwdAuth is the resolver invoked
	// by the middleware on a trusted-IP hit.
	fwdAuthHolder *auth.ForwardAuthHolder
	fwdAuth       auth.ForwardAuthResolver
}

// The Handler's dependencies are split by whether the code actually
// tolerates their absence.
//
// Required seams are constructor arguments — Platform, Library,
// Discovery, Account and Email each take theirs positionally, so leaving
// one out is a compile error at the composition root. This exists
// because it did not: ProviderCfg was declared on a 31-field Deps struct
// and never assigned, and the zero value sailed through to a nil
// dereference on every provider settings request.
//
// Options holds the seams that genuinely may be nil. Every one of them is
// nil-guarded at its use site, and that guard is a deliberate degrade —
// no storage backend configured, no OIDC provider set up, no worker pool.

// Platform carries process-level facts and the two fan-out primitives
// every surface can reach for.
type PlatformDeps struct {
	cfg     config.Config
	static  embed.FS
	version string
	commit  string
	hub     *sse.Hub
}

// NewPlatform builds the platform group.
func NewPlatformDeps(cfg config.Config, static embed.FS, version, commit string, hub *sse.Hub) PlatformDeps {
	return PlatformDeps{cfg: cfg, static: static, version: version, commit: commit, hub: hub}
}

// Library carries the book-and-shelf surfaces: the catalog, its
// containers, the ingest staging area and per-user reading position.
type LibraryDeps struct {
	lib      *service.LibraryService
	shelf    *service.ShelfService
	books    *repo.BookRepo
	bookdrop *service.BookDropService
	progress *service.ProgressService
}

// NewLibrary builds the library group.
func NewLibraryDeps(
	lib *service.LibraryService,
	shelf *service.ShelfService,
	books *repo.BookRepo,
	bookdrop *service.BookDropService,
	progress *service.ProgressService,
) LibraryDeps {
	return LibraryDeps{lib: lib, shelf: shelf, books: books, bookdrop: bookdrop, progress: progress}
}

// Discovery carries the surfaces that find, describe or measure books
// rather than storing them.
type DiscoveryDeps struct {
	enrich       *service.EnrichmentService
	providerCfg  *service.ProviderSettingsService
	search       *service.SearchService
	stats        *service.StatsService
	readingStats *service.ReadingSessionService
	annotations  *service.AnnotationService
	guides       *repo.BookReadingGuideRepo
	guideRunner  *service.GuideRunner
	// Audiobook generation (ADR-0025 — ADR-0028). Narration is discovery
	// in the same sense a reading guide is: derived from a book rather
	// than stored with it.
	audiobooks    *service.AudiobookService
	audiobookRepo *repo.BookAudiobookRepo
}

// NewDiscovery builds the discovery group.
func NewDiscoveryDeps(
	enrich *service.EnrichmentService,
	providerCfg *service.ProviderSettingsService,
	search *service.SearchService,
	stats *service.StatsService,
	readingStats *service.ReadingSessionService,
	annotations *service.AnnotationService,
	guides *repo.BookReadingGuideRepo,
	guideRunner *service.GuideRunner,
	audiobooks *service.AudiobookService,
	audiobookRepo *repo.BookAudiobookRepo,
) DiscoveryDeps {
	return DiscoveryDeps{
		enrich: enrich, providerCfg: providerCfg, search: search,
		stats: stats, readingStats: readingStats, annotations: annotations,
		guides: guides, guideRunner: guideRunner,
		audiobooks: audiobooks, audiobookRepo: audiobookRepo,
	}
}

// Account carries identity, instance settings and the user's own
// devices.
type AccountDeps struct {
	auth        *service.AuthService
	users       *repo.UserRepo
	devices     *service.DeviceService
	appSettings *repo.AppSettingsRepo
}

// NewAccount builds the account group.
func NewAccountDeps(
	auth *service.AuthService,
	users *repo.UserRepo,
	devices *service.DeviceService,
	appSettings *repo.AppSettingsRepo,
) AccountDeps {
	return AccountDeps{auth: auth, users: users, devices: devices, appSettings: appSettings}
}

// Email carries the delivery subsystem (ADR-0020). Notifier is always
// non-nil; whether email is actually on is runtime state it holds, which
// emailEnabled() consults so admin edits hot-reload without a restart.
type EmailDeps struct {
	notifier   *service.Notifier
	resets     *service.PasswordResetService
	inviteRepo *repo.UserInviteRepo
	cipher     crypto.Cipher
	emailTpl   *email.Templates
}

// NewEmail builds the email group.
func NewEmailDeps(
	notifier *service.Notifier,
	resets *service.PasswordResetService,
	inviteRepo *repo.UserInviteRepo,
	cipher crypto.Cipher,
	emailTpl *email.Templates,
) EmailDeps {
	return EmailDeps{notifier: notifier, resets: resets, inviteRepo: inviteRepo, cipher: cipher, emailTpl: emailTpl}
}

// Options are the seams that may legitimately be absent. Each is
// nil-guarded where it is used, and nil selects a documented fallback
// rather than an error.
type Options struct {
	// LibStore powers the file-serve path's presign-vs-local decision.
	// nil on installs with no storage backend configured — serveBookFile
	// falls back to local serving.
	LibStore service.LibraryStore
	// OIDC and Identities are nil until an admin configures a provider.
	OIDC       *service.OIDCService
	Identities *repo.IdentityRepo
	// Covers is the pre-approval cover store; nil disables cover serving.
	Covers *coverstore.Store
	// Queue is the worker pool. nil means jobs are not dispatched.
	Queue queue.Client
	// FwdAuthHolder carries the runtime CIDR/header config, atomically
	// swappable so settings saves hot-reload. FwdAuth is the resolver the
	// middleware invokes on a trusted-IP hit. Both nil disables
	// forward-auth and every request falls through to RequireAuth.
	// ADR-0022.
	FwdAuthHolder *auth.ForwardAuthHolder
	FwdAuth       auth.ForwardAuthResolver
}

// New assembles the Handler. The five required groups are positional, so
// a seam added to any of them fails the build until the composition root
// supplies it.
func New(p PlatformDeps, l LibraryDeps, d DiscoveryDeps, a AccountDeps, e EmailDeps, opts Options) *Handler {
	return &Handler{
		cfg: p.cfg, static: p.static, version: p.version, commit: p.commit, hub: p.hub,

		lib: l.lib, shelf: l.shelf, books: l.books, bookdrop: l.bookdrop, progress: l.progress,

		enrich: d.enrich, providerCfg: d.providerCfg, search: d.search,
		stats: d.stats, readingStats: d.readingStats, annotations: d.annotations,
		guides: d.guides, guideRunner: d.guideRunner,
		audiobooks: d.audiobooks, audiobookRepo: d.audiobookRepo,

		auth: a.auth, users: a.users, devices: a.devices, appSettings: a.appSettings,

		notifier: e.notifier, resets: e.resets, inviteRepo: e.inviteRepo,
		cipher: e.cipher, emailTpl: e.emailTpl,

		libStore: opts.LibStore, oidc: opts.OIDC, identities: opts.Identities,
		oidcSettings: newOIDCSettingsService(a.appSettings, opts.OIDC),
		covers:       opts.Covers, queue: opts.Queue,
		fwdAuthHolder: opts.FwdAuthHolder, fwdAuth: opts.FwdAuth,
	}
}

// newOIDCSettingsService wires the module that applies an OIDC settings
// submission, or nothing when there is no settings repo to write to.
//
// The guard is passed as a typed nil-check rather than straight through:
// a nil *OIDCService in an interface field is not a nil interface, and
// the module would call it.
func newOIDCSettingsService(
	appSettings *repo.AppSettingsRepo,
	oidc *service.OIDCService,
) *service.OIDCSettingsService {
	if appSettings == nil {
		return nil
	}
	rows := service.AppSettingsOIDCRows{Repo: appSettings}
	if oidc == nil {
		return service.NewOIDCSettingsService(rows, nil)
	}
	return service.NewOIDCSettingsService(rows, oidc)
}

// emailEnabled reports whether the email subsystem is wired and on.
// Handlers gate the feature on this so feature-disabled installs
// return 503 EMAIL_DISABLED uniformly. Reload via SettingsEmailUpdate
// flips the runtime state without a restart. ADR-0020.
func (h *Handler) emailEnabled() bool {
	return h.notifier != nil && h.notifier.Enabled()
}

// Secure reports whether the session cookie should be marked Secure.
func (h *Handler) Secure() bool { return false }
