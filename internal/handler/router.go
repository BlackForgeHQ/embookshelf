package handler

import (
	"io/fs"
	"net/http"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
)

func (h *Handler) Engine() *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	r.Use(cors.New(cors.Config{
		AllowOrigins:     h.cfg.AllowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Accept", "Content-Type", "HX-Request", "HX-Target", "HX-Current-URL", "HX-Boosted"},
		AllowCredentials: true,
	}))

	// CSRF / origin check applies to every state-changing request globally.
	r.Use(auth.CSRFGuard())

	// Embedded static assets (Tailwind output, htmx).
	if staticFS, err := fs.Sub(h.static, "static"); err == nil {
		r.StaticFS("/static", http.FS(staticFS))
	}

	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/app")
	})

	// Public routes.
	r.GET("/login", h.LoginPage)
	r.POST("/login", h.Login)
	r.POST("/logout", h.Logout)
	r.GET("/signup", h.SignupPage)
	r.POST("/signup", h.Signup)

	// Protected HTML app.
	app := r.Group("/app")
	app.Use(auth.RequireAuth(h.auth))
	{
		app.GET("", h.Home)
		app.GET("/", h.Home)
		app.GET("/library", h.Library)

		app.GET("/book/:id", h.BookDetail)
		app.GET("/book/:id/edit", h.BookEdit)
		app.POST("/book/:id", h.BookUpdate)
		app.POST("/book/:id/progress", h.BookProgress)
		app.POST("/book/:id/shelf/:slug", h.BookToggleShelf)

		app.GET("/read/:id", h.BookRead)
		app.GET("/read/:id/file", h.BookFile)

		app.GET("/book/:id/enrich", h.EnrichSearch)
		app.POST("/book/:id/cover-from-url", h.EnrichApplyCover)

		app.GET("/shelf/:slug", h.ShelfView)
		app.POST("/shelves", h.ShelfCreate)
		app.POST("/shelf/:slug/delete", h.ShelfDelete)

		app.GET("/bookdrop", h.BookDrop)
		app.GET("/bookdrop/row/:id", h.BookDropRow)
		app.GET("/bookdrop/:id/cover", h.BookDropCover)
		app.POST("/bookdrop/:id/approve", h.BookDropApprove)
		app.POST("/bookdrop/:id/reject", h.BookDropReject)

		app.GET("/cover/:id", h.BookCover)

		app.GET("/settings", h.SettingsHome)
		app.GET("/settings/libraries", h.SettingsLibraries)
		app.POST("/settings/libraries/paths", h.LibraryPathCreate)
		app.POST("/settings/libraries/paths/:id/delete", h.LibraryPathDelete)
		app.POST("/settings/libraries/paths/:id/scan", h.LibraryPathScan)
	}

	// SSE stream (also protected).
	events := r.Group("/events")
	events.Use(auth.RequireAuth(h.auth))
	events.GET("", h.Events)

	// Public JSON API (healthcheck is unauthenticated on purpose).
	api := r.Group("/api/v1")
	{
		api.GET("/healthcheck", h.Healthcheck)
	}

	// OPDS catalog for e-reader apps. Basic Auth instead of the cookie
	// session — clients (KOReader, Moon+ Reader, FBReader, ...) don't
	// maintain session state.
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

	return r
}
