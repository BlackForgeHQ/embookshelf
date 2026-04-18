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

	r.Use(auth.CSRFGuard())

	api := r.Group("/api/v1")
	{
		api.GET("/healthcheck", h.Healthcheck)
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
		_, _ = io.Copy(c.Writer, index)
	})
}
