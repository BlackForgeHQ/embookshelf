// SPDX-License-Identifier: AGPL-3.0-or-later

// Package llm talks to an OpenAI-compatible chat-completions endpoint.
//
// One adapter covers OpenAI, OpenRouter, Ollama, LM Studio and vLLM,
// because they share a protocol. That is deliberate (ADR-0024 §3): a
// self-hosted operator who does not want their library read by a third
// party points BaseURL at a local server and the text never leaves the
// machine.
package llm

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

// Roles in a chat exchange.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message is one turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Config describes the endpoint. APIKey is optional — a local server
// usually wants none, and sending an empty bearer token makes some of
// them reject the request outright.
type Config struct {
	BaseURL string
	Model   string
	APIKey  string
	Timeout time.Duration
	// RequestJSONMode sends response_format: json_object. Off by default:
	// support is uneven across OpenAI-compatible servers, so the prompt
	// carries the JSON contract and this is a bonus, never a dependency.
	RequestJSONMode bool
}

// DefaultTimeout is generous because a full-text guide can take minutes
// on a local model.
const DefaultTimeout = 5 * time.Minute

// Client is a chat-completions caller. Safe for concurrent use.
type Client struct {
	cfg  Config
	http *http.Client
}

func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("llm: base URL is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("llm: model is required")
	}
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	// No retry decorator here, unlike the metadata providers: one
	// generation can run for minutes, so a blind retry multiplies both
	// the wait and the bill. The queue job owns whether to try again.
	return &Client{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}, nil
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

// Chat sends the messages and returns the first choice's content.
func (c *Client) Chat(ctx context.Context, msgs []Message) (string, error) {
	payload := chatRequest{Model: c.cfg.Model, Messages: msgs}
	if c.cfg.RequestJSONMode {
		payload.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("llm: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(c.cfg.APIKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// Cap the echoed body: an HTML error page from a misconfigured
		// proxy would otherwise land whole in the log.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("llm: %d %s: %s",
			resp.StatusCode, http.StatusText(resp.StatusCode), strings.TrimSpace(string(snippet)))
	}

	var parsed chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("llm: decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("llm: response carried no choices")
	}
	return parsed.Choices[0].Message.Content, nil
}

// ErrUnparseable is returned when the model would not produce the
// requested JSON object, even after one correction.
var ErrUnparseable = errors.New("llm: model did not return the requested JSON")

// ChatJSON sends the messages and unmarshals the reply into out.
//
// Models wrap JSON in code fences, prefix it with a sentence, or answer in
// prose entirely, and which happens varies by model and by run. So the
// reply is parsed tolerantly, and one — exactly one — repair turn is sent
// quoting what came back and what was wrong with it. Bounding it at one
// matters during a bulk run: a model stuck in prose must not loop.
func (c *Client) ChatJSON(ctx context.Context, msgs []Message, out any) error {
	reply, err := c.Chat(ctx, msgs)
	if err != nil {
		return err
	}
	if err := unmarshalReply(reply, out); err == nil {
		return nil
	}

	repair := append(append([]Message{}, msgs...),
		Message{Role: RoleAssistant, Content: reply},
		Message{Role: RoleUser, Content: repairInstruction},
	)
	reply, err = c.Chat(ctx, repair)
	if err != nil {
		return err
	}
	if err := unmarshalReply(reply, out); err != nil {
		return fmt.Errorf("%w: %v", ErrUnparseable, err)
	}
	return nil
}

const repairInstruction = "That was not valid JSON. Reply with the JSON object only — " +
	"no prose, no explanation, no code fences."

func unmarshalReply(reply string, out any) error {
	raw, ok := extractJSONObject(reply)
	if !ok {
		return errors.New("no JSON object in reply")
	}
	return json.Unmarshal([]byte(raw), out)
}

// extractJSONObject finds the outermost {...} in a model reply, ignoring
// code fences and any prose around it. Brace counting is string-aware, so
// a "}" inside a value does not end the object early.
func extractJSONObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		switch {
		case escaped:
			escaped = false
		case ch == '\\' && inString:
			escaped = true
		case ch == '"':
			inString = !inString
		case inString:
			// Braces inside a string value are data, not structure.
		case ch == '{':
			depth++
		case ch == '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}
