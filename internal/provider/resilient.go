package provider

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	gobreaker "github.com/sony/gobreaker/v2"
	"golang.org/x/time/rate"
)

// roundTripperFunc adapts a plain function into an http.RoundTripper.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// NewResilientClient builds an *http.Client with three layers:
//
//  1. Token-bucket rate limiter (golang.org/x/time/rate) — prevents tripping upstream quotas
//  2. Circuit breaker (sony/gobreaker/v2) — stops hammering a dead provider; auto-recovers
//  3. Retryable HTTP (hashicorp/go-retryablehttp) — exponential backoff on transient 5xx/network errors
//
// Composition order: rate-limit → circuit-break → retry-transport.
// Retries happen inside the retry layer, so each logical request consumes
// one rate-limit token regardless of how many attempts it takes.
func NewResilientClient(name string, rps float64, burst int) *http.Client {
	// --- Layer 3 (innermost): retryable HTTP transport ---
	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = 3
	retryClient.RetryWaitMin = 300 * time.Millisecond
	retryClient.RetryWaitMax = 5 * time.Second
	retryClient.Logger = nil // suppress log spam

	baseTransport := retryClient.StandardClient().Transport

	// --- Layer 2: circuit breaker ---
	cb := gobreaker.NewCircuitBreaker[*http.Response](gobreaker.Settings{
		Name:    name,
		Timeout: 30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.Requests >= 5 &&
				float64(counts.TotalFailures)/float64(counts.Requests) >= 0.6
		},
	})

	breakerTransport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return cb.Execute(func() (*http.Response, error) {
			resp, err := baseTransport.RoundTrip(req)
			if err != nil {
				return nil, err
			}
			// Treat 429 and 5xx as breaker failures.
			if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
				resp.Body.Close()
				return nil, fmt.Errorf("%s: upstream returned %d", name, resp.StatusCode)
			}
			return resp, nil
		})
	})

	// --- Layer 1 (outermost): rate limiter ---
	limiter := rate.NewLimiter(rate.Limit(rps), burst)

	rateLimitedTransport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		ctx := req.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		if err := limiter.Wait(ctx); err != nil {
			return nil, err
		}
		return breakerTransport.RoundTrip(req)
	})

	return &http.Client{
		Transport: rateLimitedTransport,
		Timeout:   15 * time.Second,
	}
}
