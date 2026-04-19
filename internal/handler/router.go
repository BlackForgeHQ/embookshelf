package handler

import (
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
)

func (h *Handler) Engine() *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	r.Use(cors.New(cors.Config{
		AllowOrigins:     h.cfg.AllowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Accept", "Content-Type", "X-Requested-With"},
		AllowCredentials: true,
	}))

	r.Use(auth.CSRFGuard(h.cfg.AllowedOrigins))

	api := r.Group("/api/v1")
	{
		api.GET("/healthcheck", h.Healthcheck)

		// Auth surface — cookie-based sessions. Login/signup/logout stay
		// public; /me requires a valid session.
		api.POST("/auth/login", h.Login)
		api.POST("/auth/logout", h.Logout)
		api.GET("/auth/signup", h.SignupStatus)
		api.POST("/auth/signup", h.Signup)

		authed := api.Group("")
		authed.Use(auth.RequireAuth(h.auth))
		{
			authed.GET("/me", h.Me)
			authed.PATCH("/me", h.AccountUpdateName)
			authed.POST("/me/password", h.AccountChangePassword)
			authed.GET("/instance", h.InstanceSummary)

			// Library + catalog
			authed.GET("/libraries", h.Libraries)
			authed.GET("/books", h.Books)
			authed.GET("/books/:id", h.BookDetail)
			authed.GET("/books/:id/cover", h.BookCover)
			authed.GET("/books/:id/file", h.BookFile)
			authed.PATCH("/books/:id", h.BookPatch)
			authed.POST("/books/:id/progress", h.BookProgressUpdate)
			authed.POST("/books/:id/shelves/:slug", h.BookAddShelf)
			authed.DELETE("/books/:id/shelves/:slug", h.BookRemoveShelf)

			// Per-user shelves (regular + smart)
			authed.GET("/shelves", h.Shelves)
			authed.POST("/shelves", h.ShelfCreate)
			authed.PATCH("/shelves/:slug", h.ShelfUpdate)
			authed.DELETE("/shelves/:slug", h.ShelfDelete)

			// BookDrop ingest queue
			authed.GET("/bookdrop", h.BookDropList)
			authed.GET("/bookdrop/:id/cover", h.BookDropCover)
			authed.POST("/bookdrop/upload", h.BookDropUpload)
			authed.POST("/bookdrop/:id/approve", h.BookDropApprove)
			authed.POST("/bookdrop/:id/reject", h.BookDropReject)

			// Metadata enrichment
			authed.GET("/books/:id/enrich", h.EnrichSearch)
			authed.POST("/books/:id/cover-from-url", h.EnrichApplyCover)

			// Library statistics dashboard
			authed.GET("/stats", h.Stats)
			authed.GET("/stats/reading", h.ReadingStats)

			// Device sync (reMarkable Paper Pro, ...) — per-user
			// destinations you can push books to.
			authed.GET("/devices", h.Devices)
			authed.POST("/devices", h.DevicePair)
			authed.DELETE("/devices/:id", h.DeviceDelete)
			authed.POST("/books/:id/send/:deviceId", h.BookSendToDevice)

			// Annotations (highlights + notes)
			authed.GET("/annotations", h.AnnotationsRecent)
			authed.GET("/books/:id/annotations", h.AnnotationsForBook)
			authed.POST("/books/:id/annotations", h.AnnotationCreate)
			authed.PATCH("/annotations/:id", h.AnnotationPatch)
			authed.DELETE("/annotations/:id", h.AnnotationDelete)

			// Settings — instance-wide config surfaces. RequireRole
			// stacks on top of RequireAuth so non-admins get 403 cleanly
			// instead of the whole API returning 401.
			admin := authed.Group("/settings")
			admin.Use(auth.RequireRole(model.RoleAdmin))
			{
				admin.GET("/instance", h.InstanceInfo)

				admin.GET("/libraries", h.SettingsLibraries)
				admin.POST("/libraries/paths", h.SettingsLibraryPathCreate)
				admin.DELETE("/libraries/paths/:id", h.SettingsLibraryPathDelete)
				admin.POST("/libraries/paths/:id/scan", h.SettingsLibraryPathScan)

				admin.GET("/users", h.SettingsUsersList)
				admin.POST("/users", h.SettingsUsersCreate)
				admin.PATCH("/users/:id/role", h.SettingsUsersUpdateRole)
				admin.DELETE("/users/:id", h.SettingsUsersDelete)
			}
		}
	}

	// Server-Sent Events stream — cookie-authed, but mounted off /api/v1
	// because browsers don't carry trailing-slash semantics through
	// EventSource reliably and the TS client expects a bare `/events`.
	events := r.Group("/events")
	events.Use(auth.RequireAuth(h.auth))
	{
		events.GET("", h.Events)
	}

	// OPDS catalog for e-reader apps. Basic Auth — clients (KOReader,
	// Moon+ Reader, FBReader, ...) don't maintain session state.
	opds := r.Group("/opds")
	opds.Use(auth.BasicAuth(h.auth, "embookshelf"))
	{
		opds.GET("", h.OPDSRoot)
		opds.GET("/", h.OPDSRoot)
		opds.GET("/all", h.OPDSAll)
		opds.GET("/library/:slug", h.OPDSLibrary)
		opds.GET("/recent", h.OPDSRecent)
		opds.GET("/search", h.OPDSSearch)
		opds.GET("/search.xml", h.OPDSSearchDescription)
		opds.GET("/book/:id/download", h.OPDSDownload)
		opds.GET("/cover/:id", h.OPDSCover)
	}

	h.mountSPA(r)

	return r
}

// mountSPA serves the compiled React bundle from the embedded filesystem.
// Static assets are served directly; any unmatched GET under a non-API
// prefix falls back to index.html so the client-side router can resolve it.
func (h *Handler) mountSPA(r *gin.Engine) {
	dist, err := fs.Sub(h.static, "dist")
	if err != nil {
		return
	}
	fileServer := http.FileServer(http.FS(dist))

	r.NoRoute(func(c *gin.Context) {
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Status(http.StatusMethodNotAllowed)
			return
		}
		p := strings.TrimPrefix(c.Request.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if f, err := dist.Open(path.Clean(p)); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}
		index, err := dist.Open("index.html")
		if err != nil {
			c.String(http.StatusNotFound, "frontend bundle not built — run `npm run build` in web/")
			return
		}
		defer index.Close()
		c.Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Gin seeds NoRoute responses with 404; override to 200 so the SPA
		// shell is served with a success status on deep links like /login.
		c.Writer.WriteHeader(http.StatusOK)
		_, _ = io.Copy(c.Writer, index)
	})
}
