// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
)

func TestConverterClientStagesMarkdown(t *testing.T) {
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/convert" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("X-Converter-Version", "0.1.0")
		_, _ = w.Write([]byte("# Hello\n"))
	}))
	defer sidecar.Close()

	c := &ConverterClient{}
	got, err := c.Convert(context.Background(), sidecar.URL, strings.NewReader("%PDF-"))
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	defer os.Remove(got.Path)

	staged, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatalf("read staged file: %v", err)
	}
	if string(staged) != "# Hello\n" {
		t.Fatalf("staged = %q", staged)
	}
	if got.Version != "0.1.0" {
		t.Fatalf("Version = %q", got.Version)
	}
}

// TestConverterClientReadsThePageRecord — a PDF answer's page headers
// land on the result (ADR-0040); an answer without them, or with a page
// count that does not parse, is "not recorded" rather than an error.
func TestConverterClientReadsThePageRecord(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    model.PageCoverage
	}{
		{"pdf with gaps", map[string]string{
			"X-Converter-Page-Count": "353", "X-Converter-Missing-Pages": "1,10-15",
		}, model.PageCoverage{PageCount: 353, MissingPages: "1,10-15"}},
		{"pdf with nothing missing", map[string]string{
			"X-Converter-Page-Count": "10", "X-Converter-Missing-Pages": "",
		}, model.PageCoverage{PageCount: 10}},
		{"no page record", nil, model.PageCoverage{}},
		{"garbled count", map[string]string{
			"X-Converter-Page-Count": "lots", "X-Converter-Missing-Pages": "2",
		}, model.PageCoverage{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				_, _ = w.Write([]byte("text\n"))
			}))
			defer sidecar.Close()

			got, err := (&ConverterClient{}).Convert(context.Background(), sidecar.URL, strings.NewReader("%PDF-"))
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			defer os.Remove(got.Path)
			if got.Pages != tc.want {
				t.Fatalf("Pages = %+v, want %+v", got.Pages, tc.want)
			}
		})
	}
}

// TestConverterClientRejectionIsPermanentAndVerbatim — 415/422 mean the
// document itself is refused: same bytes, same answer, so the error is
// typed permanent and carries the sidecar's message untouched.
func TestConverterClientRejectionIsPermanentAndVerbatim(t *testing.T) {
	for _, status := range []int{http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity} {
		sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"PDF has no extractable text (Scanned, 1 pages): OCR is required"}`))
		}))

		c := &ConverterClient{}
		_, err := c.Convert(context.Background(), sidecar.URL, strings.NewReader("x"))
		sidecar.Close()

		var rejected *ConvertRejectedError
		if !errors.As(err, &rejected) {
			t.Fatalf("status %d: err = %v, want ConvertRejectedError", status, err)
		}
		if rejected.Status != status {
			t.Fatalf("Status = %d, want %d", rejected.Status, status)
		}
		if rejected.Message != "PDF has no extractable text (Scanned, 1 pages): OCR is required" {
			t.Fatalf("Message = %q, want the sidecar's error verbatim", rejected.Message)
		}
	}
}

// TestConverterClientServerErrorIsTransient — a 500 is the sidecar's bad
// day, not the document's fault: a plain error, so the queue retries.
func TestConverterClientServerErrorIsTransient(t *testing.T) {
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
	}))
	defer sidecar.Close()

	c := &ConverterClient{}
	_, err := c.Convert(context.Background(), sidecar.URL, strings.NewReader("x"))
	if err == nil {
		t.Fatal("want an error")
	}
	var rejected *ConvertRejectedError
	if errors.As(err, &rejected) {
		t.Fatalf("500 typed permanent — the queue would never retry a sidecar restart")
	}
}

// TestConverterClientHealth — the probe is the adapter's, not the
// handler's (#300): reachable answers the version header, a non-200
// or a dead listener is an error carrying why, verbatim.
func TestConverterClientHealth(t *testing.T) {
	t.Run("reachable reports the version", func(t *testing.T) {
		sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/healthz" {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
			w.Header().Set("X-Converter-Version", "0.1.0")
			_, _ = w.Write([]byte("ok"))
		}))
		defer sidecar.Close()

		version, err := (&ConverterClient{}).Health(context.Background(), sidecar.URL)
		if err != nil {
			t.Fatalf("Health: %v", err)
		}
		if version != "0.1.0" {
			t.Fatalf("version = %q", version)
		}
	})

	t.Run("non-200 is an error naming the status", func(t *testing.T) {
		sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer sidecar.Close()

		if _, err := (&ConverterClient{}).Health(context.Background(), sidecar.URL); err == nil ||
			!strings.Contains(err.Error(), "healthz answered") {
			t.Fatalf("err = %v, want a healthz-answered error", err)
		}
	})

	t.Run("dead listener is an error", func(t *testing.T) {
		sidecar := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		sidecar.Close()

		if _, err := (&ConverterClient{}).Health(context.Background(), sidecar.URL); err == nil {
			t.Fatal("err = nil against a dead listener")
		}
	})
}
