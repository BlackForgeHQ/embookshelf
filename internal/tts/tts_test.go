// SPDX-License-Identifier: AGPL-3.0-or-later

package tts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mustEngine(t *testing.T, id EngineID, cfg Config) Engine {
	t.Helper()
	e, err := New(id, cfg)
	if err != nil {
		t.Fatalf("New(%s): %v", id, err)
	}
	return e
}

// ---------------------------------------------------------------------------
// Catalog
// ---------------------------------------------------------------------------

// The catalog is what the settings UI renders and what the estimate
// prices from, so every entry has to be complete.
func TestCatalogEntriesAreComplete(t *testing.T) {
	t.Parallel()

	if len(Catalog) != 3 {
		t.Fatalf("catalog has %d engines, want 3 (ADR-0026 §2)", len(Catalog))
	}
	seen := map[EngineID]bool{}
	for _, info := range Catalog {
		if seen[info.ID] {
			t.Errorf("duplicate catalog id %q", info.ID)
		}
		seen[info.ID] = true
		if info.Label == "" {
			t.Errorf("%s has no label", info.ID)
		}
		if info.MaxRequestChars <= 0 {
			t.Errorf("%s has no per-request character cap", info.ID)
		}
		if info.DefaultPricePerMillionChars < 0 {
			t.Errorf("%s has a negative default price", info.ID)
		}
		if _, err := New(info.ID, Config{BaseURL: "https://x", APIKey: "k"}); err != nil {
			t.Errorf("catalog lists %s but New refuses it: %v", info.ID, err)
		}
	}
}

func TestNewRejectsAnUnknownEngine(t *testing.T) {
	t.Parallel()

	if _, err := New("polly", Config{}); err == nil {
		t.Fatal("want an error for an engine outside the catalog, got nil")
	}
}

// ---------------------------------------------------------------------------
// OpenAI-compatible
// ---------------------------------------------------------------------------

func TestOpenAISynthesizePostsToAudioSpeech(t *testing.T) {
	t.Parallel()

	var (
		gotPath string
		gotAuth string
		gotBody map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte("mp3-bytes"))
	}))
	defer srv.Close()

	eng := mustEngine(t, EngineOpenAI, Config{BaseURL: srv.URL + "/v1", APIKey: "sk-test"})
	got, err := eng.Synthesize(context.Background(), Request{
		Text:  "Hello there.",
		Voice: "alloy",
		Model: "tts-1",
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	if string(got) != "mp3-bytes" {
		t.Errorf("audio = %q, want the response body verbatim", got)
	}
	if gotPath != "/v1/audio/speech" {
		t.Errorf("path = %q, want /v1/audio/speech", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization = %q, want a bearer token", gotAuth)
	}
	if gotBody["input"] != "Hello there." {
		t.Errorf("input = %v, want the text", gotBody["input"])
	}
	if gotBody["voice"] != "alloy" {
		t.Errorf("voice = %v, want alloy", gotBody["voice"])
	}
	// Concatenation only works if every segment comes back in the same
	// format, so the format is ours to fix, not the engine's to choose.
	if gotBody["response_format"] != "mp3" {
		t.Errorf("response_format = %v, want mp3", gotBody["response_format"])
	}
}

// OpenAI's voices are a fixed list, so there is nothing to call.
func TestOpenAIListVoicesNeedsNoNetwork(t *testing.T) {
	t.Parallel()

	eng := mustEngine(t, EngineOpenAI, Config{BaseURL: "http://127.0.0.1:1", APIKey: "k"})
	voices, err := eng.ListVoices(context.Background())
	if err != nil {
		t.Fatalf("ListVoices: %v", err)
	}
	if len(voices) == 0 {
		t.Fatal("want a static voice list, got none")
	}
	for _, v := range voices {
		if v.ID == "" || v.Label == "" {
			t.Errorf("incomplete voice %+v", v)
		}
	}
}

// ---------------------------------------------------------------------------
// ElevenLabs
// ---------------------------------------------------------------------------

func TestElevenLabsSynthesizeUsesVoiceInPathAndKeyHeader(t *testing.T) {
	t.Parallel()

	var (
		gotPath   string
		gotKey    string
		gotFormat string
		gotBody   map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("xi-api-key")
		gotFormat = r.URL.Query().Get("output_format")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte("eleven-mp3"))
	}))
	defer srv.Close()

	eng := mustEngine(t, EngineElevenLabs, Config{BaseURL: srv.URL, APIKey: "xi-test"})
	got, err := eng.Synthesize(context.Background(), Request{
		Text:  "Hello there.",
		Voice: "voice-abc",
		Model: "eleven_multilingual_v2",
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	if string(got) != "eleven-mp3" {
		t.Errorf("audio = %q, want the response body verbatim", got)
	}
	if !strings.HasSuffix(gotPath, "/text-to-speech/voice-abc") {
		t.Errorf("path = %q, want the voice id in the path", gotPath)
	}
	if gotKey != "xi-test" {
		t.Errorf("xi-api-key = %q, want the configured key", gotKey)
	}
	if gotFormat != "mp3_44100_128" {
		t.Errorf("output_format = %q, want mp3_44100_128", gotFormat)
	}
	if gotBody["text"] != "Hello there." {
		t.Errorf("text = %v, want the text", gotBody["text"])
	}
}

func TestElevenLabsListVoicesReadsTheVoicesEndpoint(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/voices") {
			t.Errorf("path = %q, want /voices", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"voices":[
			{"voice_id":"v1","name":"Rachel"},
			{"voice_id":"v2","name":"Adam"}
		]}`)
	}))
	defer srv.Close()

	eng := mustEngine(t, EngineElevenLabs, Config{BaseURL: srv.URL, APIKey: "k"})
	voices, err := eng.ListVoices(context.Background())
	if err != nil {
		t.Fatalf("ListVoices: %v", err)
	}
	if len(voices) != 2 {
		t.Fatalf("got %d voices, want 2", len(voices))
	}
	if voices[0].ID != "v1" || voices[0].Label != "Rachel" {
		t.Errorf("voices[0] = %+v, want {v1 Rachel}", voices[0])
	}
}

// ---------------------------------------------------------------------------
// Azure Speech
// ---------------------------------------------------------------------------

func TestAzureSynthesizeSendsSSMLWithSubscriptionKey(t *testing.T) {
	t.Parallel()

	var (
		gotPath   string
		gotKey    string
		gotFormat string
		gotBody   string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("Ocp-Apim-Subscription-Key")
		gotFormat = r.Header.Get("X-Microsoft-OutputFormat")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte("azure-mp3"))
	}))
	defer srv.Close()

	eng := mustEngine(t, EngineAzure, Config{BaseURL: srv.URL, APIKey: "az-test"})
	got, err := eng.Synthesize(context.Background(), Request{
		Text:  "Hello there.",
		Voice: "en-US-JennyNeural",
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	if string(got) != "azure-mp3" {
		t.Errorf("audio = %q, want the response body verbatim", got)
	}
	if gotPath != "/cognitiveservices/v1" {
		t.Errorf("path = %q, want /cognitiveservices/v1", gotPath)
	}
	if gotKey != "az-test" {
		t.Errorf("subscription key header = %q, want the configured key", gotKey)
	}
	if !strings.Contains(gotFormat, "mp3") {
		t.Errorf("output format = %q, want an mp3 format", gotFormat)
	}
	if !strings.Contains(gotBody, `<voice name="en-US-JennyNeural">`) {
		t.Errorf("body = %q, want SSML naming the voice", gotBody)
	}
	if !strings.Contains(gotBody, "Hello there.") {
		t.Errorf("body = %q, want the text", gotBody)
	}
}

// Book prose is full of ampersands and angle brackets, and SSML is XML:
// unescaped text either corrupts the request or gets read aloud as markup.
func TestAzureSynthesizeEscapesTheText(t *testing.T) {
	t.Parallel()

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	eng := mustEngine(t, EngineAzure, Config{BaseURL: srv.URL, APIKey: "k"})
	if _, err := eng.Synthesize(context.Background(), Request{
		Text:  `Smith & Sons said "<no>"`,
		Voice: "en-US-JennyNeural",
	}); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	if strings.Contains(gotBody, "<no>") {
		t.Error("raw angle brackets reached the SSML body")
	}
	if !strings.Contains(gotBody, "&amp;") {
		t.Error("ampersand was not escaped")
	}
}

// ---------------------------------------------------------------------------
// Error mapping — the worker decides whether to retry from this
// ---------------------------------------------------------------------------

// A bad key will still be bad in thirty seconds. Retrying it 25 times
// wastes a River slot and buries the real cause in the log.
func TestSynthesizeMarksClientErrorsPermanent(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"invalid api key"}`)
	}))
	defer srv.Close()

	eng := mustEngine(t, EngineOpenAI, Config{BaseURL: srv.URL, APIKey: "bad"})
	_, err := eng.Synthesize(context.Background(), Request{Text: "x", Voice: "alloy", Model: "tts-1"})
	if err == nil {
		t.Fatal("want an error for 401, got nil")
	}
	if !errors.Is(err, ErrPermanent) {
		t.Errorf("401 must be permanent, got %v", err)
	}
	if !strings.Contains(err.Error(), "invalid api key") {
		t.Errorf("error %q does not carry the engine's own message", err)
	}
}

// Rate limits and server errors are exactly what retrying is for.
func TestSynthesizeLeavesRateLimitsAndServerErrorsRetryable(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))

		eng := mustEngine(t, EngineOpenAI, Config{BaseURL: srv.URL, APIKey: "k"})
		_, err := eng.Synthesize(context.Background(), Request{Text: "x", Voice: "alloy", Model: "tts-1"})
		srv.Close()

		if err == nil {
			t.Errorf("status %d: want an error, got nil", status)
			continue
		}
		if errors.Is(err, ErrPermanent) {
			t.Errorf("status %d must stay retryable, got a permanent error", status)
		}
	}
}

// An engine that answers 200 with nothing has failed, and splicing zero
// bytes into the concatenation would silently shorten the book.
func TestSynthesizeRejectsAnEmptyBody(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	eng := mustEngine(t, EngineOpenAI, Config{BaseURL: srv.URL, APIKey: "k"})
	if _, err := eng.Synthesize(context.Background(), Request{Text: "x", Voice: "alloy", Model: "tts-1"}); err == nil {
		t.Fatal("want an error for an empty 200, got nil")
	}
}
