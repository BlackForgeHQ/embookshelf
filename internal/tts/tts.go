// SPDX-License-Identifier: AGPL-3.0-or-later

// Package tts is the seam over text-to-speech engines.
//
// Unlike the LLM seam next door, this one is a catalog. embookshelf has
// twice refused a provider catalog — ADR-0020 for email, ADR-0024 for the
// LLM — and both times the reasoning was that the vendors differed in
// billing rather than capability, so a second adapter would have been a
// copy of the first. Speech engines are not like that: they differ in
// per-request cap by a factor of eight, in whether they accept SSML at
// all, in whether they can report word timings, and in price by a factor
// of twenty. Here the second and third adapters are real on day one
// (ADR-0026).
//
// Engines are *selected*, not fanned out. Narrating a book through three
// engines would produce three books and three bills, so ADR-0013's
// graceful-degrade policy has no analogue here: a failed engine is a
// failed generation.
package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrPermanent marks a failure that retrying cannot fix — a bad key, a
// voice that does not exist, text the engine refuses. The queue worker
// branches on it: a permanent failure fails the segment immediately
// rather than burning River's retry budget on an answer that will not
// change (ADR-0028 §6).
var ErrPermanent = errors.New("permanent")

// DefaultTimeout bounds one synthesis request. Generous because a 40k
// character segment is minutes of audio to generate, and short enough
// that a hung connection does not hold a worker slot all day.
const DefaultTimeout = 5 * time.Minute

// EngineID names a catalog entry.
type EngineID string

const (
	EngineOpenAI     EngineID = "openai"
	EngineElevenLabs EngineID = "elevenlabs"
	EngineAzure      EngineID = "azure"
)

// Info is one catalog entry: everything the settings UI and the estimate
// need to know about an engine without instantiating it.
type Info struct {
	ID    EngineID
	Label string
	// MaxRequestChars is the engine's own per-request limit. The segment
	// planner never emits a segment larger than this.
	MaxRequestChars int
	// DefaultPricePerMillionChars seeds the admin's editable price. A
	// default, not a source of truth — prices change without our releases
	// and a stale underestimate is the dangerous direction (ADR-0028 §2).
	DefaultPricePerMillionChars float64
	// DefaultBaseURL is the endpoint when there is one obvious choice.
	// Empty for OpenAI-compatible, where the whole point is that the
	// operator picks (cloud, or a local Kokoro).
	DefaultBaseURL string
	// DefaultModel is the model id most people want.
	DefaultModel string
	// NeedsModel reports whether the engine takes a model at all. Azure
	// selects everything through the voice name.
	NeedsModel bool
	// newSpeaker builds this engine's adapter. Declared on the catalog
	// entry so adding an engine is one entry rather than an entry plus a
	// switch case somewhere else that nothing forces to agree with it.
	//
	// Unexported because only this package declares engines. The
	// settings handler reads Info's named fields and never marshals it.
	newSpeaker func(Config) speaker
}

// Catalog is the single declaration of which engines this binary knows.
// Hard-coded and rebuilt to change, like the metadata provider catalog
// (ADR-0008). Adding Polly or Google is one entry plus one adapter.
var Catalog = []Info{
	{
		ID:                          EngineOpenAI,
		Label:                       "OpenAI-compatible",
		MaxRequestChars:             4096,
		DefaultPricePerMillionChars: 15,
		DefaultModel:                "tts-1",
		NeedsModel:                  true,
		newSpeaker:                  func(c Config) speaker { return &openAIEngine{cfg: c} },
	},
	{
		ID:                          EngineElevenLabs,
		Label:                       "ElevenLabs",
		MaxRequestChars:             5000,
		DefaultPricePerMillionChars: 180,
		DefaultBaseURL:              "https://api.elevenlabs.io/v1",
		DefaultModel:                "eleven_multilingual_v2",
		NeedsModel:                  true,
		newSpeaker:                  func(c Config) speaker { return &elevenLabsEngine{cfg: c} },
	},
	{
		ID:                          EngineAzure,
		Label:                       "Azure Speech",
		MaxRequestChars:             8000,
		DefaultPricePerMillionChars: 15,
		NeedsModel:                  false,
		newSpeaker:                  func(c Config) speaker { return &azureEngine{cfg: c} },
	},
}

// Lookup returns the catalog entry for id.
func Lookup(id EngineID) (Info, bool) {
	for _, info := range Catalog {
		if info.ID == id {
			return info, true
		}
	}
	return Info{}, false
}

// Voice is one selectable narrator.
type Voice struct {
	ID    string
	Label string
}

// Request is one unit of synthesis.
type Request struct {
	Text  string
	Voice string
	Model string
}

// Config is what an adapter needs to reach its engine.
type Config struct {
	BaseURL string
	APIKey  string
	Timeout time.Duration
	// HTTPClient overrides the default. Tests use it; production does not.
	HTTPClient *http.Client
}

func (c Config) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &http.Client{Timeout: timeout}
}

// Engine is the seam every adapter implements.
//
// Two methods rather than one: the generate dialog has to populate a
// voice picker, and a voice list is the one thing every engine expresses
// differently enough that the caller cannot guess it (ADR-0026 §4).
type Engine interface {
	Synthesize(ctx context.Context, req Request) ([]byte, error)
	ListVoices(ctx context.Context) ([]Voice, error)
}

// speaker is one engine's adapter.
type speaker interface {
	Synthesize(ctx context.Context, r Request) ([]byte, error)
	ListVoices(ctx context.Context) ([]Voice, error)
}

// New builds the adapter for id.
func New(id EngineID, cfg Config) (Engine, error) {
	info, ok := Lookup(id)
	if !ok {
		return nil, fmt.Errorf("tts: unknown engine %q", id)
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = info.DefaultBaseURL
	}
	if base == "" {
		return nil, fmt.Errorf("tts: %s needs a base URL", id)
	}
	cfg.BaseURL = base

	if info.newSpeaker == nil {
		return nil, fmt.Errorf("tts: engine %q is in the catalog but has no adapter", id)
	}
	return info.newSpeaker(cfg), nil
}

// ---------------------------------------------------------------------------
// Shared HTTP
// ---------------------------------------------------------------------------

// do performs a request and returns the body, mapping status codes onto
// the retryable/permanent split the queue worker branches on.
//
// Deliberately no retry decorator here, following llm.Client: one
// synthesis call can run for minutes, so a blind retry multiplies both
// the wait and the bill. The queue owns whether to try again, at segment
// granularity, where the cost is visible.
func do(ctx context.Context, cfg Config, req *http.Request) ([]byte, error) {
	resp, err := cfg.client().Do(req.WithContext(ctx))
	if err != nil {
		// Transport failures are exactly what a retry is for.
		return nil, fmt.Errorf("tts request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 500 {
			msg = msg[:500]
		}
		// 429 is a rate limit and 5xx is the engine's problem; both pass
		// with time. Every other 4xx is a request we built wrong or a
		// credential that is wrong, and neither improves on retry.
		if resp.StatusCode/100 == 4 && resp.StatusCode != http.StatusTooManyRequests {
			return nil, fmt.Errorf("%w: engine returned %d: %s", ErrPermanent, resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("engine returned %d: %s", resp.StatusCode, msg)
	}
	if readErr != nil {
		return nil, fmt.Errorf("read engine response: %w", readErr)
	}
	return body, nil
}

// audioFrom wraps do for the synthesis calls, which all share the same
// "a 200 with no bytes is still a failure" rule. Splicing zero bytes into
// the concatenation would silently shorten the book by one chapter and
// leave every later chapter mark wrong.
func audioFrom(ctx context.Context, cfg Config, req *http.Request) ([]byte, error) {
	body, err := do(ctx, cfg, req)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, errors.New("engine returned no audio")
	}
	return body, nil
}

func jsonRequest(method, url string, payload any) (*http.Request, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}
