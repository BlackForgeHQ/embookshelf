// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/email"
	"github.com/blackforge/embookshelf/internal/repo"
)

// emailSettingsDTO is the wire shape. Password is never sent — the
// `passwordSet` flag tells the UI whether a password is currently
// stored. PUT carries the password only when the admin types a new
// one (empty means "leave alone").
type emailSettingsDTO struct {
	Enabled     bool         `json:"enabled"`
	SMTP        emailSMTPDTO `json:"smtp"`
	From        emailFromDTO `json:"from"`
	PublicURL   string       `json:"publicUrl"`
	PasswordSet bool         `json:"passwordSet"`
}

type emailSMTPDTO struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	TLS      string `json:"tls"`
}

type emailFromDTO struct {
	Address string `json:"address"`
	Name    string `json:"name"`
}

func toEmailSettingsDTO(cfg repo.EmailConfig) emailSettingsDTO {
	return emailSettingsDTO{
		Enabled: cfg.Enabled,
		SMTP: emailSMTPDTO{
			Host:     cfg.SMTP.Host,
			Port:     cfg.SMTP.Port,
			Username: cfg.SMTP.Username,
			TLS:      cfg.SMTP.TLS,
		},
		From: emailFromDTO{
			Address: cfg.From.Address,
			Name:    cfg.From.Name,
		},
		PublicURL:   cfg.PublicURL,
		PasswordSet: cfg.SMTP.Password != "",
	}
}

// emailSettings declares the EMAIL surface. Trimming and validation
// belong to the row, so a CLI or an importer is held to the same rules
// as these endpoints; the password is elided from every GET (ADR-0010)
// and rides the tri-state secret slot on PUT. The 204 is historical —
// the panel re-fetches rather than reading the PUT body.
var emailSettings = settingsDomain[repo.EmailConfig, emailSettingsDTO]{
	name: "email settings",
	get: func(ctx context.Context, h *Handler) (repo.EmailConfig, error) {
		return h.appSettings.GetEmail(ctx)
	},
	save: func(ctx context.Context, h *Handler, cfg repo.EmailConfig) error {
		return h.appSettings.SetEmail(ctx, cfg)
	},
	toDTO: func(_ *Handler, _ *gin.Context, cfg repo.EmailConfig) emailSettingsDTO {
		return toEmailSettingsDTO(cfg)
	},
	merge: func(dto emailSettingsDTO, _ repo.EmailConfig) repo.EmailConfig {
		return repo.EmailConfig{
			Enabled: dto.Enabled,
			SMTP: repo.EmailSMTPConfig{
				Host:     dto.SMTP.Host,
				Port:     dto.SMTP.Port,
				Username: dto.SMTP.Username,
				TLS:      dto.SMTP.TLS,
			},
			From: repo.EmailFromConfig{
				Address: dto.From.Address,
				Name:    dto.From.Name,
			},
			PublicURL: dto.PublicURL,
		}
	},
	secrets: func(dto *emailSettingsDTO, next, current *repo.EmailConfig) []settingsSecret {
		return []settingsSecret{{
			incoming: dto.SMTP.Password,
			set:      dto.PasswordSet,
			stored:   current.SMTP.Password,
			slot:     &next.SMTP.Password,
		}}
	},
	// A row refusal is the admin's mistake to see, and it carries its
	// own message. Anything else — a cipher or a database failure — is
	// ours.
	badRequest: func(err error) bool { return errors.Is(err, repo.ErrEmailInvalid) },
	// Hot-reload so the new SMTP config takes effect without restart.
	// Reload errors don't fail the save — the row is already persisted
	// and Notifier holds a disabled state until the admin fixes the
	// config — but they're returned so the UI can surface the SMTP
	// construction error inline.
	afterSave: func(h *Handler, c *gin.Context, _ repo.EmailConfig) bool {
		if h.notifier == nil {
			return true
		}
		if err := h.notifier.Reload(c.Request.Context()); err != nil {
			slog.Warn("email settings reload", "err", err)
			writeErrorCode(c, http.StatusBadGateway, CodeEmailReloadFailed, err.Error())
			return false
		}
		return true
	},
	noBody: true,
}

func (h *Handler) SettingsEmailGet(c *gin.Context)    { settingsGet(c, h, emailSettings) }
func (h *Handler) SettingsEmailUpdate(c *gin.Context) { settingsPut(c, h, emailSettings) }

type emailTestReq struct {
	To string `json:"to"`
}

// SettingsEmailTest dispatches a one-off SMTP send using the row
// currently in app_settings. The password from the request body
// (if any) is treated as a "test before save" override; otherwise
// the stored password is used. Returns the SMTP-side error verbatim
// so the admin can copy it into a support thread.
func (h *Handler) SettingsEmailTest(c *gin.Context) {
	var body emailTestReq
	if !bindJSON(c, &body) {
		return
	}
	to := strings.TrimSpace(body.To)
	if to == "" {
		writeError(c, http.StatusBadRequest, "to is required")
		return
	}
	if _, err := mail.ParseAddress(to); err != nil {
		writeError(c, http.StatusBadRequest, "invalid recipient address")
		return
	}

	cfg, err := h.appSettings.GetEmail(c.Request.Context())
	if err != nil {
		writeServerError(c, "email settings load for test", err)
		return
	}
	if cfg.SMTP.Host == "" || cfg.From.Address == "" {
		writeError(c, http.StatusBadRequest, "configure SMTP host and From before testing")
		return
	}
	sender, err := email.NewSMTPSender(email.SMTPConfig{
		Host:        cfg.SMTP.Host,
		Port:        cfg.SMTP.Port,
		Username:    cfg.SMTP.Username,
		Password:    cfg.SMTP.Password,
		TLS:         email.TLSMode(cfg.SMTP.TLS),
		FromAddress: cfg.From.Address,
		FromName:    cfg.From.Name,
		DialTimeout: 30 * time.Second,
	})
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()
	msg := email.Message{
		To:      to,
		Subject: "embookshelf email test",
		Text:    "If you received this message your SMTP settings work. — embookshelf",
		HTML:    "<p>If you received this message your SMTP settings work.</p><p>— embookshelf</p>",
	}
	if err := sender.Send(ctx, msg); err != nil {
		writeErrorCode(c, http.StatusBadGateway, CodeSMTPError, err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}
