// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/service"
)

const kindleTestUser = "c2c2c2c2-0003-4003-8003-000000000001"

// enabledNotifier builds a Notifier whose runtime state says the email
// subsystem is on — the first gate SendToKindle passes. The row is
// written and reloaded rather than the flag being set directly because
// enabled state is Notifier's own, only reachable through Reload.
func enabledNotifier(t *testing.T) *service.Notifier {
	t.Helper()
	ctx := context.Background()
	settings := repo.NewAppSettingsRepo(repotest.New(t), nil)
	if err := settings.SetEmail(ctx, repo.EmailConfig{
		Enabled:   true,
		SMTP:      repo.EmailSMTPConfig{Host: "localhost", Port: 1025, TLS: "none"},
		From:      repo.EmailFromConfig{Address: "library@example.test"},
		PublicURL: "https://books.example.test",
	}); err != nil {
		t.Fatalf("write EMAIL row: %v", err)
	}
	n := service.NewNotifier(service.NotifierDeps{AppSettings: settings})
	if err := n.Reload(ctx); err != nil {
		t.Fatalf("notifier reload: %v", err)
	}
	if !n.Enabled() {
		t.Fatal("notifier still disabled after reloading an enabled row")
	}
	return n
}

// The worker pool is one of the seams Options documents as nil-able, so
// an install without one must degrade rather than dereference it. Driven
// as a body: the book-scoped seam has already resolved the book, which
// makes the enqueue reachable with no catalog behind it.
func TestSendToKindleWithoutAWorkerPoolIsA503NotAPanic(t *testing.T) {
	h := &Handler{notifier: enabledNotifier(t)} // Options.Queue absent.

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/books/b1/send-to-kindle", nil)
	c.Params = gin.Params{{Key: "id", Value: "b1"}}
	// KindleEmail is set on the context user so the endpoint's re-fetch
	// arm — the only thing here that wants a user store — stays unvisited.
	c.Request = c.Request.WithContext(auth.WithUser(c.Request.Context(),
		&model.User{ID: kindleTestUser, KindleEmail: "reader@kindle.com"}))

	h.SendToKindle(c, bookScope{
		UserID: kindleTestUser,
		Book:   model.Book{ID: "b1", Format: "EPUB"},
	})

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "queue unavailable") {
		t.Errorf("body = %s, want the degrade its sibling enqueue sites write", rec.Body.String())
	}
}
