# Metadata provider catalog hard-coded in the binary

The set of metadata providers (Google Books, Open Library, Hardcover, Goodreads, Amazon, DuckDuckGo) is declared as a Go literal in `internal/provider/catalog.go` and constructed by a `switch` in `internal/provider/Build`. Adding a new provider means writing a Go adapter, registering it in both spots, and shipping a release. There is no DB-driven registry, no plugin loader, no config-file-declared provider.

## Status

accepted (2026-05-03)

## Decisions bundled here

### 1. Provider list lives in `provider.Catalog` (Go literal)

Each entry carries `{ID, Name, External, DefaultEnabled, RateLimit}`. The list is the single source of truth — `Build()`, the settings handler DTO, and the per-row seed in `provider_settings` all walk it. Mismatch between Catalog and Build is a startup-time bug, not a runtime fallback.

### 2. Provider construction lives in `provider.Build` (`switch` on ID)

Each `case` calls a hand-written constructor (`NewGoogleBooks`, `NewOpenLibrary`, …) that wires the provider against a shared `NewResilientClient`. No reflection, no factory map. New ID without a `case` returns nil and is logged + skipped at boot — typo doesn't crash the server.

### 3. Default rate limits + DefaultEnabled flags also in code

`Catalog[i].RateLimit` (RPS/Burst) and `DefaultEnabled` ship with the binary. Admins toggle `enabled` and edit `config` per provider via the Settings UI; rate limits are **not** admin-tunable today. Tightening or loosening a provider's RPS requires a release.

## Considered options

### Rejected: DB-stored generic provider rows

A `providers` table with `{id, name, kind, base_url, auth_template, response_jsonpath}` plus a generic HTTP+JSON adapter would let a self-hosted operator add a private corporate catalog without rebuilding. Closest analog: Komga's "OpenLibrary-style" plugin shape.

Rejected because: every provider we ship today has provider-specific quirks that no generic template handles cleanly — Goodreads is HTML-scrape, Hardcover is GraphQL, Amazon needs cookie + region-specific TLD, DuckDuckGo composes Wikipedia + DDG instant answers, OpenLibrary's polymorphic JSON requires hand-written struct shaping. The generic adapter would either be useless (covers only the trivial REST cases) or a turing-complete config language we'd then have to debug. The realistic provider count is also small (~10) and slow-moving — release cadence is not the bottleneck.

### Rejected: Go plugins (`.so` drop-ins)

ABI fragility, Go-version coupling, and zero adoption industry-wide. Not entertained.

### Rejected: External adapter via subprocess + JSON-RPC

LSP-style. Adds a runtime coupling (subprocess lifecycle, IPC schema, error propagation) for benefits no real user has asked for.

## Why this is worth recording

A reader scanning `Catalog` sees a hard-coded list and naturally wonders whether it should be a `providers` table. The answer is "yes, that would be more flexible, and we deliberately didn't" — recording the rejection stops the next engineer from spending a sprint building a registry that solves a problem we don't have. The decision is **additive-deepening reversible** (a future registry can sit beside the in-binary list, each provider implemented either way) but the *surprise* is what needs the ADR, not the irreversibility.

## Companion artifacts

- `internal/provider/catalog.go` — the literal.
- `internal/provider/provider.go` — `Build` switch + `Configurable`/`SchemaProvider` interfaces.
- `docs/architecture.md` — provider-layer overview.
- `docs/research/metadata-go-libraries.md` — landscape survey informing which adapters we picked.
