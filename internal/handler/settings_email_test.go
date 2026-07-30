// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/repo"
)

// storedEmail is a complete, valid-looking EMAIL row with a secret in it.
func storedEmail() repo.EmailConfig {
	return repo.EmailConfig{
		Enabled: true,
		SMTP: repo.EmailSMTPConfig{
			Host: "smtp.example.com", Port: 587,
			Username: "postmaster", Password: "stored-password", TLS: "starttls",
		},
		From:      repo.EmailFromConfig{Address: "books@example.com", Name: "embookshelf"},
		PublicURL: "https://books.example.com",
	}
}

// TestSettingsEmailGetNeverReturnsThePassword is the property the whole
// write-only shape exists for: the GET tells the UI that a password is
// stored and nothing more. A regression here leaks the SMTP credential
// to every admin browser session, and is invisible in the UI because the
// field renders blank either way.
func TestSettingsEmailGetNeverReturnsThePassword(t *testing.T) {
	h := &Handler{appSettings: &fakeAppSettings{email: storedEmail()}}

	c, rec := settingsCtx(t, http.MethodGet, "/api/v1/settings/email", "")
	h.SettingsEmailGet(c)

	if httpStatus(c, rec) != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "stored-password") {
		t.Fatalf("the SMTP password travelled to the client: %s", rec.Body.String())
	}
	var got emailSettingsDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if !got.PasswordSet {
		t.Error("passwordSet = false despite a stored password; the UI would offer to set one that exists")
	}
	if got.SMTP.Host != "smtp.example.com" || got.From.Address != "books@example.com" {
		t.Errorf("non-secret fields did not survive the round trip: %+v", got)
	}
}

// TestSettingsEmailUpdateKeepsAPasswordNotRetyped drives the tri-state
// rule through the endpoint rather than the function. The keep arm is
// what makes the panel usable: an admin editing the From name must not
// have to re-enter a secret the form cannot show them.
func TestSettingsEmailUpdateKeepsAPasswordNotRetyped(t *testing.T) {
	cases := []struct {
		name        string
		password    string
		passwordSet bool
		want        string
	}{
		{"keep", "", true, "stored-password"},
		{"clear", "", false, ""},
		{"replace", "typed-password", true, "typed-password"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeAppSettings{email: storedEmail()}
			h := &Handler{appSettings: store}

			body := emailSettingsDTO{
				Enabled: true,
				SMTP: emailSMTPDTO{
					Host: "smtp.example.com", Port: 587, Username: "postmaster",
					Password: tc.password, TLS: "starttls",
				},
				From:        emailFromDTO{Address: "books@example.com", Name: "renamed"},
				PublicURL:   "https://books.example.com",
				PasswordSet: tc.passwordSet,
			}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			c, rec := settingsCtx(t, http.MethodPut, "/api/v1/settings/email", string(raw))
			h.SettingsEmailUpdate(c)

			if httpStatus(c, rec) != http.StatusNoContent {
				t.Fatalf("status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
			}
			if store.emailWrites != 1 {
				t.Fatalf("email row written %d times, want 1", store.emailWrites)
			}
			if store.email.SMTP.Password != tc.want {
				t.Errorf("stored password = %q, want %q", store.email.SMTP.Password, tc.want)
			}
			if store.email.From.Name != "renamed" {
				t.Errorf("the edit the admin actually made was lost: %+v", store.email.From)
			}
		})
	}
}

// TestSettingsEmailUpdateSplitsRefusalsFromFailures pins the one rule
// the handler still owns now that validation moved onto the row: a
// wrapped ErrEmailInvalid is the admin's mistake and carries its own
// message, anything else is ours and must not be echoed as a 400.
//
// Both arms matter. Answering 500 for a bad SMTP port hides a fixable
// typo behind "internal error"; answering 400 for a cipher failure tells
// the admin to correct a form that was already correct.
func TestSettingsEmailUpdateSplitsRefusalsFromFailures(t *testing.T) {
	valid := storedEmail()
	raw, err := json.Marshal(toEmailSettingsDTO(valid))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	t.Run("row refusal is a 400 carrying the row's message", func(t *testing.T) {
		store := &fakeAppSettings{
			email:       valid,
			setEmailErr: fmt.Errorf("%w: smtp port must be 1..65535", repo.ErrEmailInvalid),
		}
		h := &Handler{appSettings: store}

		c, rec := settingsCtx(t, http.MethodPut, "/api/v1/settings/email", string(raw))
		h.SettingsEmailUpdate(c)

		if httpStatus(c, rec) != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "smtp port must be 1..65535") {
			t.Errorf("the row's message did not reach the admin: %s", rec.Body.String())
		}
	})

	t.Run("any other failure is a 500", func(t *testing.T) {
		store := &fakeAppSettings{email: valid, setEmailErr: errBoom}
		h := &Handler{appSettings: store}

		c, rec := settingsCtx(t, http.MethodPut, "/api/v1/settings/email", string(raw))
		h.SettingsEmailUpdate(c)

		if httpStatus(c, rec) != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 (body %s)", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "boom") {
			t.Errorf("an internal failure was echoed to the client: %s", rec.Body.String())
		}
	})
}

// TestSettingsEmailTestValidatesTheRecipient covers the guards on the
// test-send before it can dial anything: a blank or unparseable
// recipient, and an SMTP row too incomplete to send with.
func TestSettingsEmailTestValidatesTheRecipient(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		email repo.EmailConfig
	}{
		{"blank recipient", `{"to":"   "}`, storedEmail()},
		{"unparseable recipient", `{"to":"not-an-address"}`, storedEmail()},
		{"unconfigured row", `{"to":"someone@example.com"}`, repo.EmailConfig{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{appSettings: &fakeAppSettings{email: tc.email}}
			c, rec := settingsCtx(t, http.MethodPost, "/api/v1/settings/email/test", tc.body)
			h.SettingsEmailTest(c)
			if httpStatus(c, rec) != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
		})
	}
}
