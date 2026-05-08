package handler

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
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

// SettingsEmailGet returns the current EMAIL config with the
// password elided. Admin-only — ADR-0010 keeps the password
// AES-GCM-encrypted at rest, but reading "yes a password is set" is
// useful for the UI even on instances without a KEK.
func (h *Handler) SettingsEmailGet(c *gin.Context) {
	cfg, err := h.appSettings.GetEmail(c.Request.Context(), h.cipher)
	if err != nil {
		writeServerError(c, "email settings get", err)
		return
	}
	c.JSON(http.StatusOK, toEmailSettingsDTO(cfg))
}

// SettingsEmailUpdate validates and persists the EMAIL row. An empty
// password in the request means "keep the existing one" so admins
// can edit other fields without retyping the secret.
func (h *Handler) SettingsEmailUpdate(c *gin.Context) {
	var body emailSettingsDTO
	if !bindJSON(c, &body) {
		return
	}

	body.SMTP.Host = strings.TrimSpace(body.SMTP.Host)
	body.SMTP.Username = strings.TrimSpace(body.SMTP.Username)
	body.From.Address = strings.TrimSpace(body.From.Address)
	body.From.Name = strings.TrimSpace(body.From.Name)
	body.PublicURL = strings.TrimRight(strings.TrimSpace(body.PublicURL), "/")

	if body.Enabled {
		if err := validateEmailSettings(body); err != nil {
			writeError(c, http.StatusBadRequest, err.Error())
			return
		}
	}

	current, err := h.appSettings.GetEmail(c.Request.Context(), h.cipher)
	if err != nil {
		writeServerError(c, "email settings load", err)
		return
	}
	cfg := repo.EmailConfig{
		Enabled: body.Enabled,
		SMTP: repo.EmailSMTPConfig{
			Host:     body.SMTP.Host,
			Port:     body.SMTP.Port,
			Username: body.SMTP.Username,
			TLS:      body.SMTP.TLS,
			Password: body.SMTP.Password,
		},
		From: repo.EmailFromConfig{
			Address: body.From.Address,
			Name:    body.From.Name,
		},
		PublicURL: body.PublicURL,
	}
	if cfg.SMTP.Password == "" {
		cfg.SMTP.Password = current.SMTP.Password
	}
	if err := h.appSettings.SetEmail(c.Request.Context(), h.cipher, cfg); err != nil {
		writeServerError(c, "email settings save", err)
		return
	}
	c.Status(http.StatusNoContent)
}

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

	cfg, err := h.appSettings.GetEmail(c.Request.Context(), h.cipher)
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
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"code": "SMTP_ERROR", "message": err.Error()}})
		return
	}
	c.Status(http.StatusNoContent)
}

func validateEmailSettings(body emailSettingsDTO) error {
	if body.SMTP.Host == "" {
		return errors.New("smtp host is required")
	}
	if body.SMTP.Port <= 0 || body.SMTP.Port > 65535 {
		return errors.New("smtp port must be 1..65535")
	}
	switch body.SMTP.TLS {
	case "", "starttls", "tls", "none":
	default:
		return errors.New("smtp tls must be one of: none, starttls, tls")
	}
	if body.From.Address == "" {
		return errors.New("from address is required")
	}
	if _, err := mail.ParseAddress(body.From.Address); err != nil {
		return errors.New("from address is not a valid mailbox")
	}
	if body.PublicURL == "" {
		return errors.New("public url is required when email is enabled")
	}
	u, err := url.Parse(body.PublicURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("public url must be an absolute http(s) URL")
	}
	return nil
}
