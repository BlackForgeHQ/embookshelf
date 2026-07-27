// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/llm"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/task"
)

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
func (h *Handler) BookGuideGet(c *gin.Context) {
	if requireUserID(c) == "" {
		return
	}
	g, err := h.guides.GetByBookID(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "no reading guide for this book yet")
			return
		}
		writeServerError(c, "load reading guide", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"guide": toReadingGuideDTO(g)})
}

// BookGuideGenerate queues generation for one book and returns 202.
//
// Always overwrites, including a hand-edited guide — unlike a bulk run,
// which skips those. Here the user is looking at the guide they are
// replacing, so the intent is visible (ADR-0024 §5).
func (h *Handler) BookGuideGenerate(c *gin.Context) {
	if requireUserID(c) == "" {
		return
	}
	ctx := c.Request.Context()

	cfg, err := h.appSettings.GetReadingGuide(ctx)
	if err != nil {
		writeServerError(c, "read guide settings", err)
		return
	}
	if !cfg.Enabled {
		writeErrorCode(c, http.StatusServiceUnavailable, CodeGuidesDisabled,
			"reading guides are not configured by the admin")
		return
	}
	if h.queue == nil {
		writeServerError(c, "guide generate", errors.New("no worker pool configured"))
		return
	}
	if err := h.queue.Enqueue(ctx, task.ReadingGuideArgs{BookID: c.Param("id")}); err != nil {
		writeServerError(c, "queue guide generation", err)
		return
	}
	c.Status(http.StatusAccepted)
}

type readingGuideEditReq struct {
	About    string `json:"about"`
	Audience string `json:"audience"`
	NotFor   string `json:"notFor"`
	Problems string `json:"problems"`
}

// BookGuideEdit saves a hand-written guide and marks the row edited, so
// bulk runs leave it alone from then on.
func (h *Handler) BookGuideEdit(c *gin.Context) {
	if requireUserID(c) == "" {
		return
	}
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

	id := c.Param("id")
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
// whether one is stored; an empty key on PUT means "leave it alone",
// matching how the SMTP password is handled.
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

func (h *Handler) SettingsReadingGuideGet(c *gin.Context) {
	cfg, err := h.appSettings.GetReadingGuide(c.Request.Context())
	if err != nil {
		writeServerError(c, "read guide settings", err)
		return
	}
	c.JSON(http.StatusOK, readingGuideSettingsDTO{
		Enabled: cfg.Enabled, BaseURL: cfg.BaseURL, Model: cfg.Model,
		KeySet: cfg.APIKey != "", AuthStyle: cfg.AuthStyle, Language: cfg.Language,
		TextCap: cfg.TextCap, RequestJSONMode: cfg.RequestJSONMode,
	})
}

func (h *Handler) SettingsReadingGuideUpdate(c *gin.Context) {
	var body readingGuideSettingsDTO
	if !bindJSON(c, &body) {
		return
	}
	ctx := c.Request.Context()
	current, err := h.appSettings.GetReadingGuide(ctx)
	if err != nil {
		writeServerError(c, "read guide settings", err)
		return
	}

	next := repo.ReadingGuideConfig{
		Enabled:         body.Enabled,
		BaseURL:         body.BaseURL,
		Model:           body.Model,
		APIKey:          current.APIKey,
		AuthStyle:       body.AuthStyle,
		Language:        body.Language,
		TextCap:         body.TextCap,
		RequestJSONMode: body.RequestJSONMode,
	}
	// An empty key means "keep what is stored" — the GET never returned
	// it, so the form cannot echo it back.
	if strings.TrimSpace(body.APIKey) != "" {
		next.APIKey = body.APIKey
	}

	if err := h.appSettings.SetReadingGuide(ctx, next); err != nil {
		// Validation refuses enabling without an endpoint; that is the
		// admin's mistake to see, not a 500.
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	h.SettingsReadingGuideGet(c)
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

	client, err := llm.New(llm.Config{
		BaseURL:         cfg.BaseURL,
		Model:           cfg.Model,
		APIKey:          cfg.APIKey,
		AuthStyle:       llm.AuthStyle(cfg.AuthStyle),
		RequestJSONMode: cfg.RequestJSONMode,
		// Short: a test that hangs for five minutes teaches nothing.
		Timeout: 30 * time.Second,
	})
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
