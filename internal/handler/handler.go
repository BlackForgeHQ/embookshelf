package handler

import (
	"embed"
	"net/http"

	"github.com/a-h/templ"
	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/coverstore"
	"github.com/blackforge/embookshelf/internal/queue"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/sse"
)

type Handler struct {
	cfg       config.Config
	static    embed.FS
	lib       *service.LibraryService
	shelf     *service.ShelfService
	auth      *service.AuthService
	bookdrop  *service.BookDropService
	progress  *service.ProgressService
	enrich    *service.EnrichmentService
	libPath   *service.LibraryPathService
	covers    *coverstore.Store
	hub       *sse.Hub
	queue     queue.Client
}

type Deps struct {
	Cfg      config.Config
	Static   embed.FS
	Lib      *service.LibraryService
	Shelf    *service.ShelfService
	Auth     *service.AuthService
	BookDrop *service.BookDropService
	Progress *service.ProgressService
	Enrich   *service.EnrichmentService
	LibPath  *service.LibraryPathService
	Covers   *coverstore.Store
	Hub      *sse.Hub
	Queue    queue.Client
}

func New(d Deps) *Handler {
	return &Handler{
		cfg: d.Cfg, static: d.Static,
		lib: d.Lib, shelf: d.Shelf, auth: d.Auth,
		bookdrop: d.BookDrop, progress: d.Progress, enrich: d.Enrich,
		libPath: d.LibPath, covers: d.Covers,
		hub: d.Hub, queue: d.Queue,
	}
}

// Secure reports whether the session cookie should be marked Secure.
func (h *Handler) Secure() bool { return false }

func currentUserID(c *gin.Context) string {
	u := auth.UserFromContext(c.Request.Context())
	if u == nil {
		return ""
	}
	return u.ID
}

func requireUser(c *gin.Context) string {
	id := currentUserID(c)
	if id == "" {
		c.AbortWithStatus(http.StatusUnauthorized)
	}
	return id
}

func render(c *gin.Context, component templ.Component) {
	c.Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = component.Render(c.Request.Context(), c.Writer)
}
