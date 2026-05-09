// SPDX-License-Identifier: AGPL-3.0-or-later

package email

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/wneessen/go-mail"
)

// Sender is the transport seam. One real implementation
// (SMTPSender); a NoopSender is wired when the email subsystem is
// disabled so callers never branch on enabled-ness. ADR-0020.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// TLSMode mirrors the SMTP TLS policy admins pick in the settings UI.
// Port choice is independent — operators routinely run STARTTLS on
// port 587 and implicit TLS on 465.
type TLSMode string

const (
	// TLSNone is plaintext SMTP. Refused for connections where the
	// password is non-empty unless the host is "localhost" or
	// "127.0.0.1" — credentials over the wire in the clear is not a
	// shape this code helps you ship.
	TLSNone TLSMode = "none"
	// TLSStarttls upgrades after EHLO via STARTTLS. Fails closed if
	// the server doesn't advertise the capability.
	TLSStarttls TLSMode = "starttls"
	// TLSImplicit dials TLS from byte zero (port 465 convention).
	TLSImplicit TLSMode = "tls"
)

// SMTPConfig is the transport-side view of the EMAIL config. Service
// code translates the at-rest JSON into this struct after decrypting
// the password — Sender never touches the cipher.
type SMTPConfig struct {
	Host        string
	Port        int
	Username    string
	Password    string
	TLS         TLSMode
	FromAddress string
	FromName    string
	// DialTimeout caps the connect+TLS handshake. Zero means use the
	// 30s default — matches Brevo / SES / Postmark wait expectations.
	DialTimeout time.Duration
}

// ErrDisabled is returned by NoopSender so callers that ignore the
// Sender for "we are configured" (rare — feature gating is supposed
// to happen at the handler) get a clean signal instead of a silent
// drop.
var ErrDisabled = errors.New("email: subsystem disabled")

// SMTPSender wraps a *mail.Client. The client is rebuilt on every
// Send so config changes take effect without a restart and so a
// transient network failure on Tuesday doesn't poison Wednesday's
// reset email. go-mail's DialAndSend is itself a one-shot — reusing
// a client across long idle periods loses you nothing.
type SMTPSender struct {
	cfg SMTPConfig
	mu  sync.Mutex
}

// NewSMTPSender validates host/port and returns a ready Sender. It
// does NOT dial — admins press "Send test email" to learn whether the
// remote is reachable.
func NewSMTPSender(cfg SMTPConfig) (*SMTPSender, error) {
	if cfg.Host == "" {
		return nil, errors.New("smtp host empty")
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return nil, fmt.Errorf("smtp port out of range: %d", cfg.Port)
	}
	if cfg.FromAddress == "" {
		return nil, errors.New("smtp from address empty")
	}
	switch cfg.TLS {
	case TLSNone, TLSStarttls, TLSImplicit:
	case "":
		cfg.TLS = TLSStarttls
	default:
		return nil, fmt.Errorf("unknown TLS mode %q", cfg.TLS)
	}
	if cfg.TLS == TLSNone && cfg.Password != "" &&
		cfg.Host != "localhost" && cfg.Host != "127.0.0.1" {
		return nil, errors.New("refusing plaintext SMTP with non-empty password to a non-loopback host")
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 30 * time.Second
	}
	return &SMTPSender{cfg: cfg}, nil
}

// Send delivers msg synchronously. Callers using a queue dispatch
// this on the worker goroutine; HTTP handlers should not call Send
// directly except for the admin "test email" endpoint.
func (s *SMTPSender) Send(ctx context.Context, msg Message) error {
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()

	m := mail.NewMsg()
	if err := m.FromFormat(cfg.FromName, cfg.FromAddress); err != nil {
		return fmt.Errorf("from: %w", err)
	}
	if err := m.To(msg.To); err != nil {
		return fmt.Errorf("to: %w", err)
	}
	m.Subject(msg.Subject)
	m.SetMessageID()
	m.SetDate()

	if msg.HTML != "" {
		m.SetBodyString(mail.TypeTextPlain, msg.Text)
		m.AddAlternativeString(mail.TypeTextHTML, msg.HTML)
	} else {
		m.SetBodyString(mail.TypeTextPlain, msg.Text)
	}

	for _, a := range msg.Attachments {
		opts := []mail.FileOption{mail.WithFileName(a.Filename)}
		if a.ContentType != "" {
			opts = append(opts, mail.WithFileContentType(mail.ContentType(a.ContentType)))
		}
		if err := m.AttachReader(a.Filename, a.Reader, opts...); err != nil {
			return fmt.Errorf("attach %s: %w", a.Filename, err)
		}
	}

	opts := []mail.Option{
		mail.WithPort(cfg.Port),
		mail.WithTimeout(cfg.DialTimeout),
	}
	if cfg.Username != "" || cfg.Password != "" {
		opts = append(opts,
			mail.WithSMTPAuth(mail.SMTPAuthAutoDiscover),
			mail.WithUsername(cfg.Username),
			mail.WithPassword(cfg.Password),
		)
	}
	switch cfg.TLS {
	case TLSStarttls:
		opts = append(opts, mail.WithTLSPortPolicy(mail.TLSMandatory))
	case TLSImplicit:
		opts = append(opts, mail.WithSSL())
	case TLSNone:
		opts = append(opts, mail.WithTLSPortPolicy(mail.NoTLS))
	}

	client, err := mail.NewClient(cfg.Host, opts...)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	if err := client.DialAndSendWithContext(ctx, m); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return nil
}

// NoopSender is the disabled-subsystem Sender. Send always returns
// ErrDisabled. Wire it at boot when EMAIL.enabled=false so domain
// code can call Sender.Send unconditionally; handlers gate the
// feature before reaching it, but the sentinel makes a misuse
// loud.
type NoopSender struct{}

// Send returns ErrDisabled.
func (NoopSender) Send(_ context.Context, _ Message) error {
	slog.Warn("email: NoopSender.Send called — email subsystem disabled")
	return ErrDisabled
}
