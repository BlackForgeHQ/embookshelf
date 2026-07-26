// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/crypto"
	"github.com/blackforge/embookshelf/internal/provider"
)

// schemaProvider is a stand-in metadata provider that declares one
// password-kind field and one plain field, which is all the secret walk
// cares about.
type schemaProvider struct {
	name   provider.Source
	fields []provider.ConfigField
	config []byte
}

func (p *schemaProvider) Name() provider.Source { return p.name }
func (p *schemaProvider) Search(context.Context, provider.Query) ([]provider.Match, error) {
	return nil, nil
}
func (p *schemaProvider) ConfigSchema() []provider.ConfigField { return p.fields }
func (p *schemaProvider) Configure(raw []byte) error           { p.config = raw; return nil }

func newSchemaProvider() *schemaProvider {
	return &schemaProvider{
		name: provider.Source("hardcover"),
		fields: []provider.ConfigField{
			{Key: "token", Kind: provider.ConfigFieldPassword},
			{Key: "language", Kind: provider.ConfigFieldText},
		},
	}
}

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

// Password-kind fields are encrypted in place; everything else stays
// readable so a stored config is still legible in psql (ADR-0010).
func TestProviderConfigEncryptsOnlyPasswordFields(t *testing.T) {
	t.Parallel()

	p := newSchemaProvider()
	svc := NewProviderSettingsService([]provider.Provider{p}, newFakeProviderSettings(), testCipher(t))

	plain := []byte(`{"token":"secret-token","language":"en"}`)
	enc, err := svc.encryptConfigFields(plain, p)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if strings.Contains(string(enc), "secret-token") {
		t.Errorf("password field stored in plaintext: %s", enc)
	}
	var obj map[string]any
	if err := json.Unmarshal(enc, &obj); err != nil {
		t.Fatalf("encrypted blob is not valid JSON: %v", err)
	}
	if obj["language"] != "en" {
		t.Errorf("non-secret field was altered: %v", obj["language"])
	}

	back, err := svc.decryptConfigFields(enc, p)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	var round map[string]any
	if err := json.Unmarshal(back, &round); err != nil {
		t.Fatalf("decrypted blob is not valid JSON: %v", err)
	}
	if round["token"] != "secret-token" {
		t.Errorf("round-trip lost the secret: %v", round["token"])
	}
}

// A provider that declares no schema has no secrets to find; the blob
// must pass through untouched rather than being re-marshalled.
func TestProviderConfigWithoutSchemaPassesThrough(t *testing.T) {
	t.Parallel()

	plainProvider := &schemaProvider{name: provider.Source("openlibrary")}
	svc := NewProviderSettingsService(
		[]provider.Provider{plainProvider}, newFakeProviderSettings(), testCipher(t))

	in := []byte(`{"anything":"goes"}`)
	out, err := svc.encryptConfigFields(in, plainProvider)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if string(out) != string(in) {
		t.Errorf("blob = %s, want it untouched", out)
	}
}

func TestProviderConfigEmptyBlobIsNoop(t *testing.T) {
	t.Parallel()

	p := newSchemaProvider()
	svc := NewProviderSettingsService([]provider.Provider{p}, newFakeProviderSettings(), testCipher(t))

	for _, in := range [][]byte{nil, []byte(""), []byte("{}"), []byte("  {}  ")} {
		out, err := svc.encryptConfigFields(in, p)
		if err != nil {
			t.Fatalf("encrypt(%q): %v", in, err)
		}
		if string(out) != string(in) {
			t.Errorf("encrypt(%q) = %q, want unchanged", in, out)
		}
	}
}

// LoadConfigs hands each provider its stored config with secrets already
// decrypted — a provider must never receive ciphertext.
func TestLoadConfigsHandsProvidersPlaintext(t *testing.T) {
	t.Parallel()

	p := newSchemaProvider()
	settings := newFakeProviderSettings()
	svc := NewProviderSettingsService([]provider.Provider{p}, settings, testCipher(t))

	stored, err := svc.encryptConfigFields([]byte(`{"token":"live-token","language":"fr"}`), p)
	if err != nil {
		t.Fatalf("seed encrypt: %v", err)
	}
	settings.configs["hardcover"] = stored

	if err := svc.LoadConfigs(context.Background()); err != nil {
		t.Fatalf("LoadConfigs: %v", err)
	}
	if p.config == nil {
		t.Fatal("provider was never configured")
	}
	if !strings.Contains(string(p.config), "live-token") {
		t.Errorf("provider received %s, want the decrypted token", p.config)
	}
}

func TestSetProviderEnabledRejectsUnknownID(t *testing.T) {
	t.Parallel()

	svc := NewProviderSettingsService(
		[]provider.Provider{newSchemaProvider()}, newFakeProviderSettings(), testCipher(t))

	if err := svc.SetProviderEnabled(context.Background(), "not-a-provider", true); err == nil {
		t.Fatal("want ErrUnknownProvider for an id the binary doesn't ship")
	}
}

func TestSetProviderEnabledTogglesKnownID(t *testing.T) {
	t.Parallel()

	settings := newFakeProviderSettings()
	svc := NewProviderSettingsService(
		[]provider.Provider{newSchemaProvider()}, settings, testCipher(t))

	if err := svc.SetProviderEnabled(context.Background(), "hardcover", true); err != nil {
		t.Fatalf("SetProviderEnabled: %v", err)
	}
	if !settings.enabled["hardcover"] {
		t.Error("provider was not enabled in the store")
	}
}

// Config written through the service must land encrypted, not raw.
func TestSetProviderConfigStoresEncrypted(t *testing.T) {
	t.Parallel()

	settings := newFakeProviderSettings()
	svc := NewProviderSettingsService(
		[]provider.Provider{newSchemaProvider()}, settings, testCipher(t))

	err := svc.SetProviderConfig(context.Background(), "hardcover",
		[]byte(`{"token":"plaintext-secret","language":"en"}`))
	if err != nil {
		t.Fatalf("SetProviderConfig: %v", err)
	}

	stored := string(settings.configs["hardcover"])
	if strings.Contains(stored, "plaintext-secret") {
		t.Errorf("secret reached the store in plaintext: %s", stored)
	}
	if !strings.Contains(stored, "en") {
		t.Errorf("non-secret field lost: %s", stored)
	}
}
