// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/crypto"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

func testCipher(t *testing.T) crypto.Cipher {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	c, err := crypto.NewAESGCM(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatalf("NewAESGCM: %v", err)
	}
	return c
}

func newSettingsRepo(t *testing.T) *repo.AppSettingsRepo {
	t.Helper()
	return repo.NewAppSettingsRepo(repotest.New(t), testCipher(t))
}

// rawValue reads the stored JSONB verbatim — the only way to prove a
// secret is actually encrypted at rest rather than merely round-tripping.
func rawValue(t *testing.T, r *repo.AppSettingsRepo, key string) string {
	t.Helper()
	raw, err := r.GetRaw(context.Background(), key)
	if err != nil {
		t.Fatalf("GetRaw(%s): %v", key, err)
	}
	return string(raw)
}

// ---------------------------------------------------------------------------
// OIDC client secrets — ADR-0010 promises these are encrypted at rest.
// ---------------------------------------------------------------------------

func TestGenericOIDCSecretEncryptedAtRest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newSettingsRepo(t)

	err := r.SetGenericOIDC(ctx, repo.GenericOIDCConfig{
		Enabled:      true,
		ClientID:     "client-abc",
		ClientSecret: "s3cr3t-value",
		IssuerURI:    "https://idp.example.com",
	})
	if err != nil {
		t.Fatalf("SetGenericOIDC: %v", err)
	}

	stored := rawValue(t, r, repo.SettingOIDCGeneric)
	if strings.Contains(stored, "s3cr3t-value") {
		t.Fatalf("client secret stored in plaintext: %s", stored)
	}
	if !strings.Contains(stored, "client-abc") {
		t.Errorf("non-secret fields must stay inspectable, got: %s", stored)
	}

	got, err := r.GetGenericOIDC(ctx)
	if err != nil {
		t.Fatalf("GetGenericOIDC: %v", err)
	}
	if got.ClientSecret != "s3cr3t-value" {
		t.Fatalf("ClientSecret = %q, want the plaintext back", got.ClientSecret)
	}
	if got.ClientID != "client-abc" || !got.Enabled {
		t.Errorf("non-secret fields lost: %+v", got)
	}
}

func TestPresetOIDCSecretEncryptedAtRest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newSettingsRepo(t)

	for _, tc := range []struct {
		name string
		key  string
		set  func(context.Context, repo.OAuthPresetConfig) error
		get  func(context.Context) (repo.OAuthPresetConfig, error)
	}{
		{"google", repo.SettingOIDCGoogle, r.SetGoogle, r.GetGoogle},
		{"github", repo.SettingOIDCGitHub, r.SetGitHub, r.GetGitHub},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.set(ctx, repo.OAuthPresetConfig{
				Enabled:      true,
				ClientID:     "id-" + tc.name,
				ClientSecret: "secret-" + tc.name,
			}); err != nil {
				t.Fatalf("set: %v", err)
			}
			if stored := rawValue(t, r, tc.key); strings.Contains(stored, "secret-"+tc.name) {
				t.Fatalf("client secret stored in plaintext: %s", stored)
			}
			got, err := tc.get(ctx)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if got.ClientSecret != "secret-"+tc.name {
				t.Fatalf("ClientSecret = %q, want plaintext back", got.ClientSecret)
			}
		})
	}
}

// Pre-encryption rows carry no enc:v1: prefix. They must keep working —
// the Cipher passes them through and the next write upgrades them.
func TestOIDCSecretPlaintextRowStillReadable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newSettingsRepo(t)

	legacy := `{"enabled":true,"clientId":"legacy-id","clientSecret":"legacy-plaintext"}`
	if err := r.SetRaw(ctx, repo.SettingOIDCGoogle, []byte(legacy)); err != nil {
		t.Fatalf("SetRaw: %v", err)
	}

	got, err := r.GetGoogle(ctx)
	if err != nil {
		t.Fatalf("GetGoogle: %v", err)
	}
	if got.ClientSecret != "legacy-plaintext" {
		t.Fatalf("ClientSecret = %q, want the legacy plaintext", got.ClientSecret)
	}

	// Next write upgrades the row to ciphertext.
	if err := r.SetGoogle(ctx, got); err != nil {
		t.Fatalf("SetGoogle: %v", err)
	}
	if stored := rawValue(t, r, repo.SettingOIDCGoogle); strings.Contains(stored, "legacy-plaintext") {
		t.Fatalf("re-save must upgrade the row to ciphertext, got: %s", stored)
	}
}

// ---------------------------------------------------------------------------
// Email — the domain that already encrypted, now via the shared mechanism.
// ---------------------------------------------------------------------------

func TestEmailSecretEncryptedAtRest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newSettingsRepo(t)

	cfg := repo.DefaultEmailConfig()
	cfg.Enabled = true
	cfg.SMTP.Host = "smtp.example.com"
	cfg.SMTP.Username = "postmaster"
	cfg.SMTP.Password = "smtp-hunter2"
	cfg.From.Address = "books@example.com"
	// An enabled row now has to be a complete one — the row validates
	// what only the HTTP handler used to.
	cfg.PublicURL = "https://books.example.com"
	if err := r.SetEmail(ctx, cfg); err != nil {
		t.Fatalf("SetEmail: %v", err)
	}

	stored := rawValue(t, r, repo.SettingEmail)
	if strings.Contains(stored, "smtp-hunter2") {
		t.Fatalf("smtp password stored in plaintext: %s", stored)
	}
	if !strings.Contains(stored, "smtp.example.com") {
		t.Errorf("non-secret fields must stay inspectable, got: %s", stored)
	}

	got, err := r.GetEmail(ctx)
	if err != nil {
		t.Fatalf("GetEmail: %v", err)
	}
	if got.SMTP.Password != "smtp-hunter2" {
		t.Fatalf("Password = %q, want plaintext back", got.SMTP.Password)
	}
	if got.SMTP.Host != "smtp.example.com" || !got.Enabled {
		t.Errorf("non-secret fields lost: %+v", got)
	}
}

func TestEmailNormalizesOnSave(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newSettingsRepo(t)

	cfg := repo.DefaultEmailConfig()
	cfg.SMTP.Host = "  smtp.example.com  "
	cfg.PublicURL = "https://books.example.com/"
	cfg.SMTP.TLS = ""
	if err := r.SetEmail(ctx, cfg); err != nil {
		t.Fatalf("SetEmail: %v", err)
	}

	got, err := r.GetEmail(ctx)
	if err != nil {
		t.Fatalf("GetEmail: %v", err)
	}
	if got.SMTP.Host != "smtp.example.com" {
		t.Errorf("Host = %q, want trimmed", got.SMTP.Host)
	}
	if got.PublicURL != "https://books.example.com" {
		t.Errorf("PublicURL = %q, want trailing slash stripped", got.PublicURL)
	}
	if got.SMTP.TLS != "starttls" {
		t.Errorf("TLS = %q, want starttls default", got.SMTP.TLS)
	}
}

// ---------------------------------------------------------------------------
// Missing rows fall back to defaults; seeding is idempotent.
// ---------------------------------------------------------------------------

func TestMissingRowsYieldDefaults(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newSettingsRepo(t)

	email, err := r.GetEmail(ctx)
	if err != nil {
		t.Fatalf("GetEmail: %v", err)
	}
	if email != repo.DefaultEmailConfig() {
		t.Errorf("email = %+v, want DefaultEmailConfig", email)
	}

	fwd, err := r.GetForwardAuth(ctx)
	if err != nil {
		t.Fatalf("GetForwardAuth: %v", err)
	}
	if fwd.Headers.User != repo.DefaultForwardAuthConfig().Headers.User {
		t.Errorf("forward-auth = %+v, want defaults", fwd)
	}

	ap, err := r.GetOIDCAutoProvision(ctx)
	if err != nil {
		t.Fatalf("GetOIDCAutoProvision: %v", err)
	}
	if ap != repo.DefaultOIDCAutoProvisionDetails() {
		t.Errorf("auto-provision = %+v, want defaults", ap)
	}
}

func TestSeedIsIdempotentAndPreservesEdits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newSettingsRepo(t)

	if err := r.SeedAll(ctx); err != nil {
		t.Fatalf("SeedAll: %v", err)
	}
	cfg := repo.DefaultEmailConfig()
	cfg.SMTP.Host = "edited.example.com"
	if err := r.SetEmail(ctx, cfg); err != nil {
		t.Fatalf("SetEmail: %v", err)
	}
	if err := r.SeedAll(ctx); err != nil {
		t.Fatalf("second SeedAll: %v", err)
	}

	got, err := r.GetEmail(ctx)
	if err != nil {
		t.Fatalf("GetEmail: %v", err)
	}
	if got.SMTP.Host != "edited.example.com" {
		t.Fatalf("seed overwrote an edited row: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// Forward-auth — no secrets, but validation must survive the migration.
// ---------------------------------------------------------------------------

func TestForwardAuthValidationRejectsOnSave(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newSettingsRepo(t)

	err := r.SetForwardAuth(ctx, repo.ForwardAuthConfig{
		Enabled: true, // no CIDRs
		Headers: repo.ForwardAuthHeaders{User: "Remote-User"},
	})
	if err == nil {
		t.Fatal("want ErrForwardAuthEnabledWithoutCIDR, got nil")
	}
}

func TestForwardAuthRoundTripNormalizes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newSettingsRepo(t)

	err := r.SetForwardAuth(ctx, repo.ForwardAuthConfig{
		Enabled:           true,
		TrustedProxyCIDRs: []string{" 10.0.0.0/8 ", ""},
		Headers:           repo.ForwardAuthHeaders{User: " Remote-User "},
		LogoutURL:         "https://sso.example.com/logout/",
	})
	if err != nil {
		t.Fatalf("SetForwardAuth: %v", err)
	}

	got, err := r.GetForwardAuth(ctx)
	if err != nil {
		t.Fatalf("GetForwardAuth: %v", err)
	}
	if len(got.TrustedProxyCIDRs) != 1 || got.TrustedProxyCIDRs[0] != "10.0.0.0/8" {
		t.Errorf("CIDRs = %v, want [10.0.0.0/8]", got.TrustedProxyCIDRs)
	}
	if got.Headers.User != "Remote-User" {
		t.Errorf("user header = %q, want trimmed", got.Headers.User)
	}
	if got.LogoutURL != "https://sso.example.com/logout" {
		t.Errorf("logoutUrl = %q, want trailing slash stripped", got.LogoutURL)
	}
}
