// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
)

func (h *Handler) Engine() *gin.Engine {
	r := gin.New()
	// otelgin first so the span wrapping every request starts before the
	// logger/recovery middleware runs. No-op when the global tracer
	// provider is the default (OTEL_ENABLED=false).
	if h.cfg.OTELEnabled {
		r.Use(otelgin.Middleware(h.cfg.OTELServiceName))
	}
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	r.Use(cors.New(cors.Config{
		AllowOrigins:     h.cfg.AllowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Accept", "Content-Type", "X-Requested-With"},
		AllowCredentials: true,
	}))

	// Forward-auth runs before CSRFGuard so a trusted-IP hit can mark
	// the request and skip the origin check. No-op when disabled or
	// when the source IP is outside TrustedProxyCIDRs. ADR-0022.
	if h.fwdAuthHolder != nil && h.fwdAuth != nil {
		r.Use(auth.ForwardAuth(h.fwdAuthHolder, h.fwdAuth))
	}

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

		// Password reset + invite acceptance. Public — pre-auth surface
		// gated by token possession. ADR-0020.
		api.POST("/auth/password-reset/request", h.PasswordResetRequest)
		api.GET("/auth/password-reset/verify", h.PasswordResetVerify)
		api.POST("/auth/password-reset/confirm", h.PasswordResetConfirm)
		api.POST("/auth/invites/accept", h.InviteAccept)

		// OIDC / SSO — each provider has its own /auth/oidc/:slug
		// entrypoint (slug ∈ { google | github | generic }). The
		// callback is shared; the state token carries the slug so the
		// service knows which provider issued it.
		api.GET("/auth/oidc/config", h.OIDCConfig)
		api.GET("/auth/oidc/callback", h.OIDCCallback)
		api.GET("/auth/oidc/:slug", h.OIDCLogin)

		authed := api.Group("")
		authed.Use(auth.RequireAuth(h.auth))
		{
			authed.GET("/me", h.Me)
			authed.PATCH("/me", h.AccountUpdateName)
			authed.POST("/me/password", h.AccountChangePassword)
			authed.GET("/instance", h.InstanceSummary)
			authed.GET("/config", h.AppConfig)

			// Account identity management — list links, start a link
			// flow, unlink with the lockout guard, set initial
			// password for OIDC-provisioned users.
			authed.GET("/account/identities", h.AccountIdentities)
			authed.GET("/account/oidc/link/:slug", h.AccountOIDCLink)
			authed.DELETE("/account/oidc/:provider", h.AccountOIDCUnlink)
			authed.POST("/account/password/set", h.AccountSetInitialPassword)
			authed.PUT("/account/kindle-email", h.AccountKindleEmailUpdate)

			// Cross-entity search powering the global command palette
			// and the library page combobox.
			authed.GET("/search", h.userScoped(h.Search))

			// Library + catalog
			authed.GET("/libraries", h.Libraries)
			authed.GET("/books", h.userScoped(h.Books))
			authed.GET("/books/:id", h.bookScoped(h.BookDetail))
			authed.GET("/books/:id/cover", h.bookScoped(h.BookCover))
			authed.GET("/books/:id/file", h.bookScoped(h.BookFile))
			authed.GET("/books/:id/pages", h.bookScoped(h.ComicPagesIndex))
			authed.GET("/books/:id/pages/:n", h.bookScoped(h.ComicPage))
			authed.PATCH("/books/:id", h.bookScoped(h.BookPatch))
			authed.DELETE("/books/:id", auth.RequireRole(model.RoleAdmin), h.bookScoped(h.BookDelete))
			authed.POST("/books/:id/progress", h.bookScoped(h.BookProgressUpdate))
			authed.POST("/books/:id/shelves/:slug", h.bookScoped(h.BookAddShelf))
			authed.DELETE("/books/:id/shelves/:slug", h.bookScoped(h.BookRemoveShelf))

			// Per-user shelves (regular + smart). Public-shelf routing
			// is folded into the same paths via the `public:<slug>`
			// prefix (ADR-0017); the publish toggle is a separate
			// admin-gated endpoint.
			authed.GET("/shelves", h.userScoped(h.Shelves))
			authed.POST("/shelves", h.userScoped(h.ShelfCreate))
			authed.PATCH("/shelves/:slug", h.userScoped(h.ShelfUpdate))
			authed.DELETE("/shelves/:slug", h.userScoped(h.ShelfDelete))
			authed.PUT("/shelves/:slug/publish", h.ShelfPublish)

			// BookDrop ingest queue
			authed.GET("/bookdrop", h.userScoped(h.BookDropList))
			authed.GET("/bookdrop/:id/cover", h.userScoped(h.BookDropCover))
			authed.PUT("/bookdrop/:id/cover", h.userScoped(h.BookDropPutCover))
			authed.GET("/bookdrop/:id/file", h.userScoped(h.BookDropFile))
			authed.POST("/bookdrop/upload", h.userScoped(h.BookDropUpload))
			authed.POST("/bookdrop/:id/approve", h.userScoped(h.BookDropApprove))
			authed.POST("/bookdrop/:id/reject", h.userScoped(h.BookDropReject))

			// Metadata enrichment
			authed.GET("/books/:id/enrich", h.bookScoped(h.EnrichSearch))
			authed.GET("/books/:id/enrich/stream", h.bookScoped(h.EnrichStream))
			authed.PUT("/books/:id/metadata", h.bookScoped(h.EnrichApplyMatch))
			authed.PUT("/books/:id/metadata/locks", h.bookScoped(h.EnrichToggleFieldLocks))
			authed.POST("/books/metadata/isbn-lookup", h.userScoped(h.EnrichISBNLookup))
			authed.POST("/books/:id/cover-from-url", h.bookScoped(h.EnrichApplyCover))
			authed.DELETE("/books/:id/cover", h.bookScoped(h.EnrichRemoveCover))

			// Reading guides (ADR-0024). Generation is queued, so the
			// POST answers 202 and guide.updated lands over SSE.
			//
			// Reading a guide is open to any signed-in user; writing one
			// is admin-only, for the same two reasons the audiobook
			// routes below are. Generating spends the instance's LLM
			// key, and a guide is per-book rather than per-user, so one
			// reader's regenerate or hand-edit overwrites what everyone
			// else sees. The bulk run is already admin-gated under
			// /settings/reading-guide/run.
			authed.GET("/books/:id/guide", h.bookScoped(h.BookGuideGet))
			authed.POST("/books/:id/guide",
				auth.RequireRole(model.RoleAdmin), h.bookScoped(h.BookGuideGenerate))
			authed.PUT("/books/:id/guide",
				auth.RequireRole(model.RoleAdmin), h.bookScoped(h.BookGuideEdit))

			// Markdown renditions (ADR-0033). Same read/spend split as
			// the guide: any signed-in user may look, converting spends
			// sidecar CPU and is the admin's call.
			authed.GET("/books/:id/markdown", h.bookScoped(h.BookMarkdownGet))
			authed.GET("/books/:id/markdown/file", h.bookScoped(h.BookMarkdownDownload))

			// Generated EPUBs (ADR-0034). Download rides BookFile's
			// ?rendition=epub selector, beside the audio one.
			authed.GET("/books/:id/epub", h.bookScoped(h.BookEpubGet))
			authed.POST("/books/:id/epub",
				auth.RequireRole(model.RoleAdmin), h.bookScoped(h.BookEpubGenerate))
			authed.POST("/books/:id/markdown",
				auth.RequireRole(model.RoleAdmin), h.bookScoped(h.BookMarkdownGenerate))

			// Generated narration (ADR-0025 — ADR-0028). Reading the
			// status is open to any signed-in user, because anyone can
			// play a finished audiobook; everything that spends money is
			// admin-gated, since only the key's owner should be able to
			// spend it (ADR-0028 §1). There is deliberately no bulk run.
			authed.GET("/books/:id/audiobook", h.bookScoped(h.BookAudiobookGet))
			authed.GET("/books/:id/audiobook/estimate",
				auth.RequireRole(model.RoleAdmin), h.bookScoped(h.BookAudiobookEstimate))
			authed.POST("/books/:id/audiobook",
				auth.RequireRole(model.RoleAdmin), h.bookScoped(h.BookAudiobookGenerate))
			authed.POST("/books/:id/audiobook/cancel",
				auth.RequireRole(model.RoleAdmin), h.bookScoped(h.BookAudiobookCancel))
			authed.POST("/books/:id/audiobook/retry",
				auth.RequireRole(model.RoleAdmin), h.bookScoped(h.BookAudiobookRetry))
			authed.DELETE("/books/:id/audiobook",
				auth.RequireRole(model.RoleAdmin), h.bookScoped(h.BookAudiobookDelete))

			// Library statistics dashboard
			authed.GET("/stats", h.userScoped(h.Stats))
			authed.GET("/stats/reading", h.userScoped(h.ReadingStats))

			// Send-to-Kindle delivery. Email subsystem must be on; the
			// handler returns 503 EMAIL_DISABLED otherwise. ADR-0021.
			authed.POST("/books/:id/send-to-kindle", h.bookScoped(h.SendToKindle))

			// Device sync (reMarkable Paper Pro, ...) — per-user
			// destinations you can push books to.
			authed.GET("/devices", h.userScoped(h.Devices))
			authed.POST("/devices", h.userScoped(h.DevicePair))
			authed.DELETE("/devices/:id", h.userScoped(h.DeviceDelete))
			authed.POST("/books/:id/send/:deviceId", h.bookScoped(h.BookSendToDevice))

			// Annotations (highlights + notes)
			authed.GET("/annotations", h.userScoped(h.AnnotationsRecent))
			authed.GET("/books/:id/annotations", h.bookScoped(h.AnnotationsForBook))
			authed.POST("/books/:id/annotations", h.bookScoped(h.AnnotationCreate))
			authed.PATCH("/annotations/:id", h.userScoped(h.AnnotationPatch))
			authed.DELETE("/annotations/:id", h.userScoped(h.AnnotationDelete))

			// Settings — instance-wide config surfaces. RequireRole
			// stacks on top of RequireAuth so non-admins get 403 cleanly
			// instead of the whole API returning 401.
			admin := authed.Group("/settings")
			admin.Use(auth.RequireRole(model.RoleAdmin))
			{
				admin.GET("/instance", h.InstanceInfo)

				admin.GET("/libraries", h.SettingsLibraries)
				admin.POST("/libraries", h.SettingsLibraryCreate)
				admin.POST("/libraries/:id/rescan", h.SettingsLibraryRescan)
				admin.DELETE("/libraries/:id", h.SettingsLibraryDelete)

				admin.GET("/reading-guide", h.SettingsReadingGuideGet)
				admin.PUT("/reading-guide", h.SettingsReadingGuideUpdate)
				admin.POST("/reading-guide/test", h.SettingsReadingGuideTest)
				admin.GET("/reading-guide/estimate", h.SettingsReadingGuideEstimate)
				admin.POST("/reading-guide/run", h.SettingsReadingGuideRun)
				admin.GET("/audiobook", h.SettingsAudiobookGet)
				admin.PUT("/audiobook", h.SettingsAudiobookUpdate)
				admin.GET("/audiobook/voices", h.SettingsAudiobookVoices)
				admin.POST("/audiobook/test", h.SettingsAudiobookTest)

				admin.GET("/providers", h.SettingsProvidersList)
				admin.PATCH("/providers/:id", h.SettingsProviderUpdate)

				admin.GET("/metadata", h.SettingsMetadataGet)
				admin.PUT("/metadata", h.SettingsMetadataUpdate)

				admin.GET("/oidc", h.SettingsOIDCGet)
				admin.PUT("/oidc", h.SettingsOIDCUpdate)
				admin.POST("/oidc/test/:slug", h.SettingsOIDCTest)

				admin.GET("/converter", h.SettingsConverterGet)
				admin.PUT("/converter", h.SettingsConverterUpdate)
				admin.GET("/converter/health", h.SettingsConverterHealth)
				admin.GET("/converter/coverage", h.SettingsConverterCoverage)
				admin.POST("/converter/run", h.SettingsConverterRun)

				admin.GET("/forward-auth", h.SettingsForwardAuthGet)
				admin.PUT("/forward-auth", h.SettingsForwardAuthUpdate)

				admin.GET("/email", h.SettingsEmailGet)
				admin.PUT("/email", h.SettingsEmailUpdate)
				admin.POST("/email/test", h.SettingsEmailTest)

				admin.GET("/invites", h.AdminInvitesList)
				admin.POST("/invites", h.AdminInviteCreate)
				admin.DELETE("/invites/:id", h.AdminInviteRevoke)

				admin.GET("/users", h.SettingsUsersList)
				admin.POST("/users", h.SettingsUsersCreate)
				admin.PATCH("/users/:id/role", h.SettingsUsersUpdateRole)
				admin.POST("/users/:id/approve", h.SettingsUsersApprove)
				admin.POST("/users/:id/deny", h.SettingsUsersDeny)
				admin.DELETE("/users/:id", h.SettingsUsersDelete)

				// BookDrop housekeeping (admin-only — wipe has cross-user
				// blast radius). See ADR-0014.
				admin.DELETE("/bookdrop/processed", h.userScoped(h.BookDropClearProcessed))
				admin.GET("/bookdrop/files", h.userScoped(h.BookDropFilesPreview))
				admin.DELETE("/bookdrop/files", h.userScoped(h.BookDropWipeFiles))
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
		opds.GET("/book/:id/download", h.opdsBookScoped(h.OPDSDownload))
		opds.GET("/cover/:id", h.opdsBookScoped(h.OPDSCover))
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
		defer func() { _ = index.Close() }()
		c.Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Gin seeds NoRoute responses with 404; override to 200 so the SPA
		// shell is served with a success status on deep links like /login.
		c.Writer.WriteHeader(http.StatusOK)
		_, _ = io.Copy(c.Writer, index)
	})
}
