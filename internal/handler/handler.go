package handler

import (
	"embed"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/coverstore"
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
		libStore: d.LibStore,
	}
}

// Secure reports whether the session cookie should be marked Secure.
func (h *Handler) Secure() bool { return false }
