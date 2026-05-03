# Prospective metadata exposed as an SSE stream alongside a batch endpoint

The "fetch metadata candidates for this book" workflow ships **two** HTTP endpoints over the same provider fan-out: `GET /api/books/:id/enrich` returns batch JSON, `GET /api/books/:id/enrich/stream` returns Server-Sent Events. The stream is the UI default; batch is the headless / legacy fallback. Each per-provider goroutine pushes results into a buffered channel, and a client disconnect cancels the request context, which propagates through `errgroup` → `Provider.Search` → `http.NewRequestWithContext` → transport — in-flight upstream HTTP calls abort, freeing rate-limit tokens and breaker counts.

## Status

accepted (2026-05-03)

## Decisions bundled here

### 1. SSE for the interactive path

Frame schema:

```
event: match            data: <enrichMatchDTO JSON>
event: provider-error   data: {"provider":"...","error":"..."}
event: done             data: {"providers":["...","..."]}
```

Each provider's matches arrive as soon as that provider responds, instead of blocking on the slowest one. Goodreads HTML-scrape can take 4–8 s; Open Library JSON usually returns under 500 ms. Streaming means the UI renders the fast hits in the first second and progressively fills the rest.

### 2. Batch JSON kept in parallel

`GET /api/books/:id/enrich` exists for callers that can't `EventSource`: `curl`, future scripted bulk-imports, OPDS-side enrichment harvesters, and any non-browser client. It's a thin wrapper over the same `EnrichmentService.Search` (cached) — and is what `LookupByISBN` and `AutoEnrich` use internally. Removing it would force every consumer to speak SSE for no benefit.

### 3. Cancellation is load-bearing

`c.Request.Context()` from Gin → `EnrichmentService.SearchStream(ctx, q)` → `errgroup.WithContext(ctx)` → goroutine `Provider.Search(gctx, q)` → adapter calls `http.NewRequestWithContext(gctx, …)`. Closing the browser tab walks all the way down: net/http's transport sees ctx cancel, mid-flight TCP read aborts, retryable-http stops retrying, breaker counts the abort as a non-failure. The stream goroutine then sees the channel writer's `<-gctx.Done()` and exits. No zombie HTTP traffic to upstreams after the user walks away.

### 4. Per-provider errors surface differently per endpoint

- Stream emits `event: provider-error` per failed provider; UI badges that provider red without blocking the rest of the stream.
- Batch returns 200 with merged matches + `QueriedProviders`; per-provider errors are **not** in the response body — they live only in the health table (last_error_at, last_error per row).

This asymmetry is deliberate. Batch is consumed by headless paths that don't render errors; stream is consumed by the editor UI that does. See ADR-0013 for the underlying graceful-degrade policy.

## Considered options

### Rejected: WebSocket

Bidirectional channel, browser-native, but enrichment is unidirectional (server → client only). WebSocket adds a handshake state machine and message-framing concerns for no payoff. SSE rides plain HTTP and reuses the existing auth + ctx + middleware stack.

### Rejected: chunked JSON / NDJSON

Works on the wire but no native browser parser — UI would have to ship a custom reader. SSE has `EventSource` in every browser since 2010, including auto-reconnect.

### Rejected: batch-only

Wait for the slowest provider before returning anything. Goodreads alone (HTML scrape, often 5–8 s with retries) makes the editor feel broken. UX regression compared to streaming.

### Considered: long-poll

One round-trip per match. More server bookkeeping, no benefit over SSE on a single-binary deployment.

## Companion artifacts

- `internal/handler/enrich.go` — `EnrichSearch` (batch) + `EnrichStream` (SSE) handlers.
- `internal/service/enrichment.go` — `Search` and `SearchStream` fan-out logic.
- `internal/provider/provider.go` — `Provider.Search(ctx, Query)` interface contract that ctx is honored.
- `docs/architecture.md` — Streaming metadata enrichment section.
