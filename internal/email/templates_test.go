package email

import (
	"strings"
	"testing"
)

func TestTemplates_Render_PasswordReset(t *testing.T) {
	tpl, err := NewTemplates()
	if err != nil {
		t.Fatalf("new templates: %v", err)
	}
	text, html, err := tpl.Render("password_reset", struct {
		Name      string
		ResetURL  string
		ExpiresIn string
	}{
		Name:      "Alice",
		ResetURL:  "https://books.example.com/reset?token=abc",
		ExpiresIn: "1 hour",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(text, "Alice") {
		t.Errorf("text body missing name; got %q", text)
	}
	if !strings.Contains(text, "https://books.example.com/reset?token=abc") {
		t.Errorf("text body missing reset URL; got %q", text)
	}
	if !strings.Contains(html, "https://books.example.com/reset?token=abc") {
		t.Errorf("html body missing reset URL; got %q", html)
	}
}

func TestTemplates_Render_AdminInvite(t *testing.T) {
	tpl, err := NewTemplates()
	if err != nil {
		t.Fatalf("new templates: %v", err)
	}
	text, html, err := tpl.Render("admin_invite", struct {
		InvitedByName string
		Role          string
		AcceptURL     string
		ExpiresAt     string
	}{
		InvitedByName: "Admin",
		Role:          "user",
		AcceptURL:     "https://books.example.com/accept-invite?token=xyz",
		ExpiresAt:     "2026-05-15 12:00 UTC",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, frag := range []string{"Admin", "https://books.example.com/accept-invite?token=xyz", "2026-05-15"} {
		if !strings.Contains(text, frag) {
			t.Errorf("text body missing %q; got %q", frag, text)
		}
		if !strings.Contains(html, frag) {
			t.Errorf("html body missing %q; got %q", frag, html)
		}
	}
}
