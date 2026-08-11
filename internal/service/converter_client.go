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

// ConvertResult is a finished conversion: the artifact staged in a local
// temp file (the caller owns removing it — normally by placing it), and
// the sidecar's version for the provenance row.
type ConvertResult struct {
	Path    string
	Version string
}

// ConvertRejectedError is the sidecar refusing the document itself.
// Permanent: the same bytes will be refused again, so the job must not
// retry (ADR-0033 §5).
type ConvertRejectedError struct {
	Status  int
	Message string
}

func (e *ConvertRejectedError) Error() string { return e.Message }

// ConverterClient speaks the sidecar's whole wire contract (ADR-0033
// §3, ADR-0034 §3): POST /convert and /render/epub with staged-file
// answers, GET /healthz for the admin card's probe, version in a
// header, errors as JSON {"error": "..."}. The adapter owns the
// contract end to end — no caller builds converter HTTP itself (#300).
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
// a temp file. 415 (no detectable/supported format) and 422
// (structurally unusable) reject the document itself.
func (c *ConverterClient) Convert(ctx context.Context, baseURL string, body io.Reader) (ConvertResult, error) {
	return c.post(ctx, postSpec{
		op:          "convert",
		url:         baseURL + "/convert",
		body:        body,
		contentType: "application/octet-stream",
		rejects:     []int{http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity},
		stage:       "markdown",
		tempPattern: "embookshelf-markdown-*.md",
	})
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
// in a temp file. Error mapping mirrors Convert: 400/422 are the
// document's fault and permanent, everything else is the wire's and
// retried.
func (c *ConverterClient) RenderEPUB(ctx context.Context, baseURL string, req EpubRenderRequest) (ConvertResult, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return ConvertResult{}, fmt.Errorf("encode render request: %w", err)
	}
	return c.post(ctx, postSpec{
		op:          "render",
		url:         baseURL + "/render/epub",
		body:        bytes.NewReader(payload),
		contentType: "application/json",
		rejects:     []int{http.StatusBadRequest, http.StatusUnprocessableEntity},
		stage:       "epub",
		tempPattern: "embookshelf-epub-*.epub",
	})
}

// Health probes {baseURL}/healthz. A reachable sidecar answers its
// version; anything else — dead listener, non-200 — is an error carrying
// why, verbatim, for the admin card (ADR-0033's loud-failure rule). The
// caller bounds the probe with its own context deadline.
func (c *ConverterClient) Health(ctx context.Context, baseURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
	if err != nil {
		return "", fmt.Errorf("build healthz request: %w", err)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("healthz answered %s", resp.Status)
	}
	return resp.Header.Get("X-Converter-Version"), nil
}

// postSpec is what genuinely differs between the two POST endpoints:
// where the request goes and how it is encoded, which statuses mean
// "the document is refused", and how the staged answer is named.
type postSpec struct {
	op          string
	url         string
	body        io.Reader
	contentType string
	rejects     []int
	stage       string
	tempPattern string
}

// post owns the exchange both POST endpoints share: the timeout wrap,
// client defaulting, the rejected-vs-transient status mapping, and
// staging the answer in a temp file. One implementation so the two
// error mappings cannot drift independently (#300).
func (c *ConverterClient) post(ctx context.Context, spec postSpec) (ConvertResult, error) {
	ctx, cancel := context.WithTimeout(ctx, convertTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spec.url, spec.body)
	if err != nil {
		return ConvertResult{}, fmt.Errorf("build %s request: %w", spec.op, err)
	}
	req.Header.Set("Content-Type", spec.contentType)

	resp, err := c.client().Do(req)
	if err != nil {
		return ConvertResult{}, fmt.Errorf("converter: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusOK:
		// fall through below
	case statusIn(resp.StatusCode, spec.rejects):
		return ConvertResult{}, &ConvertRejectedError{
			Status:  resp.StatusCode,
			Message: convertErrorMessage(resp),
		}
	default:
		// 5xx and anything unexpected: transient from our side of the
		// wire — the queue retries.
		return ConvertResult{}, fmt.Errorf("converter answered %s: %s", resp.Status, convertErrorMessage(resp))
	}

	f, err := os.CreateTemp("", spec.tempPattern)
	if err != nil {
		return ConvertResult{}, fmt.Errorf("stage %s: %w", spec.stage, err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return ConvertResult{}, fmt.Errorf("read converter response: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return ConvertResult{}, fmt.Errorf("stage %s: %w", spec.stage, err)
	}
	return ConvertResult{
		Path:    f.Name(),
		Version: resp.Header.Get("X-Converter-Version"),
	}, nil
}

func (c *ConverterClient) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func statusIn(status int, set []int) bool {
	for _, s := range set {
		if s == status {
			return true
		}
	}
	return false
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
