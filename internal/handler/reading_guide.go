// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/llm"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// readingGuideStore is the slice of BookReadingGuideRepo the book-scoped
// routes need — an interface so the handler tier is exercisable without
// Postgres, same reasoning as markdownRenditionStore.
type readingGuideStore interface {
	GetByBookID(ctx context.Context, bookID string) (model.ReadingGuide, error)
	SaveEdit(ctx context.Context, bookID string, t model.ReadingGuideText) error
}

// newReadingGuideStore keeps a missing repo nil across the interface
// conversion — same trap newMarkdownRenditionStore exists for: a nil
// pointer boxed into an interface is non-nil, and every degrade check
// downstream would panic instead.
func newReadingGuideStore(r *repo.BookReadingGuideRepo) readingGuideStore {
	if r == nil {
		return nil
	}
	return r
}

// guidesUnavailableMsg is the reading guide's one "not wired" sentence,
// answered by BookGuideGet, BookGuideEdit and — folded into
// CodeGuidesDisabled — BookGuideGenerate, whenever the store behind them
// is nil.
const guidesUnavailableMsg = "reading guides are unavailable"

// readingGuideDTO is the wire shape. SourceKind travels because the
// reader needs it: a metadata-only guide for an obscure title leans on
// what the model already believed about it (ADR-0024 §2).
type readingGuideDTO struct {
	About        string `json:"about"`
	Audience     string `json:"audience"`
	NotFor       string `json:"notFor"`
	Problems     string `json:"problems"`
	SourceKind   string `json:"sourceKind"`
	Model        string `json:"model"`
	Language     string `json:"language"`
	GeneratedAt  string `json:"generatedAt"`
	EditedByUser bool   `json:"editedByUser"`
}

func toReadingGuideDTO(g model.ReadingGuide) readingGuideDTO {
	return readingGuideDTO{
		About: g.About, Audience: g.Audience, NotFor: g.NotFor, Problems: g.Problems,
		SourceKind:   string(g.SourceKind),
		Model:        g.Model,
		Language:     g.Language,
		GeneratedAt:  g.GeneratedAt.UTC().Format(time.RFC3339),
		EditedByUser: g.EditedByUser,
	}
}

// BookGuideGet returns a book's reading guide. 404 when none has been
// generated — distinct from an empty one, which cannot be stored.
func (h *Handler) BookGuideGet(c *gin.Context, s bookScope) {
	h.renditionStatus(c, renditionStatusSpec{
		available:      h.guides != nil,
		unavailableMsg: guidesUnavailableMsg,
		readOp:         "load reading guide",
		load: func(ctx context.Context) (any, error) {
			g, err := h.guides.GetByBookID(ctx, s.Book.ID)
			if err != nil {
				return nil, err
			}
			return gin.H{"guide": toReadingGuideDTO(g)}, nil
		},
		noneMsg: "no reading guide for this book yet",
	})
}

// guidesDisabledMsg is CodeGuidesDisabled's sentence — answered whether
// the store isn't wired at all or the admin has simply left the row off;
// either way there is no configured way to generate a guide.
const guidesDisabledMsg = "reading guides are not configured by the admin"

// BookGuideGenerate queues generation for one book and returns 202 — the
// shared rendition generate chain with the guide's own configuration
// (#320). Availability folds the store's presence together with the
// admin row, both answering CodeGuidesDisabled on the spec: a store that
// was never wired offers no more than one an admin switched off, and in
// practice the two always travel together — New wires h.guides and the
// READING_GUIDE row from the same install. The format preflight replaces
// the chain's built-in Convertible gate rather than running beside it —
// the guide is offered for every format, unlike markdown and the EPUB.
//
// Always overwrites, including a hand-edited guide — unlike a bulk run,
// which skips those. Here the user is looking at the guide they are
// replacing, so the intent is visible (ADR-0024 §5).
func (h *Handler) BookGuideGenerate(c *gin.Context, s bookScope) {
	ctx := c.Request.Context()

	cfg, err := h.appSettings.GetReadingGuide(ctx)
	if err != nil {
		writeServerError(c, "read guide settings", err)
		return
	}
	h.renditionGenerate(c, s, renditionRouteSpec{
		available:       h.guides != nil && cfg.Enabled,
		unavailableMsg:  guidesDisabledMsg,
		unavailableCode: CodeGuidesDisabled,
		formatGate:      h.guidePreflightConvertible,
		requestOp:       "queue guide generation",
		request: func(ctx context.Context, bookID string) error {
			// Through the runner's shared request module, so the button
			// and the bulk run make a request the same way (#336). The
			// fallback is the handler.Options rule: a wiring without a
			// runner still has a queue.
			if h.guideRunner != nil {
				return h.guideRunner.RequestOne(ctx, bookID)
			}
			return h.queue.Enqueue(ctx, jobs.ReadingGuideArgs{BookID: bookID})
		},
	})
}

// guidePreflightConvertible refuses at the button what the guide job
// would only discover in thirty seconds (ADR-0033 §5): a Convertible
// book whose text depends on an unconfigured converter, or on a
// conversion that already failed — whose message travels verbatim,
// because it is the thing the admin has to act on. Everything else —
// missing, stale, converting — is the job's to handle; the guide's
// retry is the wait.
func (h *Handler) guidePreflightConvertible(c *gin.Context, s bookScope) bool {
	if !model.Convertible(s.Book.Format) || h.renditions == nil {
		return true
	}
	if _, ok := h.requireConverter(c); !ok {
		return false
	}
	rendition, err := h.renditions.GetByBookID(c.Request.Context(), s.Book.ID)
	if errors.Is(err, repo.ErrNotFound) {
		// No row is fine: the guide job requests the conversion itself.
		return true
	}
	if err != nil {
		writeServerError(c, "read markdown rendition", err)
		return false
	}
	if rendition.State == model.RenditionFailed {
		writeError(c, http.StatusBadGateway, rendition.Error)
		return false
	}
	return true
}

type readingGuideEditReq struct {
	About    string `json:"about"`
	Audience string `json:"audience"`
	NotFor   string `json:"notFor"`
	Problems string `json:"problems"`
}

// BookGuideEdit saves a hand-written guide and marks the row edited, so
// bulk runs leave it alone from then on.
func (h *Handler) BookGuideEdit(c *gin.Context, s bookScope) {
	var body readingGuideEditReq
	if !bindJSON(c, &body) {
		return
	}
	text := model.ReadingGuideText{
		About:    strings.TrimSpace(body.About),
		Audience: strings.TrimSpace(body.Audience),
		NotFor:   strings.TrimSpace(body.NotFor),
		Problems: strings.TrimSpace(body.Problems),
	}
	if text.About == "" && text.Audience == "" && text.NotFor == "" && text.Problems == "" {
		writeError(c, http.StatusBadRequest, "a reading guide cannot be entirely empty")
		return
	}
	if h.guides == nil {
		writeError(c, http.StatusServiceUnavailable, guidesUnavailableMsg)
		return
	}

	id := s.Book.ID
	if err := h.guides.SaveEdit(c.Request.Context(), id, text); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "no reading guide for this book yet")
			return
		}
		writeServerError(c, "save reading guide", err)
		return
	}
	g, err := h.guides.GetByBookID(c.Request.Context(), id)
	if err != nil {
		writeServerError(c, "reload reading guide", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"guide": toReadingGuideDTO(g)})
}

// --- admin surface --------------------------------------------------------

// readingGuideSettingsDTO never carries the API key. keySet tells the UI
// whether one is stored, and comes back on PUT so an empty key can mean
// either "keep" or "clear" — the same three-state rule as the SMTP
// password (resolveSecret).
type readingGuideSettingsDTO struct {
	Enabled         bool   `json:"enabled"`
	BaseURL         string `json:"baseUrl"`
	Model           string `json:"model"`
	APIKey          string `json:"apiKey,omitempty"`
	KeySet          bool   `json:"keySet"`
	AuthStyle       string `json:"authStyle"`
	Language        string `json:"language"`
	TextCap         int64  `json:"textCap"`
	RequestJSONMode bool   `json:"requestJsonMode"`
}

// readingGuideSettings declares the READING_GUIDE surface. Every save
// refusal maps to a 400: the row's failures are validation, and the
// cipher/database cases were already reported this way before the
// adapter — kept verbatim rather than silently reclassified.
var readingGuideSettings = settingsDomain[repo.ReadingGuideConfig, readingGuideSettingsDTO]{
	name: "read guide settings",
	get: func(ctx context.Context, h *Handler) (repo.ReadingGuideConfig, error) {
		return h.appSettings.GetReadingGuide(ctx)
	},
	save: func(ctx context.Context, h *Handler, cfg repo.ReadingGuideConfig) error {
		return h.appSettings.SetReadingGuide(ctx, cfg)
	},
	toDTO: func(_ *Handler, _ *gin.Context, cfg repo.ReadingGuideConfig) readingGuideSettingsDTO {
		return readingGuideSettingsDTO{
			Enabled: cfg.Enabled, BaseURL: cfg.BaseURL, Model: cfg.Model,
			KeySet: cfg.APIKey != "", AuthStyle: cfg.AuthStyle, Language: cfg.Language,
			TextCap: cfg.TextCap, RequestJSONMode: cfg.RequestJSONMode,
		}
	},
	merge: func(dto readingGuideSettingsDTO, _ repo.ReadingGuideConfig) repo.ReadingGuideConfig {
		return repo.ReadingGuideConfig{
			Enabled:         dto.Enabled,
			BaseURL:         dto.BaseURL,
			Model:           dto.Model,
			AuthStyle:       dto.AuthStyle,
			Language:        dto.Language,
			TextCap:         dto.TextCap,
			RequestJSONMode: dto.RequestJSONMode,
		}
	},
	secrets: func(dto *readingGuideSettingsDTO, next, current *repo.ReadingGuideConfig) []settingsSecret {
		return []settingsSecret{{
			incoming: strings.TrimSpace(dto.APIKey),
			set:      dto.KeySet,
			stored:   current.APIKey,
			slot:     &next.APIKey,
		}}
	},
	badRequest: anySaveRefusalIsA400,
}

func (h *Handler) SettingsReadingGuideGet(c *gin.Context) {
	settingsGet(c, h, readingGuideSettings)
}

func (h *Handler) SettingsReadingGuideUpdate(c *gin.Context) {
	settingsPut(c, h, readingGuideSettings)
}

// SettingsReadingGuideEstimate sizes a bulk run before anything is spent.
func (h *Handler) SettingsReadingGuideEstimate(c *gin.Context) {
	if h.guideRunner == nil {
		writeServerError(c, "guide estimate", errors.New("no guide runner configured"))
		return
	}
	est, err := h.guideRunner.Estimate(c.Request.Context())
	if err != nil {
		writeServerError(c, "guide estimate", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"books":          est.Books,
		"fullTextBooks":  est.FullTextBooks,
		"maxInputTokens": est.MaxInputTokens,
		"totalBooks":     est.TotalBooks,
		"booksWithGuide": est.BooksWithGuide,
	})
}

// SettingsReadingGuideRun starts a bulk run.
func (h *Handler) SettingsReadingGuideRun(c *gin.Context) {
	ctx := c.Request.Context()
	cfg, err := h.appSettings.GetReadingGuide(ctx)
	if err != nil {
		writeServerError(c, "read guide settings", err)
		return
	}
	if !cfg.Enabled {
		writeErrorCode(c, http.StatusServiceUnavailable, CodeGuidesDisabled,
			"reading guides are not configured")
		return
	}
	if h.guideRunner == nil {
		writeServerError(c, "guide run", errors.New("no guide runner configured"))
		return
	}

	queued, err := h.guideRunner.Start(ctx)
	if err != nil {
		// Partial progress is real: those jobs are running. Report the
		// count alongside the failure rather than implying nothing began.
		slog.Error("guide run", "queued", queued, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"queued": queued,
			"error":  "run started but could not queue every book",
		})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"queued": queued})
}

// SettingsReadingGuideTest sends one trivial prompt to the configured
// endpoint and reports what came back.
//
// Worth its own endpoint rather than "generate one and see": the failures
// that matter here — wrong base URL, wrong auth header, unknown model,
// no quota — are all indistinguishable from "the guide looks bad" if the
// only way to exercise them is a real generation. Mirrors the email
// subsystem's test-send.
func (h *Handler) SettingsReadingGuideTest(c *gin.Context) {
	ctx := c.Request.Context()
	cfg, err := h.appSettings.GetReadingGuide(ctx)
	if err != nil {
		writeServerError(c, "read guide settings", err)
		return
	}
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.Model) == "" {
		writeError(c, http.StatusBadRequest, "set a base URL and model first")
		return
	}

	client, err := cfg.ProbeClient()
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	reply, err := client.Chat(ctx, []llm.Message{
		{Role: llm.RoleUser, Content: "Reply with the single word: ready"},
	})
	if err != nil {
		// The endpoint's own message is the useful part — Azure names a
		// bad key or a wrong region explicitly — so pass it through
		// rather than flattening it to "test failed".
		slog.Warn("reading guide test", "err", err)
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "reply": strings.TrimSpace(reply)})
}
