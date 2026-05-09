// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/blackforge/embookshelf/internal/crypto"
	"github.com/blackforge/embookshelf/internal/email"
	"github.com/blackforge/embookshelf/internal/layout"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// PasswordResetTTL is the lifetime of a reset token. Industry-standard
// 1h: long enough that legitimate email delays don't strand a user,
// short enough that an intercepted token closes fast. ADR-0020.
const PasswordResetTTL = time.Hour

// InviteTTL is the lifetime of an admin invite. 7d covers a weekend
// onboarding gap without keeping a stale invite forever. ADR-0020.
const InviteTTL = 7 * 24 * time.Hour

// SendToKindleMaxBytes mirrors Amazon's per-attachment cap. ADR-0021.
const SendToKindleMaxBytes int64 = 50 * 1024 * 1024

// kindleEligibleFormat is the set of book formats Send-to-Kindle
// accepts. The set is duplicated server-side and in the UI; both
// callers consult constants in their own layer rather than a runtime
// fetch. ADR-0021.
var kindleEligibleFormat = map[string]struct{}{
	"epub": {},
	"pdf":  {},
}

// IsKindleEligible reports whether a book's primary format can be
// shipped via Send-to-Kindle. Format is compared case-insensitively.
func IsKindleEligible(format string) bool {
	_, ok := kindleEligibleFormat[strings.ToLower(format)]
	return ok
}

// ErrEmailDisabled is returned by Notifier methods when the
// subsystem is off. Handlers translate this to 503 EMAIL_DISABLED.
var ErrEmailDisabled = errors.New("email subsystem disabled")

// ErrFormatNotSupported is returned by SendToKindle when the book's
// primary format is outside the Send-to-Kindle eligible set. ADR-0021.
var ErrFormatNotSupported = errors.New("kindle format not supported")

// ErrFileTooLarge is returned by SendToKindle when the primary file
// exceeds Amazon's 50 MB attachment cap. ADR-0021.
var ErrFileTooLarge = errors.New("kindle attachment too large")

// ErrKindleEmailUnset is returned when a SendToKindle target user
// hasn't configured their kindle_email yet.
var ErrKindleEmailUnset = errors.New("kindle email not set")

// NotifierDeps bundles the static seams Notifier needs. Sender,
// publicURL, and enabled flag live in runtime state so admins can
// flip the email subsystem on/off without restarting. Reload reads
// the EMAIL row through AppSettings + Cipher and rebuilds the
// runtime under the mutex.
type NotifierDeps struct {
	Templates   *email.Templates
	Resets      *repo.PasswordResetTokenRepo
	Invites     *repo.UserInviteRepo
	Users       *repo.UserRepo
	LibStore    LibraryStore
	AppSettings *repo.AppSettingsRepo
	Cipher      crypto.Cipher
	// Now allows tests to pin time. Production passes time.Now.
	Now func() time.Time
}

// Notifier is the orchestration above email.Sender. Owns token
// generation (crypto/rand → sha256 at rest), URL composition from
// PublicURL, and the Send-to-Kindle attachment build. The Sender
// itself does bytes; Notifier knows the domain. ADR-0020.
//
// The runtime state (sender, publicURL, enabled) is hot-reloadable.
// Admin edits to the EMAIL row trigger Reload; existing references
// in handlers and queue closures keep working without a restart.
type Notifier struct {
	deps NotifierDeps

	mu        sync.RWMutex
	sender    email.Sender
	publicURL string
	enabled   bool
}

// NewNotifier wires the static deps. The runtime sender starts
// disabled — call Reload before serving requests so the EMAIL row is
// applied.
func NewNotifier(deps NotifierDeps) *Notifier {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Notifier{deps: deps}
}

// Reload reads the current EMAIL row and rebuilds the SMTP sender.
// Disabled rows clear the sender so methods short-circuit with
// ErrEmailDisabled. A bad SMTP config also clears state and returns
// the construction error so the admin sees why hot-enable failed.
func (n *Notifier) Reload(ctx context.Context) error {
	cfg, err := n.deps.AppSettings.GetEmail(ctx, n.deps.Cipher)
	if err != nil {
		return fmt.Errorf("load email settings: %w", err)
	}
	if !cfg.Enabled {
		n.swap(nil, "", false)
		return nil
	}
	s, err := email.NewSMTPSender(email.SMTPConfig{
		Host:        cfg.SMTP.Host,
		Port:        cfg.SMTP.Port,
		Username:    cfg.SMTP.Username,
		Password:    cfg.SMTP.Password,
		TLS:         email.TLSMode(cfg.SMTP.TLS),
		FromAddress: cfg.From.Address,
		FromName:    cfg.From.Name,
	})
	if err != nil {
		n.swap(nil, "", false)
		return fmt.Errorf("smtp sender: %w", err)
	}
	n.swap(s, cfg.PublicURL, true)
	slog.Info("email subsystem ready", "from", cfg.From.Address, "host", cfg.SMTP.Host, "port", cfg.SMTP.Port)
	return nil
}

// Enabled reports whether the runtime sender is wired and the row is
// flagged on. Handlers consult this for the 503 EMAIL_DISABLED gate.
func (n *Notifier) Enabled() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.enabled
}

func (n *Notifier) swap(sender email.Sender, publicURL string, enabled bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sender = sender
	n.publicURL = publicURL
	n.enabled = enabled
}

// snapshot copies the runtime state under the read lock so methods
// don't hold the lock across SMTP I/O.
func (n *Notifier) snapshot() (sender email.Sender, publicURL string, enabled bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.sender, n.publicURL, n.enabled
}

// IssuePasswordReset stores a fresh reset token for user and sends
// the email. The plaintext token is generated here, hashed before
// storage, and embedded in the URL passed to the template — it never
// leaves the function.
func (n *Notifier) IssuePasswordReset(ctx context.Context, user model.User) error {
	sender, publicURL, enabled := n.snapshot()
	if !enabled {
		return ErrEmailDisabled
	}
	if user.Email == "" {
		return errors.New("user has no email")
	}

	plain, hash, err := newToken()
	if err != nil {
		return fmt.Errorf("token: %w", err)
	}
	expiresAt := n.deps.Now().Add(PasswordResetTTL)
	if err := n.deps.Resets.Create(ctx, hash, user.ID, expiresAt); err != nil {
		return fmt.Errorf("store reset token: %w", err)
	}

	resetURL := joinURL(publicURL, "/reset", "token", plain)
	data := struct {
		Name      string
		ResetURL  string
		ExpiresIn string
	}{
		Name:      user.Display(),
		ResetURL:  resetURL,
		ExpiresIn: humanDuration(PasswordResetTTL),
	}
	text, html, err := n.deps.Templates.Render("password_reset", data)
	if err != nil {
		return fmt.Errorf("render reset template: %w", err)
	}
	return sender.Send(ctx, email.Message{
		To:      user.Email,
		Subject: "Reset your embookshelf password",
		Text:    text,
		HTML:    html,
	})
}

// IssueAdminInvite stores a fresh invite and emails it. Returns the
// plaintext token only when callers need it for tests; production
// callers should ignore it.
func (n *Notifier) IssueAdminInvite(ctx context.Context, email_ string, role model.Role, invitedBy model.User) (plainToken string, err error) {
	sender, publicURL, enabled := n.snapshot()
	if !enabled {
		return "", ErrEmailDisabled
	}
	plain, hash, err := newToken()
	if err != nil {
		return "", fmt.Errorf("token: %w", err)
	}
	expiresAt := n.deps.Now().Add(InviteTTL)
	if err := n.deps.Invites.Create(ctx, hash, email_, role, invitedBy.ID, expiresAt); err != nil {
		return "", fmt.Errorf("store invite: %w", err)
	}

	acceptURL := joinURL(publicURL, "/accept-invite", "token", plain)
	data := struct {
		InvitedByName string
		Role          string
		AcceptURL     string
		ExpiresAt     string
	}{
		InvitedByName: invitedBy.Display(),
		Role:          string(role),
		AcceptURL:     acceptURL,
		ExpiresAt:     expiresAt.UTC().Format("2006-01-02 15:04 UTC"),
	}
	text, html, err := n.deps.Templates.Render("admin_invite", data)
	if err != nil {
		return "", fmt.Errorf("render invite template: %w", err)
	}
	if err := sender.Send(ctx, email.Message{
		To:      email_,
		Subject: "You're invited to embookshelf",
		Text:    text,
		HTML:    html,
	}); err != nil {
		return "", err
	}
	return plain, nil
}

// SendToKindle reads book bytes through libraryHandle, builds an
// email with the primary file as a single attachment, and ships it
// to user.KindleEmail. Synchronous — callers (the queue worker) are
// already off the request goroutine. ADR-0021.
func (n *Notifier) SendToKindle(ctx context.Context, book model.Book, user model.User) error {
	sender, _, enabled := n.snapshot()
	if !enabled {
		return ErrEmailDisabled
	}
	if user.KindleEmail == "" {
		return ErrKindleEmailUnset
	}
	if !IsKindleEligible(book.Format) {
		return ErrFormatNotSupported
	}

	handle, err := n.deps.LibStore.For(ctx, book.LibraryID)
	if err != nil {
		return fmt.Errorf("library handle: %w", err)
	}
	src, err := handle.BookSource(ctx, book)
	if err != nil {
		return fmt.Errorf("book source: %w", err)
	}

	reader, size, closer, err := openBookSource(ctx, handle, src)
	if err != nil {
		return fmt.Errorf("open book: %w", err)
	}
	defer func() {
		if closer != nil {
			_ = closer.Close()
		}
	}()
	if size > SendToKindleMaxBytes {
		return ErrFileTooLarge
	}

	filename := kindleAttachmentName(book)
	contentType := kindleContentType(book.Format)

	return sender.Send(ctx, email.Message{
		To:      user.KindleEmail,
		Subject: book.Title,
		// Amazon strips the body — keep one short line so spam
		// heuristics don't flag a body-less message.
		Text: "Attached: " + book.Title,
		Attachments: []email.Attachment{{
			Filename:    filename,
			ContentType: contentType,
			Reader:      reader,
		}},
	})
}

// HashToken returns sha256(plain) — the byte form stored in the
// token tables. Exposed so handlers can hash the token they read
// from the URL before lookup.
func HashToken(plain string) []byte {
	sum := sha256.Sum256([]byte(plain))
	return sum[:]
}

// newToken returns plaintext (URL-safe base64) and its sha256.
func newToken() (plain string, hash []byte, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, err
	}
	plain = base64.RawURLEncoding.EncodeToString(buf)
	return plain, HashToken(plain), nil
}

func joinURL(base, path string, queryKey, queryVal string) string {
	u, err := url.Parse(strings.TrimRight(base, "/") + path)
	if err != nil {
		return base + path + "?" + queryKey + "=" + url.QueryEscape(queryVal)
	}
	q := u.Query()
	q.Set(queryKey, queryVal)
	u.RawQuery = q.Encode()
	return u.String()
}

func humanDuration(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		days := int(d / (24 * time.Hour))
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	case d >= time.Hour:
		hours := int(d / time.Hour)
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	default:
		return d.String()
	}
}

func kindleAttachmentName(book model.Book) string {
	title := layout.SanitizeTitle(book.Title)
	author := layout.SanitizeAuthor(book.Author)
	ext := strings.ToLower(book.Format)
	if author == "" {
		return fmt.Sprintf("%s.%s", title, ext)
	}
	return fmt.Sprintf("%s - %s.%s", title, author, ext)
}

func kindleContentType(format string) string {
	switch strings.ToLower(format) {
	case "epub":
		return "application/epub+zip"
	case "pdf":
		return "application/pdf"
	default:
		return "application/octet-stream"
	}
}

// openBookSource resolves a BookSource to an io.Reader + size for
// attachment build. Local: os.Open the path. Stream: handle.Open the
// key. Presign is refused — Send-to-Kindle needs the bytes locally to
// build the attachment.
func openBookSource(ctx context.Context, handle *LibraryHandle, src BookSource) (io.Reader, int64, io.Closer, error) {
	switch src.Kind {
	case BookDeliveryStream:
		bytesSrc, err := handle.Open(ctx, src.Key)
		if err != nil {
			return nil, 0, nil, err
		}
		return io.NewSectionReader(bytesSrc, 0, bytesSrc.Size()), bytesSrc.Size(), bytesSrc, nil
	case BookDeliveryLocal:
		f, err := os.Open(src.Path)
		if err != nil {
			return nil, 0, nil, err
		}
		fi, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return nil, 0, nil, err
		}
		return f, fi.Size(), f, nil
	default:
		return nil, 0, nil, fmt.Errorf("unsupported book delivery for kindle: %v", src.Kind)
	}
}
