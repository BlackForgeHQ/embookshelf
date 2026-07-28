// SPDX-License-Identifier: AGPL-3.0-or-later

package tts

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
)

// outputMP3 is the response format every adapter asks for. Fixed by us
// rather than left to the engine, because byte-wise concatenation is only
// valid while every segment of a run comes back with identical
// parameters (ADR-0027).
const outputMP3 = "mp3"

// ---------------------------------------------------------------------------
// OpenAI-compatible
// ---------------------------------------------------------------------------

// openAIEngine speaks POST /audio/speech, which OpenAI itself,
// Kokoro-FastAPI, openedai-speech and LocalAI all implement. One adapter,
// cloud and local — the same property that made ADR-0024 pick a base-URL
// seam for chat.
type openAIEngine struct{ cfg Config }

func (e *openAIEngine) speak(ctx context.Context, r Request) ([]byte, error) {
	req, err := jsonRequest(http.MethodPost, e.cfg.BaseURL+"/audio/speech", map[string]any{
		"model":           r.Model,
		"input":           r.Text,
		"voice":           r.Voice,
		"response_format": outputMP3,
	})
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.cfg.APIKey)
	return audioFrom(ctx, e.cfg, req)
}

// openAIVoices is a static list because the endpoint has none. A local
// server may well offer different names; the field is free text in the
// settings form for exactly that reason.
var openAIVoices = []Voice{
	{ID: "alloy", Label: "Alloy"},
	{ID: "ash", Label: "Ash"},
	{ID: "ballad", Label: "Ballad"},
	{ID: "coral", Label: "Coral"},
	{ID: "echo", Label: "Echo"},
	{ID: "fable", Label: "Fable"},
	{ID: "nova", Label: "Nova"},
	{ID: "onyx", Label: "Onyx"},
	{ID: "sage", Label: "Sage"},
	{ID: "shimmer", Label: "Shimmer"},
}

func (e *openAIEngine) ListVoices(context.Context) ([]Voice, error) {
	return openAIVoices, nil
}

// ---------------------------------------------------------------------------
// ElevenLabs
// ---------------------------------------------------------------------------

// elevenLabsEngine puts the voice in the path and the key in its own
// header. Voices are per-account — the whole product is that you pick or
// clone one — so the list has to be fetched.
type elevenLabsEngine struct{ cfg Config }

func (e *elevenLabsEngine) speak(ctx context.Context, r Request) ([]byte, error) {
	url := fmt.Sprintf("%s/text-to-speech/%s?output_format=mp3_44100_128", e.cfg.BaseURL, r.Voice)
	req, err := jsonRequest(http.MethodPost, url, map[string]any{
		"text":     r.Text,
		"model_id": r.Model,
	})
	if err != nil {
		return nil, err
	}
	req.Header.Set("xi-api-key", e.cfg.APIKey)
	return audioFrom(ctx, e.cfg, req)
}

func (e *elevenLabsEngine) ListVoices(ctx context.Context) ([]Voice, error) {
	req, err := http.NewRequest(http.MethodGet, e.cfg.BaseURL+"/voices", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("xi-api-key", e.cfg.APIKey)

	body, err := do(ctx, e.cfg, req)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Voices []struct {
			VoiceID string `json:"voice_id"`
			Name    string `json:"name"`
		} `json:"voices"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse elevenlabs voices: %w", err)
	}
	out := make([]Voice, 0, len(payload.Voices))
	for _, v := range payload.Voices {
		out = append(out, Voice{ID: v.VoiceID, Label: v.Name})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Azure Speech
// ---------------------------------------------------------------------------

// azureOutputFormat is the closest thing Azure offers to the other two.
// Azure has no 44.1 kHz MP3 at all, which is fine: concatenation needs
// every segment of one run to match, not every engine to match.
const azureOutputFormat = "audio-24khz-96kbitrate-mono-mp3"

// azureEngine posts SSML rather than JSON, and selects everything —
// language, gender, style — through the voice name. BaseURL is the
// region host, e.g. https://westeurope.tts.speech.microsoft.com.
type azureEngine struct{ cfg Config }

func (e *azureEngine) speak(ctx context.Context, r Request) ([]byte, error) {
	body := buildSSML(r.Voice, r.Text)
	req, err := http.NewRequest(http.MethodPost, e.cfg.BaseURL+"/cognitiveservices/v1", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/ssml+xml")
	req.Header.Set("X-Microsoft-OutputFormat", azureOutputFormat)
	req.Header.Set("Ocp-Apim-Subscription-Key", e.cfg.APIKey)
	// Azure rejects requests with no user agent on some regions.
	req.Header.Set("User-Agent", "embookshelf")
	return audioFrom(ctx, e.cfg, req)
}

// buildSSML wraps text for Azure.
//
// The escaping is the whole reason this is a function. SSML is XML and
// book prose is full of ampersands, angle brackets and quotes; unescaped,
// they either corrupt the request or get narrated as markup. xml.EscapeText
// rather than a hand-rolled replacer, so the set of escapes is the
// standard library's problem.
func buildSSML(voice, text string) string {
	var escaped strings.Builder
	if err := xml.EscapeText(&escaped, []byte(text)); err != nil {
		// EscapeText only fails if the writer does, and a strings.Builder
		// cannot. Fall through with whatever was written.
		_ = err
	}
	var escapedVoice strings.Builder
	_ = xml.EscapeText(&escapedVoice, []byte(voice))

	return `<speak version="1.0" xmlns="http://www.w3.org/2001/10/synthesis" xml:lang="en-US">` +
		`<voice name="` + escapedVoice.String() + `">` + escaped.String() + `</voice></speak>`
}

func (e *azureEngine) ListVoices(ctx context.Context) ([]Voice, error) {
	req, err := http.NewRequest(http.MethodGet, e.cfg.BaseURL+"/cognitiveservices/voices/list", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Ocp-Apim-Subscription-Key", e.cfg.APIKey)

	body, err := do(ctx, e.cfg, req)
	if err != nil {
		return nil, err
	}
	var payload []struct {
		ShortName   string `json:"ShortName"`
		DisplayName string `json:"DisplayName"`
		LocaleName  string `json:"LocaleName"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse azure voices: %w", err)
	}
	out := make([]Voice, 0, len(payload))
	for _, v := range payload {
		label := v.DisplayName
		if v.LocaleName != "" {
			label = fmt.Sprintf("%s (%s)", v.DisplayName, v.LocaleName)
		}
		out = append(out, Voice{ID: v.ShortName, Label: label})
	}
	return out, nil
}
