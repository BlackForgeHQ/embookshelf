// SPDX-License-Identifier: AGPL-3.0-or-later

package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestResilientTransport_RateLimits(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// 2 rps, burst 1 — over 800 ms at most ~2-3 requests should get through.
	client := NewResilientClient("rate-test", 2, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()

	for i := 0; i < 5; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		if err != nil {
			break
		}
		resp, err := client.Do(req)
		if err != nil {
			break // context deadline or rate limit wait exceeded
		}
		resp.Body.Close()
	}

	got := hits.Load()
	if got > 3 {
		t.Errorf("expected at most 3 requests through rate limiter in 800ms at 2 rps/burst 1, got %d", got)
	}
	if got == 0 {
		t.Error("expected at least 1 request to get through, got 0")
	}
}

func TestResilientTransport_CircuitBreaker(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	// High rate limit so the breaker is the bottleneck, not the limiter.
	client := NewResilientClient("breaker-test", 1000, 100)

	var lastErr error
	for i := 0; i < 20; i++ {
		req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatalf("creating request: %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()
	}

	// With retries (max 3 retries = 4 attempts each), 20 logical requests
	// would produce up to 80 server hits if nothing intervened. The circuit
	// breaker should open well before that.
	serverHits := hits.Load()
	if serverHits >= 80 {
		t.Errorf("circuit breaker did not open: server saw %d hits (expected far fewer than 80)", serverHits)
	}

	if lastErr == nil {
		t.Error("expected last error to be non-nil after circuit opens")
	}
}

func TestResilientTransport_RetriesTransientErrors(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// High rate limit so retries aren't throttled.
	client := NewResilientClient("retry-test", 1000, 100)

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("creating request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected successful response after retries, got error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	serverHits := hits.Load()
	if serverHits < 3 {
		t.Errorf("expected at least 3 server hits (2 failures + 1 success), got %d", serverHits)
	}
}
