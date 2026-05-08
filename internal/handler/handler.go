package handler

import (
	"embed"

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
	cfg          config.Config
	static       embed.FS
	version      string
	commit       string
	lib          *service.LibraryService
	shelf        *service.ShelfService
	auth         *service.AuthService
	bookdrop     *service.BookDropService
	progress     *service.ProgressService
	enrich       *service.EnrichmentService
	annotations  *service.AnnotationService
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
	// without a restart. resetRepo + inviteRepo + users back the token
	// flows; cipher + emailTpl back the admin "send test email"
	// endpoint and any handler-side render. ADR-0020.
	users      *repo.UserRepo
	books      *repo.BookRepo
	notifier   *service.Notifier
	resetRepo  *repo.PasswordResetTokenRepo
	inviteRepo *repo.UserInviteRepo
	cipher     crypto.Cipher
	emailTpl   *email.Templates
}

type Deps struct {
	Cfg          config.Config
	Static       embed.FS
	Version      string
	Commit       string
	Lib          *service.LibraryService
	Shelf        *service.ShelfService
	Auth         *service.AuthService
	BookDrop     *service.BookDropService
	Progress     *service.ProgressService
	Enrich       *service.EnrichmentService
	Annotations  *service.AnnotationService
	Stats        *service.StatsService
	ReadingStats *service.ReadingSessionService
	Devices      *service.DeviceService
	OIDC         *service.OIDCService
	Identities   *repo.IdentityRepo
	Search       *service.SearchService
	AppSettings  *repo.AppSettingsRepo
	Covers       *coverstore.Store
	Hub          *sse.Hub
	Queue        queue.Client
	// LibStore powers the presign-vs-local decision in serveBookFile
	// and any other library-aware lookup. Optional — omitting it falls
	// back to local c.File() serving for every book.
	LibStore service.LibraryStore
	// Email subsystem seams. Notifier is always wired; its runtime
	// state determines whether email features are enabled. Disabled
	// handlers return 503 EMAIL_DISABLED via emailEnabled(). ADR-0020.
	Users      *repo.UserRepo
	Books      *repo.BookRepo
	Notifier   *service.Notifier
	ResetRepo  *repo.PasswordResetTokenRepo
	InviteRepo *repo.UserInviteRepo
	Cipher     crypto.Cipher
	EmailTpl   *email.Templates
}

func New(d Deps) *Handler {
	return &Handler{
		cfg: d.Cfg, static: d.Static,
		version: d.Version, commit: d.Commit,
		lib: d.Lib, shelf: d.Shelf, auth: d.Auth,
		bookdrop: d.BookDrop, progress: d.Progress, enrich: d.Enrich,
		annotations: d.Annotations, stats: d.Stats,
		readingStats: d.ReadingStats,
		devices:      d.Devices,
		oidc:         d.OIDC,
		identities:   d.Identities,
		search:       d.Search,
		appSettings:  d.AppSettings,
		covers:       d.Covers,
		hub:          d.Hub, queue: d.Queue,
		libStore:   d.LibStore,
		users:      d.Users,
		books:      d.Books,
		notifier:   d.Notifier,
		resetRepo:  d.ResetRepo,
		inviteRepo: d.InviteRepo,
		cipher:     d.Cipher,
		emailTpl:   d.EmailTpl,
	}
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
