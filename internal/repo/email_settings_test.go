// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"errors"
	"testing"
)

// checkEmail runs the row's Normalize and Validate in the order Set
// runs them, with no database and no cipher. Every rule below is one an
// HTTP handler used to own, so the test that proves it lives here now
// must not need HTTP to reach it.
func checkEmail(t *testing.T, cfg EmailConfig) error {
	t.Helper()
	if emailSetting.Validate == nil {
		t.Fatal("the EMAIL row declares no Validate — its rules still live in the handler")
	}
	return emailSetting.Validate(emailSetting.Normalize(cfg))
}

// enabledEmail is a config that passes every rule, so each test can
// break exactly one field and know which rule refused it.
func enabledEmail() EmailConfig {
	return EmailConfig{
		Enabled: true,
		SMTP: EmailSMTPConfig{
			Host: "smtp.example.com",
			Port: 587,
			TLS:  "starttls",
		},
		From:      EmailFromConfig{Address: "library@example.com", Name: "embookshelf"},
		PublicURL: "https://books.example.com",
	}
}

// An enabled row with no host configures a mailer that cannot dial
// anything; the handler refused it, and so must every other caller.
func TestEmailValidateRequiresAnSMTPHost(t *testing.T) {
	t.Parallel()

	cfg := enabledEmail()
	cfg.SMTP.Host = "   "
	if err := checkEmail(t, cfg); err == nil {
		t.Fatal("an enabled row with a blank SMTP host was accepted")
	}
}

// Port 0 is what an omitted JSON field decodes to, so the out-of-range
// check is the one that catches a caller who simply forgot the field.
func TestEmailValidateRefusesAPortOutOfRange(t *testing.T) {
	t.Parallel()

	for _, port := range []int{0, -1, 65536} {
		cfg := enabledEmail()
		cfg.SMTP.Port = port
		if err := checkEmail(t, cfg); err == nil {
			t.Errorf("port %d was accepted", port)
		}
	}
}

// email.TLSMode understands three values. A fourth reaches the sender as
// an unknown mode, which is a runtime surprise rather than a save-time
// one unless the row refuses it here.
func TestEmailValidateRefusesAnUnknownTLSMode(t *testing.T) {
	t.Parallel()

	cfg := enabledEmail()
	cfg.SMTP.TLS = "ssl"
	if err := checkEmail(t, cfg); err == nil {
		t.Fatal("TLS mode \"ssl\" was accepted")
	}
	for _, mode := range []string{"none", "starttls", "tls", ""} {
		ok := enabledEmail()
		ok.SMTP.TLS = mode
		if err := checkEmail(t, ok); err != nil {
			// The empty string is legal because Normalize fills it in
			// with starttls before Validate ever sees the row.
			t.Errorf("TLS mode %q was refused: %v", mode, err)
		}
	}
}

// A From that is blank or not a mailbox is rejected by the relay, not by
// us — at send time, long after the admin left the settings page.
func TestEmailValidateRequiresAParseableFromAddress(t *testing.T) {
	t.Parallel()

	for _, addr := range []string{"", "   ", "not an address", "library@", "@example.com"} {
		cfg := enabledEmail()
		cfg.From.Address = addr
		if err := checkEmail(t, cfg); err == nil {
			t.Errorf("From address %q was accepted", addr)
		}
	}
}

// Every link an email carries — password reset, invite — is built on
// PublicURL. A relative or schemeless one produces mail whose links go
// nowhere, and the recipient is the first to find out.
func TestEmailValidateRequiresAnAbsolutePublicURL(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"", "   ", "books.example.com", "/library", "ftp://books.example.com", "https://"} {
		cfg := enabledEmail()
		cfg.PublicURL = raw
		if err := checkEmail(t, cfg); err == nil {
			t.Errorf("public URL %q was accepted", raw)
		}
	}
}

// An admin fills the form in stages, and the boot seed writes a blank
// row on first boot — refusing either would leave no row to edit.
// Nothing sends mail while Enabled is false, so nothing is at risk.
func TestEmailValidateOnlyBindsWhenEnabled(t *testing.T) {
	t.Parallel()

	if err := checkEmail(t, EmailConfig{}); err != nil {
		t.Errorf("a blank disabled row was refused: %v", err)
	}
	if err := checkEmail(t, DefaultEmailConfig()); err != nil {
		t.Errorf("the seed row was refused: %v", err)
	}
}

// The handler answers 400 for a refusal and 500 for anything else, and
// it tells them apart with errors.Is. A rule that forgets to wrap turns
// an admin's typo into a server error.
func TestEmailValidateWrapsErrEmailInvalid(t *testing.T) {
	t.Parallel()

	broken := []EmailConfig{}
	for _, mutate := range []func(*EmailConfig){
		func(c *EmailConfig) { c.SMTP.Host = "" },
		func(c *EmailConfig) { c.SMTP.Port = 0 },
		func(c *EmailConfig) { c.SMTP.TLS = "ssl" },
		func(c *EmailConfig) { c.From.Address = "" },
		func(c *EmailConfig) { c.From.Address = "nonsense" },
		func(c *EmailConfig) { c.PublicURL = "" },
		func(c *EmailConfig) { c.PublicURL = "books.example.com" },
	} {
		cfg := enabledEmail()
		mutate(&cfg)
		broken = append(broken, cfg)
	}

	for _, cfg := range broken {
		err := checkEmail(t, cfg)
		if err == nil {
			t.Fatalf("%+v was accepted", cfg)
		}
		if !errors.Is(err, ErrEmailInvalid) {
			t.Errorf("%q does not wrap ErrEmailInvalid, so the handler would answer 500", err)
		}
	}
}

// The handler used to trim these five fields before validating. That
// pass is gone, so the row is the only thing standing between a padded
// paste and a host nobody can resolve.
func TestEmailNormalizeTrims(t *testing.T) {
	t.Parallel()

	got := emailSetting.Normalize(EmailConfig{
		SMTP: EmailSMTPConfig{Host: "  smtp.example.com  ", Username: "  postmaster  "},
		From: EmailFromConfig{Address: "  library@example.com  ", Name: "  embookshelf  "},
		// A trailing slash would double up against every path the
		// mail templates append.
		PublicURL: "  https://books.example.com/  ",
	})

	if got.SMTP.Host != "smtp.example.com" || got.SMTP.Username != "postmaster" {
		t.Errorf("SMTP not trimmed: %+v", got.SMTP)
	}
	if got.From.Address != "library@example.com" || got.From.Name != "embookshelf" {
		t.Errorf("From not trimmed: %+v", got.From)
	}
	if got.PublicURL != "https://books.example.com" {
		t.Errorf("PublicURL = %q, want trimmed with no trailing slash", got.PublicURL)
	}
	if got.SMTP.TLS != "starttls" {
		t.Errorf("TLS = %q, want the starttls default", got.SMTP.TLS)
	}
}
