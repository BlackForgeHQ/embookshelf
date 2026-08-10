// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// ConvertResult is a finished conversion: the markdown staged in a local
// temp file (the caller owns removing it — normally by placing it), and
// the sidecar's version for the provenance row.
type ConvertResult struct {
	Path    string
	Version string
}

// ConvertRejectedError is the sidecar refusing the document itself —
// 415 (no detectable/supported format) or 422 (structurally unusable).
// Permanent: the same bytes will be refused again, so the job must not
// retry (ADR-0033 §5).
type ConvertRejectedError struct {
	Status  int
	Message string
}

func (e *ConvertRejectedError) Error() string { return e.Message }

// ConverterClient speaks the sidecar's wire contract (ADR-0033 §3):
// POST /convert, raw bytes in, raw markdown out, version in a header,
// errors as JSON {"error": "..."}.
type ConverterClient struct {
	// HTTPClient defaults to a client with no overall timeout — a large
	// book on a busy sidecar can legitimately take a while, and the job
	// context carries the cancel.
	HTTPClient *http.Client
}

// convertTimeout bounds one conversion. anydoc converts in milliseconds;
// a minute of headroom covers a queue of large books ahead of us without
// letting a hung sidecar pin a worker forever.
const convertTimeout = 60 * time.Second

// Convert streams body to the sidecar and stages the markdown answer in
// a temp file.
func (c *ConverterClient) Convert(ctx context.Context, baseURL string, body io.Reader) (ConvertResult, error) {
	ctx, cancel := context.WithTimeout(ctx, convertTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/convert", body)
	if err != nil {
		return ConvertResult{}, fmt.Errorf("build convert request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return ConvertResult{}, fmt.Errorf("converter: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		// fall through below
	case http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
		return ConvertResult{}, &ConvertRejectedError{
			Status:  resp.StatusCode,
			Message: convertErrorMessage(resp),
		}
	default:
		// 5xx and anything unexpected: transient from our side of the
		// wire — the queue retries.
		return ConvertResult{}, fmt.Errorf("converter answered %s: %s", resp.Status, convertErrorMessage(resp))
	}

	f, err := os.CreateTemp("", "embookshelf-markdown-*.md")
	if err != nil {
		return ConvertResult{}, fmt.Errorf("stage markdown: %w", err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return ConvertResult{}, fmt.Errorf("read converter response: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return ConvertResult{}, fmt.Errorf("stage markdown: %w", err)
	}
	return ConvertResult{
		Path:    f.Name(),
		Version: resp.Header.Get("X-Converter-Version"),
	}, nil
}

// convertErrorMessage extracts the sidecar's {"error": "..."} verbatim,
// falling back to the raw body — the loud-failure rule wants the real
// string, not a summary of it.
func convertErrorMessage(resp *http.Response) string {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var parsed struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err == nil && parsed.Error != "" {
		return parsed.Error
	}
	return string(raw)
}

// EpubRenderRequest is /render/epub's JSON body (ADR-0034 §3): the
// markdown plus the metadata the OPF needs.
type EpubRenderRequest struct {
	Markdown string `json:"markdown"`
	Title    string `json:"title"`
	Author   string `json:"author"`
	Language string `json:"language"`
}

// RenderEPUB posts markdown to the sidecar and stages the EPUB answer
// in a temp file. Error mapping mirrors Convert: 422 is the document's
// fault and permanent, everything else is the wire's and retried.
func (c *ConverterClient) RenderEPUB(ctx context.Context, baseURL string, req EpubRenderRequest) (ConvertResult, error) {
	ctx, cancel := context.WithTimeout(ctx, convertTimeout)
	defer cancel()

	payload, err := json.Marshal(req)
	if err != nil {
		return ConvertResult{}, fmt.Errorf("encode render request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/render/epub", bytes.NewReader(payload))
	if err != nil {
		return ConvertResult{}, fmt.Errorf("build render request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return ConvertResult{}, fmt.Errorf("converter: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		// fall through below
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return ConvertResult{}, &ConvertRejectedError{
			Status:  resp.StatusCode,
			Message: convertErrorMessage(resp),
		}
	default:
		return ConvertResult{}, fmt.Errorf("converter answered %s: %s", resp.Status, convertErrorMessage(resp))
	}

	f, err := os.CreateTemp("", "embookshelf-epub-*.epub")
	if err != nil {
		return ConvertResult{}, fmt.Errorf("stage epub: %w", err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return ConvertResult{}, fmt.Errorf("read converter response: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return ConvertResult{}, fmt.Errorf("stage epub: %w", err)
	}
	return ConvertResult{
		Path:    f.Name(),
		Version: resp.Header.Get("X-Converter-Version"),
	}, nil
}
