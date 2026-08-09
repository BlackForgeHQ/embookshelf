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
