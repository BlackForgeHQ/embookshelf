// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/blackforge/embookshelf/internal/crypto"
	"github.com/blackforge/embookshelf/internal/provider"
	"github.com/blackforge/embookshelf/internal/repo"
)

// ProviderSettingsService is the admin surface over provider_settings:
// which metadata providers are enabled, their config blobs, their ranking,
// and the health telemetry the Settings panel renders.
//
// It was carved out of EnrichmentService, which had grown to hold six
// unrelated concerns behind one struct. This half is genuinely
// independent — no fan-out or apply path calls it — so it earns its own
// module. Cover intake deliberately stayed behind: ApplyMatch calls it,
// so separating would have added a dependency without removing coupling.
//
// Secret handling lives here because this is the only place provider
// config is written: password-kind fields declared by a provider's
// ConfigSchema are AES-GCM encrypted in place (ADR-0010), so a config
// blob is legible in psql except for its secrets.
type ProviderSettingsService struct {
	providers []provider.Provider
	settings  providerSettingsStore
	cipher    crypto.Cipher
}

func NewProviderSettingsService(
	providers []provider.Provider,
	settings providerSettingsStore,
	cipher crypto.Cipher,
) *ProviderSettingsService {
	if cipher == nil {
		cipher = crypto.Noop{}
	}
	return &ProviderSettingsService{providers: providers, settings: settings, cipher: cipher}
}

// LoadConfigs pushes stored provider configs into the matching
// provider instances. Called on service boot. Failures are logged per
// provider — one broken blob shouldn't wedge the others.
//
// Password-kind fields are decrypted before being handed to the
// provider so the live Configure call always sees plaintext.
func (s *ProviderSettingsService) LoadConfigs(ctx context.Context) error {
	raw, err := s.settings.AllConfigs(ctx)
	if err != nil {
		return err
	}
	for _, p := range s.providers {
		cfg, ok := raw[string(p.Name())]
		if !ok {
			continue
		}
		configurable, isCfg := p.(provider.Configurable)
		if !isCfg {
			continue
		}
		decoded, err := s.decryptConfigFields(cfg, p)
		if err != nil {
			slog.Warn("provider config decrypt failed", "provider", p.Name(), "err", err)
			continue
		}
		if err := configurable.Configure(decoded); err != nil {
			slog.Warn("provider configure failed", "provider", p.Name(), "err", err)
		}
	}
	return nil
}

// passwordFields returns the set of config keys a provider flags as
// password-kind — i.e. values that should be encrypted on disk.
func passwordFields(p provider.Provider) map[string]struct{} {
	sp, ok := p.(provider.SchemaProvider)
	if !ok {
		return nil
	}
	out := map[string]struct{}{}
	for _, f := range sp.ConfigSchema() {
		if f.Kind == provider.ConfigFieldPassword {
			out[f.Key] = struct{}{}
		}
	}
	return out
}

// encryptConfigFields walks a config blob and encrypts the string
// values whose keys are declared password-kind in the provider's
// schema. Returns the re-marshaled blob; non-password keys pass
// through verbatim so the stored JSON stays inspectable.
func (s *ProviderSettingsService) encryptConfigFields(cfg []byte, p provider.Provider) ([]byte, error) {
	return s.transformConfigFields(cfg, p, s.cipher.Encrypt)
}

// decryptConfigFields is the inverse of encryptConfigFields. Applied
// after every DB read so callers see plaintext secrets.
func (s *ProviderSettingsService) decryptConfigFields(cfg []byte, p provider.Provider) ([]byte, error) {
	return s.transformConfigFields(cfg, p, s.cipher.Decrypt)
}

func (s *ProviderSettingsService) transformConfigFields(
	cfg []byte, p provider.Provider, op func(string) (string, error),
) ([]byte, error) {
	if len(cfg) == 0 || bytes.Equal(bytes.TrimSpace(cfg), []byte("{}")) {
		return cfg, nil
	}
	pw := passwordFields(p)
	if len(pw) == 0 {
		return cfg, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(cfg, &obj); err != nil {
		return nil, err
	}
	// Same slot transformer the typed settings rows use — provider
	// config differs only in how its secret slots are discovered
	// (declared at runtime by the provider's schema, not by struct
	// field pointers).
	keys := make([]string, 0, len(pw))
	values := make([]string, 0, len(pw))
	for key := range pw {
		str, ok := obj[key].(string)
		if !ok {
			continue
		}
		keys = append(keys, key)
		values = append(values, str)
	}
	slots := make([]*string, len(values))
	for i := range values {
		slots[i] = &values[i]
	}
	if err := crypto.TransformSlots(op, slots); err != nil {
		return nil, err
	}
	for i, key := range keys {
		obj[key] = values[i]
	}
	return json.Marshal(obj)
}

// ListProviders joins the static catalog with the live per-row state
// (enabled + config + priority) and the provider's declared schema.
// Missing rows count as disabled with an empty config. The returned
// slice is in catalog order; the handler re-sorts by priority for the
// admin UI if it wants chain order.
func (s *ProviderSettingsService) ListProviders(ctx context.Context) ([]ProviderInfo, error) {
	rows, err := s.settings.List(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]repo.ProviderSetting, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	byProvider := make(map[provider.Source]provider.Provider, len(s.providers))
	for _, p := range s.providers {
		byProvider[p.Name()] = p
	}
	out := make([]ProviderInfo, 0, len(provider.Catalog))
	for _, c := range provider.Catalog {
		info := ProviderInfo{
			ID:       c.ID,
			Name:     c.Name,
			External: c.External,
		}
		if row, ok := byID[string(c.ID)]; ok {
			info.Enabled = row.Enabled
			info.Priority = row.Priority
			info.Config = []byte(row.Config)
			info.LastSuccessAt = row.LastSuccessAt
			info.LastErrorAt = row.LastErrorAt
			info.LastError = row.LastError
		}
		if p, ok := byProvider[c.ID]; ok {
			if sp, ok := p.(provider.SchemaProvider); ok {
				info.Schema = sp.ConfigSchema()
			}
			// Decrypt password-kind fields for the admin UI. Failures
			// return the raw blob so an admin can at least see the row
			// exists (the input will be gibberish, which is a visible
			// signal the KEK rotated out of sync).
			if decoded, err := s.decryptConfigFields(info.Config, p); err != nil {
				slog.Warn("list providers config decrypt", "provider", c.ID, "err", err)
			} else {
				info.Config = decoded
			}
		}
		out = append(out, info)
	}
	return out, nil
}

// SetProviderEnabled flips the enabled flag for a single provider. The
// id must match an entry in the static catalog; unknown ids return
// ErrUnknownProvider rather than silently upserting junk.
func (s *ProviderSettingsService) SetProviderEnabled(ctx context.Context, id string, enabled bool) error {
	if _, ok := provider.CatalogLookup(id); !ok {
		return ErrUnknownProvider
	}
	return s.settings.SetEnabled(ctx, id, enabled)
}

// SetProviderConfig stores a new config blob and pushes it into the
// running provider instance. Invalid JSON is caught by Configure and
// surfaced to the caller so a save button can flash an error.
//
// Password-kind fields (API keys, cookies) are encrypted before they
// land in the DB; the live provider gets the plaintext copy via
// Configure so the next outbound HTTP request works immediately.
func (s *ProviderSettingsService) SetProviderConfig(ctx context.Context, id string, cfg []byte) error {
	info, ok := provider.CatalogLookup(id)
	if !ok {
		return ErrUnknownProvider
	}
	var matched provider.Provider
	for _, p := range s.providers {
		if p.Name() == info.ID {
			matched = p
			break
		}
	}

	toStore := cfg
	if matched != nil {
		encrypted, err := s.encryptConfigFields(cfg, matched)
		if err != nil {
			return err
		}
		toStore = encrypted
	}
	if err := s.settings.SetConfig(ctx, id, toStore); err != nil {
		return err
	}
	if matched == nil {
		return nil
	}
	configurable, isCfg := matched.(provider.Configurable)
	if !isCfg {
		return nil
	}
	// The live Configure call reads the plaintext blob — decryption
	// round-tripping through the DB isn't required.
	return configurable.Configure(cfg)
}

// SetProviderPriority stores the sort priority. nil clears it.
func (s *ProviderSettingsService) SetProviderPriority(ctx context.Context, id string, priority *int) error {
	if _, ok := provider.CatalogLookup(id); !ok {
		return ErrUnknownProvider
	}
	return s.settings.SetPriority(ctx, id, priority)
}

// ProviderInfo is the handler-facing DTO shape: static catalog facts
// joined with the live enabled flag, config blob, priority, health
// telemetry, and the admin-UI schema describing which inputs to render.
type ProviderInfo struct {
	ID       provider.Source
	Name     string
	Enabled  bool
	External bool
	Priority *int
	// Config is the stored blob, pass-through to the UI. Providers that
	// aren't Configurable always have nil here.
	Config []byte
	// Schema describes the form fields the Settings panel should render.
	// nil when the provider doesn't declare a schema.
	Schema []provider.ConfigField
	// Health — last-success / last-error timestamps and the most
	// recent error string. Nil timestamps mean "never observed."
	LastSuccessAt *time.Time
	LastErrorAt   *time.Time
	LastError     string
}

// ErrUnknownProvider is returned by SetProviderEnabled when the caller
// hands in an id the binary doesn't recognize.
var ErrUnknownProvider = errors.New("unknown provider")
