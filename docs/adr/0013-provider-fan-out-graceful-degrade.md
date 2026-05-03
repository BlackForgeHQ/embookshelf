# Provider fan-out swallows per-provider errors; siblings continue

`EnrichmentService.Search` and `SearchStream` fan out queries across enabled providers using `golang.org/x/sync/errgroup`. A provider that errors does **not** abort the fan-out: the goroutine logs via `slog.Warn`, records the error in `provider_settings.last_error*` via a detached goroutine, and **returns nil** so `errgroup` does not cancel siblings. `_ = g.Wait()` discards the (always-nil) return value. The errgroup is used purely for context propagation, not for error short-circuit.

## Status

accepted (2026-05-03)

## Decisions bundled here

### 1. errgroup is for ctx, not for errors

This looks like a bug to a reader who knows `errgroup`'s usual semantics ("first non-nil error cancels siblings, `Wait` returns it"). It is deliberate. We want:

- **ctx propagation** — when one goroutine sees `gctx.Done()` (client disconnect, parent cancel, panic in another goroutine via recovery), all siblings stop. That is what `errgroup.WithContext` gives us.
- **no error short-circuit** — Goodreads scrape returning 503 should not cancel the in-flight Hardcover GraphQL request that is about to return a perfect match.

The right shape would be a "ctxgroup" stdlib primitive that exposes `.Go(func() error)` without short-circuit semantics. We don't have that, so we use `errgroup` and discipline every goroutine to return `nil` on per-provider failure.

**Don't "fix" the `_ = g.Wait()` line.** A reader thinking they've spotted a swallowed error and propagating it will break the whole behavior — one bad provider will start killing the entire fan-out.

### 2. Per-provider errors surface differently per endpoint

| Endpoint | Per-provider error visible to caller? | How |
|---|---|---|
| Stream (`SearchStream` → `EnrichStream`) | Yes | `event: provider-error` SSE frame with provider id + message |
| Batch (`Search` → `EnrichSearch`) | No | Caller gets 200 + merged matches + `QueriedProviders` only |
| Health table | Always | `provider_settings.last_error_at` + `last_error` per provider; rendered in admin Settings |

This asymmetry is the right shape, not an oversight. Stream is consumed by an interactive editor where users care which providers failed and want to retry one (red badge → "retry Hardcover"). Batch is consumed by `LookupByISBN` and `AutoEnrich` — headless paths that only care about whether *any* match was returned. Surfacing per-provider errors to those callers would force them to invent failure-handling for something they aren't equipped to act on. The health table is the universal ground truth for "is this provider working" debugging, regardless of which path triggered it.

### 3. Health telemetry is fire-and-forget, detached from request lifecycle

`recordProviderSuccess` and `recordProviderError` spawn a goroutine with a fresh `context.WithTimeout(context.Background(), 3*time.Second)` — **not** the request ctx. Reasons:

- A client disconnect mid-fan-out shouldn't lose the health write that says "Hardcover errored."
- A slow DB on the health write shouldn't extend request latency.
- A failed health write shouldn't fail the user's enrichment request.

Trade-off: a process kill mid-flight loses pending health writes. Acceptable — health telemetry is best-effort observability, not audit log.

## Considered options

### Rejected: fail-fast (return on first provider error)

Standard `errgroup` shape. Wrong here: external providers fail constantly (HTML scrapers especially). One Goodreads 503 would kill every enrichment request. Health table would never get the "Hardcover succeeded" signal because Hardcover would be canceled mid-call.

### Rejected: aggregate error in response body

Return matches **and** an error map `{provider: error}` to all callers. Forces every caller to decide whether the partial success is good enough. Stream already does this via `provider-error` frames; batch callers shouldn't have to write that branching code.

### Rejected: synchronous health writes on the request ctx

Simpler — no detached goroutine, no separate timeout — but ties health observability to the user's request lifecycle, which is the wrong coupling.

## Companion artifacts

- `internal/service/enrichment.go` — `Search`, `SearchStream`, `recordProviderSuccess`, `recordProviderError`.
- `internal/handler/enrich.go` — `EnrichSearch` (no per-provider errors in body), `EnrichStream` (`event: provider-error` frame).
- `internal/repo/provider_settings.go` — `last_error_at`, `last_error` columns.
- ADR-0009 — SSE stream cancellation + frame schema.
- ADR-0011 — ISBN chain step swallows errors per the same policy.
