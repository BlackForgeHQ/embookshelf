# Resilient Metadata Enrichment Pipeline

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Harden the provider HTTP layer with per-provider rate limiting, circuit breaking, and automatic retries, and improve match scoring with Unicode normalization — so the enrichment pipeline handles flaky upstream sources gracefully and scores international titles correctly.

**Architecture:** Each provider gets its own `*http.Client` composed from three layers: `golang.org/x/time/rate` (token-bucket, prevents tripping upstream quotas), `sony/gobreaker/v2` (circuit breaker, stops hammering a dead provider), and `hashicorp/go-retryablehttp` (automatic retry with exponential backoff on transient failures). Rate limits are declared per-provider in the catalog. The manual cooldown map in `EnrichmentService` is removed since the circuit breaker now handles the "skip a broken provider" concern with better semantics (half-open recovery instead of a fixed 60s wall). Title/author scoring gains Unicode NFC normalization so diacritics, ligatures, and precomposed vs. decomposed forms match consistently across providers.

**Tech Stack:** Go 1.25, `hashicorp/go-retryablehttp`, `sony/gobreaker/v2`, `golang.org/x/time/rate`, `golang.org/x/text/unicode/norm` (already an indirect dep)

**Scope note:** This plan covers infrastructure improvements to the *existing* pipeline. Adding new providers (Wikidata, WorldCat, LoC SRU, ISBNdb) and cover pipeline improvements (multi-size, WebP, SHA-256 dedup) are separate plans that build on this foundation. See `docs/metadata-enrichment-guide.md` for the full library survey.

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Create | `internal/provider/resilient.go` | Per-provider HTTP transport: rate limit + circuit breaker + retryable |
| Create | `internal/provider/resilient_test.go` | Tests for resilient transport (rate limit, breaker trip, retry) |
| Create | `internal/provider/score_test.go` | Tests for scoring including Unicode normalization |
| Modify | `internal/provider/catalog.go` | Add `RateLimit` config to `Info` struct |
| Modify | `internal/provider/provider.go` | Update `Build()` to inject per-provider resilient clients; remove `defaultHTTPClient` |
| Modify | `internal/provider/score.go` | Use NFC normalization in `scoreMatch` and `wordSet` |
| Modify | `internal/service/enrichment.go` | Remove manual cooldown map; simplify `Search`/`SearchStream`/`LookupByISBN` |
| Modify | `go.mod` | Add `go-retryablehttp`, `gobreaker/v2`, `x/time` |

---

### Task 1: Add Dependencies

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Install go-retryablehttp**

```bash
go get github.com/hashicorp/go-retryablehttp
```

- [ ] **Step 2: Install gobreaker v2**

```bash
go get github.com/sony/gobreaker/v2
```

- [ ] **Step 3: Install x/time (rate limiter)**

```bash
go get golang.org/x/time
```

- [ ] **Step 4: Promote x/text to direct dependency**

`golang.org/x/text` is already an indirect dep (via otel). Promote it so `unicode/norm` is a first-class import:

```bash
go get golang.org/x/text
```

- [ ] **Step 5: Tidy and verify**

Run: `go mod tidy && go build ./...`
Expected: clean build, no errors.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum
git commit -m "chore(deps): add go-retryablehttp, gobreaker/v2, x/time for resilient enrichment"
```

---

### Task 2: Create Resilient HTTP Transport

**Files:**
- Create: `internal/provider/resilient.go`
- Create: `internal/provider/resilient_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/provider/resilient_test.go`:

```go
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
	var count atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// 2 rps, burst 1 — third request within 1s must block.
	client := NewResilientClient("test", 2, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()

	for i := 0; i < 5; i++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
		_, err := client.Do(req)
		if err != nil {
			// Context deadline exceeded means the rate limiter held us back — expected.
			break
		}
	}
	// With 2 rps and burst 1, we should complete at most ~2 requests in 800ms.
	got := count.Load()
	if got > 3 {
		t.Errorf("expected at most 3 requests in 800ms at 2 rps, got %d", got)
	}
}

func TestResilientTransport_CircuitBreaker(t *testing.T) {
	var count atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewResilientClient("test-cb", 100, 100) // high rate limit so breaker is the constraint
	ctx := context.Background()

	// Send enough requests to trip the breaker (5 requests, 60% failure = trips).
	var lastErr error
	for i := 0; i < 20; i++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
		_, err := client.Do(req)
		if err != nil {
			lastErr = err
		}
	}

	// Breaker should have opened — later requests never reach the server.
	// With retries (3 per attempt), initial requests generate more server hits,
	// but the breaker should cut them off well before 20*4=80.
	if lastErr == nil {
		t.Error("expected circuit breaker error after sustained 500s")
	}
	if count.Load() >= 60 {
		t.Errorf("circuit breaker should have opened; server saw %d requests", count.Load())
	}
}

func TestResilientTransport_RetriesTransientErrors(t *testing.T) {
	var count atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := count.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	client := NewResilientClient("test-retry", 100, 100)
	req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected success after retries, got err: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 after retries, got %d", resp.StatusCode)
	}
	if count.Load() < 3 {
		t.Errorf("expected at least 3 attempts (2 failures + 1 success), got %d", count.Load())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/ -run TestResilient -v`
Expected: FAIL — `NewResilientClient` undefined.

- [ ] **Step 3: Implement the resilient transport**

Create `internal/provider/resilient.go`:

```go
package provider

import (
	"fmt"
	"net/http"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/sony/gobreaker/v2"
	"golang.org/x/time/rate"
)

// NewResilientClient builds an *http.Client with three layers:
//
//  1. Token-bucket rate limiter (prevents tripping upstream quotas)
//  2. Circuit breaker (stops hammering a dead provider; auto-recovers)
//  3. Retryable HTTP (exponential backoff on transient 5xx / network errors)
//
// The composition order is: rate-limit → circuit-break → retry-transport.
// Retries happen inside the retry layer, so each logical request consumes
// one rate-limit token regardless of how many attempts it takes.
func NewResilientClient(name string, rps float64, burst int) *http.Client {
	// Innermost layer: retryable HTTP with exponential backoff.
	retry := retryablehttp.NewClient()
	retry.RetryMax = 3
	retry.RetryWaitMin = 300 * time.Millisecond
	retry.RetryWaitMax = 5 * time.Second
	retry.Logger = nil // suppress default log spam; we log via slog at call sites
	base := retry.StandardClient().Transport

	// Middle layer: circuit breaker. Opens after 60% failure rate over
	// the last 5+ requests, stays open for 30 s, then half-opens to
	// probe recovery.
	cb := gobreaker.NewCircuitBreaker[*http.Response](gobreaker.Settings{
		Name:    name,
		Timeout: 30 * time.Second,
		ReadyToTrip: func(c gobreaker.Counts) bool {
			return c.Requests >= 5 &&
				float64(c.TotalFailures)/float64(c.Requests) >= 0.6
		},
	})

	// Outermost layer: token-bucket rate limiter.
	limiter := rate.NewLimiter(rate.Limit(rps), burst)

	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			// 1. Wait for a rate-limit token.
			if err := limiter.Wait(req.Context()); err != nil {
				return nil, err
			}
			// 2. Execute through the circuit breaker.
			return cb.Execute(func() (*http.Response, error) {
				resp, err := base.RoundTrip(req)
				if err != nil {
					return nil, err
				}
				// 3. Signal breaker failure on 429/5xx so the circuit
				//    opens after sustained upstream issues. The response
				//    body is closed here to prevent leaks — the caller
				//    receives a nil response + error, same as a network
				//    failure.
				if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
					_ = resp.Body.Close()
					return nil, fmt.Errorf("%s %d", name, resp.StatusCode)
				}
				return resp, nil
			})
		}),
	}
}

// roundTripperFunc adapts a plain function into an http.RoundTripper.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/provider/ -run TestResilient -v -count=1`
Expected: PASS (all 3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/provider/resilient.go internal/provider/resilient_test.go
git commit -m "feat(provider): add resilient HTTP client with rate limit, circuit breaker, retries"
```

---

### Task 3: Add Per-Provider Rate Limits to Catalog

**Files:**
- Modify: `internal/provider/catalog.go`

- [ ] **Step 1: Add RateLimitConfig to catalog**

In `internal/provider/catalog.go`, add the rate limit struct and update `Info` and `Catalog`:

```go
// RateLimitConfig tunes the per-provider token-bucket rate limiter.
// RPS is the sustained rate; Burst is the maximum tokens available
// for short spikes.
type RateLimitConfig struct {
	RPS   float64
	Burst int
}
```

Add `RateLimit RateLimitConfig` field to `Info`.

Update each catalog entry with provider-appropriate limits:

```go
var Catalog = []Info{
	{ID: SourceGoogleBooks, Name: "Google Books", External: true, DefaultEnabled: true,
		RateLimit: RateLimitConfig{RPS: 1, Burst: 3}},    // ~1000 req/day anonymous
	{ID: SourceOpenLibrary, Name: "Open Library", External: true, DefaultEnabled: true,
		RateLimit: RateLimitConfig{RPS: 2, Burst: 5}},    // generous, undocumented
	{ID: SourceHardcover, Name: "Hardcover", External: true,
		RateLimit: RateLimitConfig{RPS: 1, Burst: 1}},    // 60 rpm documented
	{ID: SourceGoodReads, Name: "Goodreads", External: true,
		RateLimit: RateLimitConfig{RPS: 0.5, Burst: 1}},  // scraping, be polite
	{ID: SourceAmazon, Name: "Amazon", External: true, DefaultEnabled: true,
		RateLimit: RateLimitConfig{RPS: 2, Burst: 5}},    // CDN cover-only
	{ID: SourceDuckDuckGo, Name: "DuckDuckGo", External: true, DefaultEnabled: true,
		RateLimit: RateLimitConfig{RPS: 1, Burst: 2}},    // instant answer API
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: clean build.

- [ ] **Step 3: Commit**

```bash
git add internal/provider/catalog.go
git commit -m "feat(provider): add per-provider rate limit config to catalog"
```

---

### Task 4: Wire Per-Provider Resilient Clients Through Build()

**Files:**
- Modify: `internal/provider/provider.go`
- Modify: `internal/provider/googlebooks.go`
- Modify: `internal/provider/openlibrary.go`
- Modify: `internal/provider/hardcover.go`
- Modify: `internal/provider/goodreads.go`
- Modify: `internal/provider/amazon.go`
- Modify: `internal/provider/duckduckgo.go`

- [ ] **Step 1: Update Build() to inject resilient clients**

In `internal/provider/provider.go`, replace the `Build` function and remove `defaultHTTPClient`:

Remove:
```go
// defaultHTTPClient is shared across providers so connection pools get reused.
// A 10 s timeout keeps one slow provider from stalling the whole fan-out.
var defaultHTTPClient = &http.Client{Timeout: 10 * time.Second}
```

Replace `Build` with:
```go
// Build constructs a provider by name with a per-provider resilient HTTP
// client (rate limit + circuit breaker + retries). Returns nil for unknown
// sources — callers (the bootstrap config in main.go) log + skip those
// rather than failing startup, so a typo doesn't crash the whole server.
func Build(name Source) Provider {
	info, ok := CatalogLookup(string(name))
	if !ok {
		return nil
	}
	client := NewResilientClient(
		string(name),
		info.RateLimit.RPS,
		info.RateLimit.Burst,
	)
	switch name {
	case SourceGoogleBooks:
		return NewGoogleBooks(client)
	case SourceOpenLibrary:
		return NewOpenLibrary(client)
	case SourceAmazon:
		return NewAmazon(client)
	case SourceDuckDuckGo:
		return NewDuckDuckGo(client)
	case SourceHardcover:
		return NewHardcover(client)
	case SourceGoodReads:
		return NewGoodReads(client)
	}
	return nil
}
```

- [ ] **Step 2: Update GoogleBooks constructor**

In `internal/provider/googlebooks.go`, change:
```go
func NewGoogleBooks() *GoogleBooks {
	return &GoogleBooks{Client: defaultHTTPClient, MaxResults: 5}
}
```
To:
```go
func NewGoogleBooks(client *http.Client) *GoogleBooks {
	return &GoogleBooks{Client: client, MaxResults: 5}
}
```

- [ ] **Step 3: Update OpenLibrary constructor**

In `internal/provider/openlibrary.go`, change:
```go
func NewOpenLibrary() *OpenLibrary {
	return &OpenLibrary{Client: defaultHTTPClient, MaxResults: 5}
}
```
To:
```go
func NewOpenLibrary(client *http.Client) *OpenLibrary {
	return &OpenLibrary{Client: client, MaxResults: 5}
}
```

- [ ] **Step 4: Update Hardcover constructor**

In `internal/provider/hardcover.go`, change the constructor to accept and use the client parameter:
```go
func NewHardcover(client *http.Client) *Hardcover {
```
Set `Client: client` instead of `Client: defaultHTTPClient`.

- [ ] **Step 5: Update GoodReads constructor**

In `internal/provider/goodreads.go`, change the constructor to accept and use the client parameter:
```go
func NewGoodReads(client *http.Client) *GoodReads {
```
Set `Client: client` instead of `Client: defaultHTTPClient`.

- [ ] **Step 6: Update Amazon constructor**

In `internal/provider/amazon.go`, change the constructor to accept and use the client parameter:
```go
func NewAmazon(client *http.Client) *Amazon {
```
Set `Client: client` instead of `Client: defaultHTTPClient`.

- [ ] **Step 7: Update DuckDuckGo constructor**

In `internal/provider/duckduckgo.go`, change the constructor to accept and use the client parameter:
```go
func NewDuckDuckGo(client *http.Client) *DuckDuckGo {
```
Set `Client: client` instead of `Client: defaultHTTPClient`.

- [ ] **Step 8: Verify build and existing tests pass**

Run: `go build ./... && go test ./...`
Expected: clean build and all tests pass. The `isbn_test.go` tests should still pass since they don't touch HTTP clients.

- [ ] **Step 9: Commit**

```bash
git add internal/provider/provider.go internal/provider/googlebooks.go \
       internal/provider/openlibrary.go internal/provider/hardcover.go \
       internal/provider/goodreads.go internal/provider/amazon.go \
       internal/provider/duckduckgo.go
git commit -m "feat(provider): inject per-provider resilient HTTP clients via Build()"
```

---

### Task 5: Unicode-Aware Match Scoring

**Files:**
- Modify: `internal/provider/score.go`
- Create: `internal/provider/score_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/provider/score_test.go`:

```go
package provider

import "testing"

func TestScoreMatch_ExactTitle(t *testing.T) {
	q := Query{Title: "The Pragmatic Programmer", Author: "David Thomas"}
	got := scoreMatch(q, "The Pragmatic Programmer", []string{"David Thomas", "Andrew Hunt"})
	if got != 100 {
		t.Errorf("exact title + author match = %d, want 100", got)
	}
}

func TestScoreMatch_ExactTitleNoAuthor(t *testing.T) {
	q := Query{Title: "Dune"}
	got := scoreMatch(q, "Dune", nil)
	if got != 85 {
		t.Errorf("exact title, no author in query = %d, want 85", got)
	}
}

func TestScoreMatch_ISBNAlwaysTop(t *testing.T) {
	q := Query{ISBN: "9780132350884"}
	got := scoreMatch(q, "completely different title", nil)
	if got != 100 {
		t.Errorf("ISBN query = %d, want 100", got)
	}
}

func TestScoreMatch_FuzzyMatch(t *testing.T) {
	q := Query{Title: "Clean Code"}
	got := scoreMatch(q, "Clean Code: A Handbook", nil)
	if got < 50 {
		t.Errorf("contains match = %d, want >= 50", got)
	}
}

func TestScoreMatch_UnicodePrecomposed(t *testing.T) {
	// "u\u0308" (u + combining diaeresis) vs "ü" (precomposed).
	// After NFC normalization both should be identical.
	q := Query{Title: "Gu\u0308nter Grass"}
	got := scoreMatch(q, "G\u00fcnter Grass", nil) // ü precomposed
	if got < 85 {
		t.Errorf("NFC-equivalent titles = %d, want >= 85 (exact after normalization)", got)
	}
}

func TestScoreMatch_UnicodeDiacritics(t *testing.T) {
	// French title with accents — should match exactly after NFC.
	q := Query{Title: "Les Misérables"}
	got := scoreMatch(q, "Les Misérables", nil)
	if got != 85 {
		t.Errorf("identical Unicode title = %d, want 85", got)
	}
}

func TestScoreMatch_CaseFoldUnicode(t *testing.T) {
	// Turkish dotless-i vs ASCII i — strings.ToLower handles this correctly
	// but tests confirm we don't regress.
	q := Query{Title: "istanbul"}
	got := scoreMatch(q, "Istanbul", nil)
	if got < 85 {
		t.Errorf("case-folded title = %d, want >= 85", got)
	}
}

func TestScoreMatch_TokenOverlap(t *testing.T) {
	q := Query{Title: "Programming in Go", Author: "Mark Summerfield"}
	got := scoreMatch(q, "Go Programming Language", []string{"Alan Donovan"})
	if got < 40 {
		t.Errorf("token overlap = %d, want >= 40", got)
	}
}

func TestScoreMatch_NoMatch(t *testing.T) {
	q := Query{Title: "Cooking with Julia"}
	got := scoreMatch(q, "Advanced Calculus", nil)
	if got > 30 {
		t.Errorf("no match = %d, want <= 30", got)
	}
}

func TestFuzzyRatio(t *testing.T) {
	cases := []struct {
		a, b string
		min  float64
	}{
		{"hello", "hello", 1.0},
		{"hello", "helo", 0.7},
		{"", "", 1.0},
		{"abc", "", 0.0},
	}
	for _, tc := range cases {
		got := fuzzyRatio(tc.a, tc.b)
		if got < tc.min {
			t.Errorf("fuzzyRatio(%q, %q) = %.2f, want >= %.2f", tc.a, tc.b, got, tc.min)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they compile and baseline behavior**

Run: `go test ./internal/provider/ -run TestScoreMatch -v`
Expected: Most tests PASS (basic scoring works). `TestScoreMatch_UnicodePrecomposed` will FAIL because current code uses `strings.ToLower` without NFC normalization — the decomposed `"u\u0308"` and precomposed `"\u00fc"` remain different strings.

- [ ] **Step 3: Add Unicode normalization to scoreMatch**

In `internal/provider/score.go`, add the import:
```go
"golang.org/x/text/unicode/norm"
```

Add a normalization helper at the bottom of the file:
```go
// normalizeText applies Unicode NFC normalization and folds to lowercase
// for comparison. NFC ensures precomposed ("ü") and decomposed ("u" +
// combining diaeresis) forms are identical, which matters for titles
// from international providers (German, French, Japanese sources).
func normalizeText(s string) string {
	return strings.ToLower(strings.TrimSpace(norm.NFC.String(s)))
}
```

Replace the first two lines of `scoreMatch`:
```go
qt := strings.ToLower(strings.TrimSpace(q.Title))
qa := strings.ToLower(strings.TrimSpace(q.Author))
mt := strings.ToLower(strings.TrimSpace(title))
```
With:
```go
qt := normalizeText(q.Title)
qa := normalizeText(q.Author)
mt := normalizeText(title)
```

Update `wordSet` to normalize before tokenizing — replace the function body:
```go
func wordSet(s string) map[string]struct{} {
	s = normalizeText(s)
	out := make(map[string]struct{})
	for _, w := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == ',' || r == '.' || r == ':' || r == ';' || r == '-'
	}) {
		if len(w) > 2 { // skip very short tokens — "the", "of", "a"
			out[w] = struct{}{}
		}
	}
	return out
}
```

Also normalize the author comparison in the author-match loop — change:
```go
al := strings.ToLower(strings.TrimSpace(a))
```
To:
```go
al := normalizeText(a)
```

- [ ] **Step 4: Run all tests to verify they pass**

Run: `go test ./internal/provider/ -v -count=1`
Expected: ALL tests pass, including `TestScoreMatch_UnicodePrecomposed`.

- [ ] **Step 5: Verify full build**

Run: `go build ./...`
Expected: clean build.

- [ ] **Step 6: Commit**

```bash
git add internal/provider/score.go internal/provider/score_test.go
git commit -m "feat(provider): add Unicode NFC normalization to match scoring"
```

---

### Task 6: Remove Manual Cooldown from EnrichmentService

**Files:**
- Modify: `internal/service/enrichment.go`

The per-provider circuit breakers now handle the "skip a broken provider" concern with proper half-open recovery semantics. The manual cooldown map (`cooldownMu`, `cooldown`, `cooldownDur`, `providerCoolingDown`, `markCooldown`, `isRateLimited`) can be removed.

- [ ] **Step 1: Remove cooldown fields from struct**

In `internal/service/enrichment.go`, remove from the `EnrichmentService` struct:
```go
	// Per-provider cooldown — when a provider returns 429 we skip it
	// for this long on subsequent Search calls. Prevents the fan-out
	// from re-triggering the rate-limiter we just tripped.
	cooldownMu  sync.Mutex
	cooldown    map[provider.Source]time.Time
	cooldownDur time.Duration
```

- [ ] **Step 2: Remove cooldown initialization from constructor**

In `NewEnrichmentService`, remove:
```go
		cooldown:    make(map[provider.Source]time.Time),
		cooldownDur: enrichCooldownAfter,
```

Remove the `enrichCooldownAfter` constant:
```go
	enrichCooldownAfter = 60 * time.Second
```

- [ ] **Step 3: Remove cooldown helper functions**

Delete `providerCoolingDown`, `markCooldown`, and `isRateLimited` functions (the three functions near the bottom of the file).

- [ ] **Step 4: Simplify Search() — remove cooldown checks**

In `Search()`, remove the cooldown check inside the provider loop:
```go
		if s.providerCoolingDown(p.Name()) {
			// 429 tripped recently — don't re-provoke the rate limiter.
			continue
		}
```

In the error handler inside `g.Go`, remove the cooldown marking:
```go
				if isRateLimited(err) {
					s.markCooldown(p.Name())
				}
```

- [ ] **Step 5: Simplify SearchStream() — remove cooldown checks**

In `SearchStream()`, remove the cooldown filter when building the `runs` list:
```go
		if s.providerCoolingDown(p.Name()) {
			continue
		}
```

In the error handler, remove:
```go
					if isRateLimited(err) {
						s.markCooldown(r.p.Name())
					}
```

- [ ] **Step 6: Simplify LookupByISBN() — remove cooldown check**

Remove:
```go
		if s.providerCoolingDown(p.Name()) {
			continue
		}
```

And in the error handler:
```go
			if isRateLimited(err) {
				s.markCooldown(p.Name())
			}
```

- [ ] **Step 7: Verify build and tests**

Run: `go build ./... && go test ./...`
Expected: clean build and all tests pass.

- [ ] **Step 8: Commit**

```bash
git add internal/service/enrichment.go
git commit -m "refactor(enrichment): remove manual cooldown — circuit breakers handle provider failures"
```

---

### Task 7: Final Verification

- [ ] **Step 1: Run the full CI check**

Run: `make ci-local`
Expected: all checks pass (go-lint, test, ui-lint, ui-typecheck, ui-test, ui-build, build).

- [ ] **Step 2: Verify no leftover references to removed code**

Search for stale references:

```bash
grep -r "defaultHTTPClient\|cooldownDur\|enrichCooldownAfter\|providerCoolingDown\|markCooldown\|isRateLimited" internal/
```

Expected: no matches.

- [ ] **Step 3: Smoke test with dev stack**

Run `make up` and open the enrichment panel for any book. Verify:
- Provider results stream in via SSE
- No errors in the Go server logs
- Rate limiting doesn't visibly slow single-user interaction (the limits are generous enough for interactive use)

- [ ] **Step 4: Commit any fixups**

If linting or the smoke test surfaced issues, commit fixes.
