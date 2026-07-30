// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/blackforge/embookshelf/internal/provider"
	"github.com/blackforge/embookshelf/internal/repo"
)

// providerSettingsStore is the admin half of provider_settings: the config
// blobs boot pushes into the live providers, the rows the Settings panel
// renders, and the three writes an admin makes.
//
// It holds no health telemetry. RecordSuccess and RecordError belong to
// the fan-out, which is a different service behind providerRunStore — this
// one reads what the admin set and writes what the admin changed, and a
// fake for it that stubbed the counters was answering for a call that can
// never arrive.
type providerSettingsStore interface {
	AllConfigs(ctx context.Context) (map[string]json.RawMessage, error)
	List(ctx context.Context) ([]repo.ProviderSetting, error)
	SetConfig(ctx context.Context, id string, config json.RawMessage) error
	SetEnabled(ctx context.Context, id string, enabled bool) error
	SetPriority(ctx context.Context, id string, priority *int) error
}

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
// Secret handling deliberately does NOT live here. Encryption is a
// DB-boundary concern (ADR-0010 §4), so the Cipher and the password-kind
// slot walk sit on ProviderSettingsRepo, where every write crosses one
// seam. Holding them here made encryption a property of this call path
// rather than of the row: SetConfig accepted whatever blob it was
// handed, so a second writer would have stored plaintext silently.
type ProviderSettingsService struct {
	providers []provider.Provider
	settings  providerSettingsStore
}

func NewProviderSettingsService(
	providers []provider.Provider,
	settings providerSettingsStore,
) *ProviderSettingsService {
	return &ProviderSettingsService{providers: providers, settings: settings}
}

// LoadConfigs pushes stored provider configs into the matching
// provider instances. Called on service boot. A Configure failure is
// logged per provider — one broken blob shouldn't wedge the others.
//
// The blobs arrive plaintext: the store decrypts password-kind fields,
// so the live Configure call cannot be handed ciphertext by accident.
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
		if err := configurable.Configure(cfg); err != nil {
			slog.Warn("provider configure failed", "provider", p.Name(), "err", err)
		}
	}
	return nil
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
			// No decrypt step: List already returned plaintext. The admin
			// UI renders these straight into password inputs, so a blob
			// that reached here still encrypted would be re-encrypted by
			// the next Save.
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
// The blob is handed to the store as plaintext; encrypting password-kind
// fields is the store's obligation. The live provider gets the same
// plaintext via Configure, so it never carries a Cipher and the next
// outbound HTTP request works immediately (ADR-0010 §4).
func (s *ProviderSettingsService) SetProviderConfig(ctx context.Context, id string, cfg []byte) error {
	info, ok := provider.CatalogLookup(id)
	if !ok {
		return ErrUnknownProvider
	}
	if err := s.settings.SetConfig(ctx, id, cfg); err != nil {
		return err
	}
	for _, p := range s.providers {
		if p.Name() != info.ID {
			continue
		}
		if configurable, isCfg := p.(provider.Configurable); isCfg {
			return configurable.Configure(cfg)
		}
		return nil
	}
	return nil
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
