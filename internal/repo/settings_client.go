// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"time"

	"github.com/blackforge/embookshelf/internal/llm"
	"github.com/blackforge/embookshelf/internal/tts"
)

// A settings row answers "give me a working client" rather than exposing
// fields for each caller to assemble one from.
//
// Four call sites used to build these configs by hand — the queue job,
// the admin connection test, and the same pair on the audiobook side —
// and nothing forced them to agree. They did not: the reading guide
// worker omitted AuthStyle, so llm.New defaulted to bearer and every
// generation 401'd against an Azure endpoint while the connection test,
// which did pass it, reported success. The one path that spends money was
// the one path that ignored the setting.
//
// The fix is depth: the row owns the whole mapping, so a caller cannot
// spell a subset of it.

// probeTimeout bounds a connection test. Deliberately far shorter than
// either generation timeout — a test that hangs for five minutes teaches
// the admin nothing.
const probeTimeout = 30 * time.Second

// llmConfig maps the row onto the client's config. The single place the
// two vocabularies meet.
func (c ReadingGuideConfig) llmConfig(timeout time.Duration) llm.Config {
	return llm.Config{
		BaseURL:         c.BaseURL,
		Model:           c.Model,
		APIKey:          c.APIKey,
		AuthStyle:       llm.AuthStyle(c.AuthStyle),
		Timeout:         timeout,
		RequestJSONMode: c.RequestJSONMode,
	}
}

// Client builds the Guide generator's client with the generous timeout a
// full-text guide needs.
func (c ReadingGuideConfig) Client() (*llm.Client, error) {
	return llm.New(c.llmConfig(llm.DefaultTimeout))
}

// ProbeClient builds the same client for the admin connection test, with
// a timeout scaled to a question rather than to a generation.
func (c ReadingGuideConfig) ProbeClient() (*llm.Client, error) {
	return llm.New(c.llmConfig(probeTimeout))
}

// ConfiguredEngine is the selected TTS engine, resolved in one call
// instead of four lookups: the catalog id, that engine's stored
// settings, and the adapter built from them, ready to drive.
//
// It used to carry the catalog Info too, because a caller once had to
// look up the engine's per-request cap to chunk a segment correctly.
// That call moved inside the adapter — Synthesize now takes a whole
// segment and chunks it internally — so the cap has nothing left to
// travel for.
type ConfiguredEngine struct {
	ID       tts.EngineID
	Settings AudiobookEngineConfig
	Engine   tts.Engine
}

// requestTimeout is the admin-configured bound on one engine call.
func (c AudiobookConfig) requestTimeout() time.Duration {
	return time.Duration(c.RequestTimeoutSeconds) * time.Second
}

// ttsConfig maps an engine slot onto the adapter's config.
func (c AudiobookConfig) ttsConfig(engine AudiobookEngineConfig, timeout time.Duration) tts.Config {
	return tts.Config{
		BaseURL: engine.BaseURL,
		APIKey:  engine.APIKey,
		Timeout: timeout,
	}
}

func (c AudiobookConfig) selectEngine(timeout time.Duration) (ConfiguredEngine, error) {
	id, engineCfg, err := c.SelectedEngine()
	if err != nil {
		return ConfiguredEngine{}, err
	}
	engine, err := tts.New(id, c.ttsConfig(engineCfg, timeout))
	if err != nil {
		return ConfiguredEngine{}, err
	}
	return ConfiguredEngine{ID: id, Settings: engineCfg, Engine: engine}, nil
}

// SelectEngine resolves and builds the selected engine for a run.
func (c AudiobookConfig) SelectEngine() (ConfiguredEngine, error) {
	return c.selectEngine(c.requestTimeout())
}

// ProbeEngine builds the same engine for the admin connection test.
func (c AudiobookConfig) ProbeEngine() (ConfiguredEngine, error) {
	return c.selectEngine(probeTimeout)
}
