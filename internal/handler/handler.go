package handler

import (
	"embed"
	"time"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/coverstore"
	"github.com/blackforge/embookshelf/internal/queue"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/sse"
	"github.com/blackforge/embookshelf/internal/storage"
)

// Ensure *service.OIDCService satisfies the nil-safe pattern used in the handler.

type Handler struct {
	cfg          config.Config
	static       embed.FS
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
	search       *service.SearchService
	appSettings  *repo.AppSettingsRepo
	covers       *coverstore.Store
	hub          *sse.Hub
	queue        queue.Client
	// Storage resolver + file repo for the presign redirect path.
	// nil on installs that have not configured a storage backend.
	resolver   storage.Resolver
	libRepo    *repo.LibraryRepo
	files      *repo.FileRepo
	presignTTL time.Duration
}

type Deps struct {
	Cfg          config.Config
	Static       embed.FS
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
	Search       *service.SearchService
	AppSettings  *repo.AppSettingsRepo
	Covers       *coverstore.Store
	Hub          *sse.Hub
	Queue        queue.Client
	// Resolver, LibRepo, FileRepo, and PresignTTL power the presign
	// redirect path. All are optional — omitting them falls back to
	// local c.File() serving for every book.
	Resolver   storage.Resolver
	LibRepo    *repo.LibraryRepo
	FileRepo   *repo.FileRepo
	PresignTTL time.Duration
}

func New(d Deps) *Handler {
	return &Handler{
		cfg: d.Cfg, static: d.Static,
		lib: d.Lib, shelf: d.Shelf, auth: d.Auth,
		bookdrop: d.BookDrop, progress: d.Progress, enrich: d.Enrich,
		annotations: d.Annotations, stats: d.Stats,
		readingStats: d.ReadingStats,
		devices:      d.Devices,
		oidc:         d.OIDC,
		search:       d.Search,
		appSettings:  d.AppSettings,
		covers:       d.Covers,
		hub:          d.Hub, queue: d.Queue,
		resolver:   d.Resolver,
		libRepo:    d.LibRepo,
		files:      d.FileRepo,
		presignTTL: d.PresignTTL,
	}
}

// Secure reports whether the session cookie should be marked Secure.
func (h *Handler) Secure() bool { return false }
