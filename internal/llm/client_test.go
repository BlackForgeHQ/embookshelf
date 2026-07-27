// SPDX-License-Identifier: AGPL-3.0-or-later

package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- the tolerant parser -------------------------------------------------

// Models wrap JSON in code fences, prefix it with "Here is the guide:", or
// both, and which of those happens varies by model and by run. The parser
// is what makes the feature behave the same on OpenAI and on a local
// Ollama instead of depending on schema enforcement neither guarantees.
func TestExtractJSONObject(t *testing.T) {
	tests := map[string]struct {
		in   string
		want string
		ok   bool
	}{
		"bare object":      {`{"a":1}`, `{"a":1}`, true},
		"json fence":       {"```json\n{\"a\":1}\n```", `{"a":1}`, true},
		"bare fence":       {"```\n{\"a\":1}\n```", `{"a":1}`, true},
		"prose before":     {"Here is the guide:\n{\"a\":1}", `{"a":1}`, true},
		"prose after":      {"{\"a\":1}\nHope that helps!", `{"a":1}`, true},
		"nested braces":    {`{"a":{"b":2}}`, `{"a":{"b":2}}`, true},
		"brace in string":  {`{"a":"}"}`, `{"a":"}"}`, true},
		"leading newlines": {"\n\n  {\"a\":1}", `{"a":1}`, true},
		"no object":        {"I cannot help with that.", "", false},
		"empty":            {"", "", false},
		"unclosed":         {`{"a":1`, "", false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := extractJSONObject(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (got %q)", ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// --- transport -----------------------------------------------------------

// recorder captures what the client actually sent, so the wire shape is
// asserted rather than assumed.
type recorder struct {
	reqs    []map[string]any
	headers []http.Header
	replies []string
	status  int
}

func (r *recorder) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", req.URL.Path)
		}
		body, _ := io.ReadAll(req.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)
		r.reqs = append(r.reqs, parsed)
		r.headers = append(r.headers, req.Header.Clone())

		if r.status != 0 && r.status != http.StatusOK {
			w.WriteHeader(r.status)
			_, _ = io.WriteString(w, `{"error":{"message":"nope"}}`)
			return
		}
		reply := ""
		if len(r.replies) > 0 {
			reply = r.replies[0]
			r.replies = r.replies[1:]
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testClient(t *testing.T, srv *httptest.Server, mut func(*Config)) *Client {
	t.Helper()
	cfg := Config{BaseURL: srv.URL, Model: "test-model", Timeout: 5 * time.Second}
	if mut != nil {
		mut(&cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestChatSendsModelAndMessages(t *testing.T) {
	rec := &recorder{replies: []string{"pong"}}
	c := testClient(t, rec.server(t), nil)

	got, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "ping"}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got != "pong" {
		t.Fatalf("content = %q, want pong", got)
	}
	if rec.reqs[0]["model"] != "test-model" {
		t.Errorf("model = %v", rec.reqs[0]["model"])
	}
	msgs, _ := rec.reqs[0]["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v", rec.reqs[0]["messages"])
	}
}

// TestChatOmitsAuthWhenNoKey — a local Ollama needs no key, and sending
// "Authorization: Bearer " has been known to make servers reject outright.
func TestChatOmitsAuthWhenNoKey(t *testing.T) {
	rec := &recorder{replies: []string{"ok"}}
	c := testClient(t, rec.server(t), nil)

	if _, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "x"}}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got := rec.headers[0].Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want none when no key is configured", got)
	}
}

func TestChatSendsAuthWhenKeySet(t *testing.T) {
	rec := &recorder{replies: []string{"ok"}}
	c := testClient(t, rec.server(t), func(cfg *Config) { cfg.APIKey = "sk-secret" })

	if _, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "x"}}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got := rec.headers[0].Get("Authorization"); got != "Bearer sk-secret" {
		t.Fatalf("Authorization = %q", got)
	}
}

// TestChatJSONModeOffByDefault — response_format is not universally
// supported across OpenAI-compatible servers, so it is opt-in and the
// prompt carries the contract instead.
func TestChatJSONModeOffByDefault(t *testing.T) {
	rec := &recorder{replies: []string{"ok"}}
	c := testClient(t, rec.server(t), nil)

	if _, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "x"}}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if _, present := rec.reqs[0]["response_format"]; present {
		t.Error("response_format sent without the operator enabling it")
	}
}

func TestChatJSONModeSentWhenEnabled(t *testing.T) {
	rec := &recorder{replies: []string{"ok"}}
	c := testClient(t, rec.server(t), func(cfg *Config) { cfg.RequestJSONMode = true })

	if _, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "x"}}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	rf, ok := rec.reqs[0]["response_format"].(map[string]any)
	if !ok || rf["type"] != "json_object" {
		t.Fatalf("response_format = %v", rec.reqs[0]["response_format"])
	}
}

func TestChatSurfacesHTTPError(t *testing.T) {
	rec := &recorder{status: http.StatusTooManyRequests}
	c := testClient(t, rec.server(t), nil)

	_, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "x"}})
	if err == nil {
		t.Fatal("no error on 429")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error %q does not name the status", err)
	}
}

func TestChatRespectsContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	c := testClient(t, srv, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Chat(ctx, []Message{{Role: RoleUser, Content: "x"}}); err == nil {
		t.Fatal("no error when the context expired")
	}
}

// --- ChatJSON: the repair turn -------------------------------------------

type guideShape struct {
	About string `json:"about"`
}

func TestChatJSONParsesFencedReply(t *testing.T) {
	rec := &recorder{replies: []string{"```json\n{\"about\":\"desert politics\"}\n```"}}
	c := testClient(t, rec.server(t), nil)

	var out guideShape
	if err := c.ChatJSON(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, &out); err != nil {
		t.Fatalf("ChatJSON: %v", err)
	}
	if out.About != "desert politics" {
		t.Fatalf("About = %q", out.About)
	}
	if len(rec.reqs) != 1 {
		t.Errorf("requests = %d, want 1 — a parseable reply needs no repair", len(rec.reqs))
	}
}

// TestChatJSONRepairsOnce — small models routinely answer with prose on the
// first turn and comply when told exactly what was wrong.
func TestChatJSONRepairsOnce(t *testing.T) {
	rec := &recorder{replies: []string{
		"I'd be happy to help! What book?",
		`{"about":"recovered"}`,
	}}
	c := testClient(t, rec.server(t), nil)

	var out guideShape
	if err := c.ChatJSON(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, &out); err != nil {
		t.Fatalf("ChatJSON: %v", err)
	}
	if out.About != "recovered" {
		t.Fatalf("About = %q", out.About)
	}
	if len(rec.reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(rec.reqs))
	}
	// The repair turn has to carry the failed output, or the model has
	// nothing to correct.
	msgs, _ := rec.reqs[1]["messages"].([]any)
	if len(msgs) < 3 {
		t.Fatalf("repair turn sent %d messages, want the original plus the bad reply plus the correction", len(msgs))
	}
	if !strings.Contains(fmt.Sprint(msgs...), "I'd be happy to help") {
		t.Error("repair turn does not include the unparseable reply")
	}
}

// TestChatJSONGivesUpAfterOneRepair — bounded, so a model stuck in prose
// cannot burn tokens in a loop during a bulk run.
func TestChatJSONGivesUpAfterOneRepair(t *testing.T) {
	rec := &recorder{replies: []string{"still prose", "more prose"}}
	c := testClient(t, rec.server(t), nil)

	var out guideShape
	err := c.ChatJSON(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, &out)
	if err == nil {
		t.Fatal("no error after two unparseable replies")
	}
	if len(rec.reqs) != 2 {
		t.Fatalf("requests = %d, want exactly 2", len(rec.reqs))
	}
}

// TestChatJSONRejectsWrongShape — valid JSON that is not the requested
// object must fail rather than silently writing an empty guide.
func TestChatJSONRejectsWrongShape(t *testing.T) {
	rec := &recorder{replies: []string{`["a","b"]`, `["c"]`}}
	c := testClient(t, rec.server(t), nil)

	var out guideShape
	if err := c.ChatJSON(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, &out); err == nil {
		t.Fatal("a JSON array was accepted as the guide object")
	}
}

// --- config --------------------------------------------------------------

func TestNewRejectsIncompleteConfig(t *testing.T) {
	for name, cfg := range map[string]Config{
		"no base url": {Model: "m"},
		"no model":    {BaseURL: "https://x/v1"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(cfg); err == nil {
				t.Fatal("accepted an incomplete config")
			}
		})
	}
}

// TestNewNormalisesBaseURL — operators paste the URL with and without a
// trailing slash, and "https://host/v1/" must not become "/v1//chat".
func TestNewNormalisesBaseURL(t *testing.T) {
	rec := &recorder{replies: []string{"ok"}}
	srv := rec.server(t)
	c := testClient(t, srv, func(cfg *Config) { cfg.BaseURL = srv.URL + "/" })

	if _, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "x"}}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

// --- auth styles ---------------------------------------------------------

// Azure AI Foundry exposes an OpenAI-compatible surface at /openai/v1 but
// has historically authenticated with an `api-key` header rather than a
// bearer token. Probing a live resource with a bogus key returns the same
// 401 for both, so which one it accepts cannot be discovered without a
// real credential — the style is therefore explicit rather than guessed.
func TestChatUsesAPIKeyHeaderWhenConfigured(t *testing.T) {
	rec := &recorder{replies: []string{"ok"}}
	c := testClient(t, rec.server(t), func(cfg *Config) {
		cfg.APIKey = "azure-secret"
		cfg.AuthStyle = AuthAPIKeyHeader
	})

	if _, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "x"}}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got := rec.headers[0].Get("api-key"); got != "azure-secret" {
		t.Errorf("api-key = %q, want the key", got)
	}
	if got := rec.headers[0].Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want none when using the api-key style", got)
	}
}

func TestChatDefaultsToBearer(t *testing.T) {
	rec := &recorder{replies: []string{"ok"}}
	c := testClient(t, rec.server(t), func(cfg *Config) { cfg.APIKey = "sk-x" })

	if _, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "x"}}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got := rec.headers[0].Get("Authorization"); got != "Bearer sk-x" {
		t.Errorf("Authorization = %q", got)
	}
	if got := rec.headers[0].Get("api-key"); got != "" {
		t.Errorf("api-key = %q, want none by default", got)
	}
}

// TestChatSendsNoAuthHeaderWithoutKey holds for either style — a local
// model needs no credential at all.
func TestChatSendsNoAuthHeaderWithoutKey(t *testing.T) {
	rec := &recorder{replies: []string{"ok"}}
	c := testClient(t, rec.server(t), func(cfg *Config) { cfg.AuthStyle = AuthAPIKeyHeader })

	if _, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "x"}}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if rec.headers[0].Get("api-key") != "" || rec.headers[0].Get("Authorization") != "" {
		t.Error("sent an auth header with no key configured")
	}
}
