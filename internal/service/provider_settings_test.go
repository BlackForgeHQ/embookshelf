// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/provider"
	"github.com/blackforge/embookshelf/internal/repo"
)

// fakeProviderConfigs stands in for providerSettingsStore — the admin
// half of provider_settings. It carries no health counters, because
// ProviderSettingsService never records either: the fan-out does, behind
// its own seam, with its own fake.
type fakeProviderConfigs struct {
	enabled    map[string]bool
	configs    map[string]json.RawMessage
	priorities map[string]*int
}

func newFakeProviderConfigs() *fakeProviderConfigs {
	return &fakeProviderConfigs{
		enabled:    map[string]bool{},
		configs:    map[string]json.RawMessage{},
		priorities: map[string]*int{},
	}
}

func (f *fakeProviderConfigs) AllConfigs(context.Context) (map[string]json.RawMessage, error) {
	return f.configs, nil
}

// List returns a nil slice when empty, matching what the repo yields for
// a table with no rows. Config rides along already decrypted, as the repo
// returns it — the property TestListProvidersSurfacesStoredConfig pins.
func (f *fakeProviderConfigs) List(context.Context) ([]repo.ProviderSetting, error) {
	var rows []repo.ProviderSetting
	for id, on := range f.enabled {
		rows = append(rows, repo.ProviderSetting{
			ID:       id,
			Enabled:  on,
			Config:   f.configs[id],
			Priority: f.priorities[id],
		})
	}
	return rows, nil
}

func (f *fakeProviderConfigs) SetConfig(_ context.Context, id string, cfg json.RawMessage) error {
	f.configs[id] = cfg
	return nil
}

func (f *fakeProviderConfigs) SetEnabled(_ context.Context, id string, on bool) error {
	f.enabled[id] = on
	return nil
}

func (f *fakeProviderConfigs) SetPriority(_ context.Context, id string, priority *int) error {
	f.priorities[id] = priority
	return nil
}

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

// ---------------------------------------------------------------------------
// This service owns no crypto. Encryption is a property of the row, so it
// sits on ProviderSettingsRepo (ADR-0010 §4) and is pinned by
// repo/provider_settings_test.go. What these tests pin is the other half
// of that split: config crosses this module untransformed in both
// directions, so there is no second place a secret could be mangled.
// ---------------------------------------------------------------------------

// SetProviderConfig hands the blob to the store exactly as it arrived.
// Transforming it here would double-encrypt once the repo does its job.
func TestSetProviderConfigPassesBlobToStoreUntouched(t *testing.T) {
	t.Parallel()

	settings := newFakeProviderConfigs()
	svc := NewProviderSettingsService([]provider.Provider{newSchemaProvider()}, settings)

	plain := `{"token":"plaintext-secret","language":"en"}`
	if err := svc.SetProviderConfig(context.Background(), "hardcover", []byte(plain)); err != nil {
		t.Fatalf("SetProviderConfig: %v", err)
	}
	if got := string(settings.configs["hardcover"]); got != plain {
		t.Errorf("store received %s, want the blob verbatim (%s)", got, plain)
	}
}

// ADR-0010 §4: a metadata provider never carries a Cipher, so the live
// Configure call must see plaintext on the save path.
func TestSetProviderConfigConfiguresProviderWithPlaintext(t *testing.T) {
	t.Parallel()

	p := newSchemaProvider()
	svc := NewProviderSettingsService([]provider.Provider{p}, newFakeProviderConfigs())

	err := svc.SetProviderConfig(context.Background(), "hardcover",
		[]byte(`{"token":"live-token","language":"en"}`))
	if err != nil {
		t.Fatalf("SetProviderConfig: %v", err)
	}
	if !strings.Contains(string(p.config), "live-token") {
		t.Errorf("provider received %s, want the plaintext token", p.config)
	}
}

// LoadConfigs is the boot path: whatever the store returns reaches
// Configure unchanged, because the store already decrypted it.
func TestLoadConfigsHandsProvidersWhatTheStoreReturns(t *testing.T) {
	t.Parallel()

	p := newSchemaProvider()
	settings := newFakeProviderConfigs()
	settings.configs["hardcover"] = []byte(`{"token":"live-token","language":"fr"}`)
	svc := NewProviderSettingsService([]provider.Provider{p}, settings)

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

// ListProviders surfaces the store's config as-is for the admin UI. A
// blob that still held ciphertext here would land in a password input and
// be re-encrypted by the next Save.
func TestListProvidersSurfacesStoredConfig(t *testing.T) {
	t.Parallel()

	p := newSchemaProvider()
	settings := newFakeProviderConfigs()
	settings.enabled["hardcover"] = true
	settings.configs["hardcover"] = []byte(`{"token":"live-token"}`)
	svc := NewProviderSettingsService([]provider.Provider{p}, settings)

	infos, err := svc.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	var found bool
	for _, info := range infos {
		if info.ID != provider.Source("hardcover") {
			continue
		}
		found = true
		if !strings.Contains(string(info.Config), "live-token") {
			t.Errorf("config = %s, want the plaintext token", info.Config)
		}
		if len(info.Schema) != 2 {
			t.Errorf("schema = %v, want the provider's two fields", info.Schema)
		}
	}
	if !found {
		t.Fatal("hardcover missing from the catalog join")
	}
}

func TestSetProviderEnabledRejectsUnknownID(t *testing.T) {
	t.Parallel()

	svc := NewProviderSettingsService(
		[]provider.Provider{newSchemaProvider()}, newFakeProviderConfigs())

	if err := svc.SetProviderEnabled(context.Background(), "not-a-provider", true); err == nil {
		t.Fatal("want ErrUnknownProvider for an id the binary doesn't ship")
	}
}

func TestSetProviderEnabledTogglesKnownID(t *testing.T) {
	t.Parallel()

	settings := newFakeProviderConfigs()
	svc := NewProviderSettingsService([]provider.Provider{newSchemaProvider()}, settings)

	if err := svc.SetProviderEnabled(context.Background(), "hardcover", true); err != nil {
		t.Fatalf("SetProviderEnabled: %v", err)
	}
	if !settings.enabled["hardcover"] {
		t.Error("provider was not enabled in the store")
	}
}
