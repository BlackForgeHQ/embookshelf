// SPDX-License-Identifier: AGPL-3.0-or-later

package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/blackforge/embookshelf/internal/audio/audiotest"
)

// fakeSpeaker records every text it was handed, in call order, and hands
// back whatever reply is configured for that call index. A nil reply
// returns one frame of silence — enough for tests that only care about
// what text arrived.
type fakeSpeaker struct {
	mu    sync.Mutex
	texts []string
	reply func(call int) ([]byte, error)
}

func (f *fakeSpeaker) speak(_ context.Context, r Request) ([]byte, error) {
	f.mu.Lock()
	call := len(f.texts)
	f.texts = append(f.texts, r.Text)
	f.mu.Unlock()
	if f.reply != nil {
		return f.reply(call)
	}
	return audiotest.Frames(1), nil
}

func (f *fakeSpeaker) ListVoices(context.Context) ([]Voice, error) {
	return nil, nil
}

func (f *fakeSpeaker) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.texts...)
}

// ---------------------------------------------------------------------------
// chunked.Synthesize
// ---------------------------------------------------------------------------

// A segment that already fits in one call must come back exactly as the
// engine sent it — including its own ID3 tag. join only decodes when there
// is more than one piece to stitch together.
func TestChunkedSynthesizePassesASinglePieceThroughUnchanged(t *testing.T) {
	t.Parallel()

	want := append([]byte("ID3-fake-tag-bytes-"), audiotest.Frames(2)...)
	sp := &fakeSpeaker{reply: func(int) ([]byte, error) { return want, nil }}
	c := chunked{speaker: sp, maxChars: 1000}

	got, err := c.Synthesize(context.Background(), Request{Text: "A short segment that fits in one call."})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if calls := sp.calls(); len(calls) != 1 {
		t.Fatalf("speak called %d times, want 1", len(calls))
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got %d bytes, want the engine's reply untouched byte-for-byte", len(got))
	}
}

// A segment over the cap becomes several calls, each within the cap, with
// no text dropped or reordered across the seams. (Sentence-boundary
// placement itself is textsplit's own contract, tested in
// internal/textsplit; this only has to prove chunked hands OnSentences'
// pieces to speak in order.)
func TestChunkedSynthesizeSplitsAboveCapPreservingOrderAndText(t *testing.T) {
	t.Parallel()

	sentence := "This is a sentence of a reasonable length. "
	text := strings.Repeat(sentence, 60) // ~2.5k chars
	sp := &fakeSpeaker{}
	c := chunked{speaker: sp, maxChars: 500}

	if _, err := c.Synthesize(context.Background(), Request{Text: text}); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	pieces := sp.calls()
	if len(pieces) < 2 {
		t.Fatalf("speak called %d times, want the segment split across several", len(pieces))
	}
	if !strings.HasPrefix(strings.TrimSpace(text), pieces[0]) {
		t.Errorf("first piece %q is not a prefix of the segment — pieces arrived out of order", pieces[0])
	}
	var joined strings.Builder
	for i, p := range pieces {
		if n := len([]rune(p)); n > 500 {
			t.Errorf("piece %d is %d chars, over the 500 cap", i, n)
		}
		joined.WriteString(p)
		joined.WriteByte(' ')
	}
	if got, want := len(strings.Fields(joined.String())), len(strings.Fields(text)); got != want {
		t.Errorf("word count across pieces = %d, want %d — a piece was dropped or reordered", got, want)
	}
}

// The join is what turns several engine calls back into one file: the
// total frame count must equal the sum of what each call returned.
func TestChunkedSynthesizeConcatenatesMultiPieceAudio(t *testing.T) {
	t.Parallel()

	text := strings.Repeat("Alpha beta gamma delta epsilon zeta. ", 40)
	frameCounts := []int{2, 3, 1, 4, 2, 3, 1, 4, 2, 3, 1, 4, 2, 3, 1, 4}
	sp := &fakeSpeaker{reply: func(call int) ([]byte, error) {
		return audiotest.Frames(frameCounts[call%len(frameCounts)]), nil
	}}
	c := chunked{speaker: sp, maxChars: 100}

	got, err := c.Synthesize(context.Background(), Request{Text: text})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	calls := sp.calls()
	if len(calls) < 2 {
		t.Fatalf("got %d calls, want several so the join path is exercised", len(calls))
	}
	wantFrames := 0
	for i := range calls {
		wantFrames += frameCounts[i%len(frameCounts)]
	}
	if gotLen, wantLen := len(got), wantFrames*audiotest.FrameBytes; gotLen != wantLen {
		t.Errorf("joined audio is %d bytes, want %d (%d frames)", gotLen, wantLen, wantFrames)
	}
}

// BeforeChunk runs once per engine call, immediately before it — never
// batched at the start and never skipped for a later chunk.
func TestChunkedSynthesizeCallsBeforeChunkBeforeEverySpeak(t *testing.T) {
	t.Parallel()

	text := strings.Repeat("Alpha beta gamma delta. ", 30)
	var mu sync.Mutex
	var order []string
	sp := &fakeSpeaker{reply: func(int) ([]byte, error) {
		mu.Lock()
		order = append(order, "speak")
		mu.Unlock()
		return audiotest.Frames(1), nil
	}}
	c := chunked{speaker: sp, maxChars: 100}

	before := func(context.Context) error {
		mu.Lock()
		order = append(order, "before")
		mu.Unlock()
		return nil
	}

	if _, err := c.Synthesize(context.Background(), Request{Text: text, BeforeChunk: before}); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	n := len(sp.calls())
	if n < 2 {
		t.Fatalf("got %d chunks, want several so the ordering is meaningful", n)
	}
	if len(order) != 2*n {
		t.Fatalf("recorded %d events for %d chunks, want exactly two each", len(order), n)
	}
	for i := 0; i < len(order); i += 2 {
		if order[i] != "before" || order[i+1] != "speak" {
			t.Fatalf("order = %v, want alternating before/speak pairs", order)
		}
	}
}

// errAbort is a caller sentinel — a stand-in for the real BeforeChunk's own
// cancellation error. Its identity, not its text, is what has to survive
// the round trip through chunked.Synthesize unwrapped.
var errAbort = errors.New("caller: abort requested")

// BeforeChunk failing must stop the loop immediately, hand back the
// caller's own sentinel unwrapped, and never reach the engine again.
func TestChunkedSynthesizeAbortsWhenBeforeChunkErrors(t *testing.T) {
	t.Parallel()

	text := strings.Repeat("Alpha beta gamma delta. ", 30)
	sp := &fakeSpeaker{}
	c := chunked{speaker: sp, maxChars: 100}

	calls := 0
	before := func(context.Context) error {
		calls++
		if calls == 2 {
			return errAbort
		}
		return nil
	}

	_, err := c.Synthesize(context.Background(), Request{Text: text, BeforeChunk: before})
	if !errors.Is(err, errAbort) {
		t.Fatalf("err = %v, want the caller's own sentinel to match via errors.Is", err)
	}
	if got := len(sp.calls()); got != 1 {
		t.Errorf("speak called %d times, want exactly the one chunk that ran before the abort", got)
	}
}

// A context already cancelled before the loop starts must stop it before
// the first engine call — and before the first BeforeChunk call too, since
// ctx.Err() is checked first in the loop.
func TestChunkedSynthesizeStopsOnACanceledContextBeforeTheFirstCall(t *testing.T) {
	t.Parallel()

	sp := &fakeSpeaker{}
	c := chunked{speaker: sp, maxChars: 1000}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	beforeCalls := 0
	before := func(context.Context) error {
		beforeCalls++
		return nil
	}

	_, err := c.Synthesize(ctx, Request{Text: "short text", BeforeChunk: before})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := len(sp.calls()); got != 0 {
		t.Errorf("speak called %d times, want zero for an already-canceled context", got)
	}
	if beforeCalls != 0 {
		t.Errorf("BeforeChunk called %d times, want zero — the ctx check runs first", beforeCalls)
	}
}

// Pins #185 rather than endorsing it: unusable engine audio is a permanent
// failure at one chunk (the caller's own audio.Payload check, outside this
// package) but only this untagged "chunk %d: %w" here once a segment spans
// more than one chunk — so the caller sees a plain error, not one matching
// tts.ErrPermanent, and retries forever. Fixing that asymmetry here would
// be a behaviour change smuggled into a refactor; it stays exactly as it
// was in internal/task before this move.
func TestChunkedJoinWrapsMultiChunkFailureUntagged(t *testing.T) {
	t.Parallel()

	text := strings.Repeat("Alpha beta gamma delta. ", 30)
	sp := &fakeSpeaker{reply: func(int) ([]byte, error) {
		return []byte("this is not an mp3"), nil
	}}
	c := chunked{speaker: sp, maxChars: 100}

	_, err := c.Synthesize(context.Background(), Request{Text: text})
	if err == nil {
		t.Fatal("want an error joining unusable multi-chunk audio, got nil")
	}
	if !strings.HasPrefix(err.Error(), "chunk 0: ") {
		t.Errorf("err = %q, want it to start with the untagged \"chunk N: \" wrap", err.Error())
	}
	if errors.Is(err, ErrPermanent) {
		t.Errorf("err = %v is tagged ErrPermanent — #185 is precisely that this join path is not", err)
	}
}

// ---------------------------------------------------------------------------
// Per-adapter caps — the point of moving chunking inside at all
// ---------------------------------------------------------------------------

func mustCap(t *testing.T, id EngineID) int {
	t.Helper()
	info, ok := Lookup(id)
	if !ok {
		t.Fatalf("Lookup(%s): not in the catalog", id)
	}
	return info.MaxRequestChars
}

// segmentOverCap builds text several times longer than cap so the split
// always produces more than one piece regardless of sentence placement.
func segmentOverCap(sentence string, maxChars int) string {
	return strings.Repeat(sentence, maxChars*3/len(sentence)+2)
}

func TestOpenAIChunksToItsOwnCap(t *testing.T) {
	t.Parallel()
	maxChars := mustCap(t, EngineOpenAI)

	var (
		mu    sync.Mutex
		texts []string
		errs  []error
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input string `json:"input"`
		}
		err := json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		if err != nil {
			errs = append(errs, err)
		} else {
			texts = append(texts, body.Input)
		}
		mu.Unlock()
		_, _ = w.Write(audiotest.Frames(2))
	}))
	defer srv.Close()

	eng := mustEngine(t, EngineOpenAI, Config{BaseURL: srv.URL, APIKey: "k"})
	text := segmentOverCap("This is a sentence of reasonable length for chunking tests. ", maxChars)

	if _, err := eng.Synthesize(context.Background(), Request{Text: text, Voice: "alloy", Model: "tts-1"}); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, e := range errs {
		t.Fatalf("decode request body: %v", e)
	}
	if len(texts) < 2 {
		t.Fatalf("got %d requests, want several — the segment exceeds OpenAI's %d-char cap", len(texts), maxChars)
	}
	for i, tx := range texts {
		if n := len([]rune(tx)); n > maxChars {
			t.Errorf("request %d's input is %d chars, over OpenAI's own %d-char cap", i, n, maxChars)
		}
	}
}

func TestElevenLabsChunksToItsOwnCap(t *testing.T) {
	t.Parallel()
	maxChars := mustCap(t, EngineElevenLabs)

	var (
		mu    sync.Mutex
		texts []string
		errs  []error
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		err := json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		if err != nil {
			errs = append(errs, err)
		} else {
			texts = append(texts, body.Text)
		}
		mu.Unlock()
		_, _ = w.Write(audiotest.Frames(2))
	}))
	defer srv.Close()

	eng := mustEngine(t, EngineElevenLabs, Config{BaseURL: srv.URL, APIKey: "k"})
	text := segmentOverCap("This is a sentence of reasonable length for chunking tests. ", maxChars)

	if _, err := eng.Synthesize(context.Background(), Request{Text: text, Voice: "voice-abc", Model: "eleven_multilingual_v2"}); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, e := range errs {
		t.Fatalf("decode request body: %v", e)
	}
	if len(texts) < 2 {
		t.Fatalf("got %d requests, want several — the segment exceeds ElevenLabs's %d-char cap", len(texts), maxChars)
	}
	largest := 0
	for i, tx := range texts {
		if n := len([]rune(tx)); n > maxChars {
			t.Errorf("request %d's text is %d chars, over ElevenLabs's own %d-char cap", i, n, maxChars)
		} else if n > largest {
			largest = n
		}
	}
	// An upper bound alone can't tell ElevenLabs's own 5000-char cap apart
	// from a shared cap borrowed from a smaller sibling: at a shared 4096,
	// every piece here would still land under both caps and this test would
	// pass regardless of which cap actually drove the split. At least one
	// piece has to land strictly above OpenAI's cap to prove that didn't
	// happen.
	if openAICap := mustCap(t, EngineOpenAI); largest <= openAICap {
		t.Errorf("largest request is %d chars, want at least one over OpenAI's %d-char cap — "+
			"otherwise a cap shared with OpenAI would pass this test too", largest, openAICap)
	}
}

// azureVoiceText pulls the narrated text back out of one SSML request
// body and unescapes it. Comparing the raw SSML body's length against the
// cap would be wrong: the <speak>/<voice> wrapper adds overhead of its
// own, and any ampersand or angle bracket in the text is escaped to
// something longer (& becomes &amp;), so the escaped body can run over the
// cap even when the actual piece textsplit produced does not.
func azureVoiceText(body string) (string, error) {
	const openTag = `<voice name="`
	i := strings.Index(body, openTag)
	if i < 0 {
		return "", fmt.Errorf("no <voice> element in %q", body)
	}
	rest := body[i+len(openTag):]
	j := strings.Index(rest, `">`)
	if j < 0 {
		return "", fmt.Errorf("malformed voice tag in %q", body)
	}
	rest = rest[j+len(`">`):]
	k := strings.Index(rest, "</voice>")
	if k < 0 {
		return "", fmt.Errorf("no closing </voice> in %q", body)
	}
	escaped := rest[:k]

	var v struct {
		Text string `xml:",chardata"`
	}
	if err := xml.Unmarshal([]byte("<x>"+escaped+"</x>"), &v); err != nil {
		return "", fmt.Errorf("unescape voice text: %w", err)
	}
	return v.Text, nil
}

func TestAzureChunksToItsOwnCap(t *testing.T) {
	t.Parallel()
	maxChars := mustCap(t, EngineAzure)

	var (
		mu    sync.Mutex
		texts []string
		errs  []error
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		text, err := azureVoiceText(string(b))
		mu.Lock()
		if err != nil {
			errs = append(errs, err)
		} else {
			texts = append(texts, text)
		}
		mu.Unlock()
		_, _ = w.Write(audiotest.Frames(2))
	}))
	defer srv.Close()

	eng := mustEngine(t, EngineAzure, Config{BaseURL: srv.URL, APIKey: "k"})
	// The ampersand forces escaping, so this segment exercises the
	// unescape-before-comparing step above rather than accidentally
	// passing because there was nothing to escape.
	text := segmentOverCap("Smith & Sons agreed to the terms today. ", maxChars)

	if _, err := eng.Synthesize(context.Background(), Request{Text: text, Voice: "en-US-JennyNeural"}); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, e := range errs {
		t.Fatalf("read request body: %v", e)
	}
	if len(texts) < 2 {
		t.Fatalf("got %d requests, want several — the segment exceeds Azure's %d-char cap", len(texts), maxChars)
	}
	largest := 0
	for i, tx := range texts {
		if n := len([]rune(tx)); n > maxChars {
			t.Errorf("request %d's unescaped text is %d chars, over Azure's own %d-char cap", i, n, maxChars)
		} else if n > largest {
			largest = n
		}
	}
	// Same reasoning as the ElevenLabs test: an upper bound alone doesn't
	// distinguish Azure's own 8000-char cap from a shared cap borrowed from
	// a smaller sibling. At least one piece has to land strictly above
	// ElevenLabs's cap to prove Azure's own, larger cap actually drove the
	// split.
	if elevenLabsCap := mustCap(t, EngineElevenLabs); largest <= elevenLabsCap {
		t.Errorf("largest request is %d chars, want at least one over ElevenLabs's %d-char cap — "+
			"otherwise a cap shared with ElevenLabs would pass this test too", largest, elevenLabsCap)
	}
}
