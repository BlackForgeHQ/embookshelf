// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// secretKeys stands in for the runtime schema walk main.go supplies:
// "hardcover" declares one password-kind field, everything else declares
// none. Provider config differs from a Setting only in how its secret
// slots are discovered — declared at runtime rather than by struct field
// pointers — so a plain map is a faithful stand-in.
func secretKeys(id string) []string {
	if id == "hardcover" {
		return []string{"token"}
	}
	return nil
}

func newProviderSettingsRepo(t *testing.T) (*repo.ProviderSettingsRepo, *db.DB) {
	t.Helper()
	d := repotest.New(t)
	return repo.NewProviderSettingsRepo(d, testCipher(t), secretKeys), d
}

// rawConfig reads the stored JSONB verbatim, bypassing the repo. This is
// the only way to prove a secret is encrypted at rest rather than merely
// round-tripping through a cipher that happens to be symmetric.
func rawConfig(t *testing.T, d *db.DB, id string) string {
	t.Helper()
	var raw []byte
	err := d.SQL.QueryRowContext(context.Background(),
		`SELECT config FROM provider_settings WHERE id = $1`, id).Scan(&raw)
	if err != nil {
		t.Fatalf("read raw config for %s: %v", id, err)
	}
	return string(raw)
}

// ---------------------------------------------------------------------------
// The decisive test: the repo is the seam that encrypts, so a secret
// written through it reaches storage as ciphertext and comes back
// plaintext — no caller can forget, the way no Setting accessor can.
// ---------------------------------------------------------------------------

func TestProviderConfigSecretEncryptedAtRest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, d := newProviderSettingsRepo(t)

	err := r.SetConfig(ctx, "hardcover",
		json.RawMessage(`{"token":"hc-live-token","language":"en"}`))
	if err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	stored := rawConfig(t, d, "hardcover")
	if strings.Contains(stored, "hc-live-token") {
		t.Fatalf("password field stored in plaintext: %s", stored)
	}
	if !strings.Contains(stored, "en") {
		t.Errorf("non-secret fields must stay inspectable in psql, got: %s", stored)
	}

	// Both read paths decrypt: the admin surface (List) and the boot
	// push into provider instances (AllConfigs).
	rows, err := r.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var found bool
	for _, row := range rows {
		if row.ID != "hardcover" {
			continue
		}
		found = true
		assertConfigField(t, "List", row.Config, "token", "hc-live-token")
		assertConfigField(t, "List", row.Config, "language", "en")
	}
	if !found {
		t.Fatal("List did not return the hardcover row")
	}

	configs, err := r.AllConfigs(ctx)
	if err != nil {
		t.Fatalf("AllConfigs: %v", err)
	}
	assertConfigField(t, "AllConfigs", configs["hardcover"], "token", "hc-live-token")
}

// A provider that declares no secret keys has nothing to transform; the
// blob must reach storage byte-for-byte rather than being re-marshalled.
func TestProviderConfigWithoutSecretsStoredVerbatim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, d := newProviderSettingsRepo(t)

	if err := r.SetConfig(ctx, "amazon", json.RawMessage(`{"domain":"de"}`)); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if stored := rawConfig(t, d, "amazon"); !strings.Contains(stored, `"de"`) {
		t.Errorf("config = %s, want the value stored untouched", stored)
	}

	configs, err := r.AllConfigs(ctx)
	if err != nil {
		t.Fatalf("AllConfigs: %v", err)
	}
	assertConfigField(t, "AllConfigs", configs["amazon"], "domain", "de")
}

// Rows written before encryption was wired carry no enc:v1: prefix. The
// Cipher passes them through and the next write upgrades them, so an
// upgrading instance needs no migration (ADR-0010).
func TestProviderConfigPlaintextRowStillReadable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, d := newProviderSettingsRepo(t)

	legacy := `{"token":"legacy-plaintext","language":"fr"}`
	_, err := d.SQL.ExecContext(ctx,
		`INSERT INTO provider_settings (id, enabled, config) VALUES ($1, true, $2)`,
		"hardcover", legacy)
	if err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	configs, err := r.AllConfigs(ctx)
	if err != nil {
		t.Fatalf("AllConfigs: %v", err)
	}
	assertConfigField(t, "AllConfigs", configs["hardcover"], "token", "legacy-plaintext")

	// Writing it back upgrades the row to ciphertext.
	if err := r.SetConfig(ctx, "hardcover", configs["hardcover"]); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if stored := rawConfig(t, d, "hardcover"); strings.Contains(stored, "legacy-plaintext") {
		t.Fatalf("re-save must upgrade the row to ciphertext, got: %s", stored)
	}
}

// An empty blob has no slots; SetConfig must not turn it into a
// ciphertext of "" or a re-marshalled object.
func TestProviderConfigEmptyBlobIsNoop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, d := newProviderSettingsRepo(t)

	for _, in := range []json.RawMessage{nil, json.RawMessage("{}"), json.RawMessage("  {}  ")} {
		if err := r.SetConfig(ctx, "hardcover", in); err != nil {
			t.Fatalf("SetConfig(%q): %v", in, err)
		}
		if stored := strings.TrimSpace(rawConfig(t, d, "hardcover")); stored != "{}" {
			t.Errorf("SetConfig(%q) stored %q, want {}", in, stored)
		}
	}
}

// A declared secret key that is absent from the blob is not an error —
// TransformSlots skips empty and missing slots, so an unset token stays
// unset rather than becoming a ciphertext of "".
func TestProviderConfigMissingSecretKeyIsFine(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, _ := newProviderSettingsRepo(t)

	if err := r.SetConfig(ctx, "hardcover", json.RawMessage(`{"language":"en"}`)); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	configs, err := r.AllConfigs(ctx)
	if err != nil {
		t.Fatalf("AllConfigs: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(configs["hardcover"], &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := obj["token"]; ok {
		t.Errorf("absent secret key was materialised: %v", obj)
	}
}

func assertConfigField(t *testing.T, path string, cfg json.RawMessage, key, want string) {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(cfg, &obj); err != nil {
		t.Fatalf("%s: config is not valid JSON: %v (%s)", path, err, cfg)
	}
	if got, _ := obj[key].(string); got != want {
		t.Errorf("%s: config[%q] = %q, want %q", path, key, got, want)
	}
}
