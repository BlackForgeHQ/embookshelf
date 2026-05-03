# ISBN lookup walks providers in admin priority order; first non-empty hit wins

`EnrichmentService.LookupByISBN(ctx, isbn)` is **not** a fan-out. It reads the per-row `priority` column from `provider_settings`, sorts providers ASC (ranked first; unranked fall back to catalog order), and walks them serially. The first provider that returns at least one match terminates the chain — within that provider's batch, the highest-Confidence match wins. This is distinct from `Search` (the prospective-metadata path), which fans out across all enabled providers and merges by confidence across the union.

## Status

accepted (2026-05-03)

## Decisions bundled here

### 1. Short-circuit on first non-empty provider

Used by `POST /api/books/metadata/isbn-lookup` (bare-ISBN bulk import) and the bookdrop `AutoEnrich` headless path. Latency is one provider's response, not the slowest's. Upstream quota cost is one provider per book, not N. ISBN is an identity match — when Google Books returns a hit for `978-…`, querying Open Library + Hardcover for the same ISBN almost never produces a *better* answer, only a different one.

### 2. Priority is one global integer per provider, not per-field

`provider_settings.priority` is a single nullable `int`. Same chain serves ISBN lookup *and* (eventually) any other ordered walk we add. BookLore's spec uses per-field chains (`fieldOptions.title.{p1..p4}`, `fieldOptions.author.{p1..p4}`); we deliberately did not adopt that — for self-hosted single-user / small-team installs, one chain is enough and the settings UI stays a single sortable list instead of a matrix.

### 3. Empty result and error are observationally identical from the chain

A provider that returns `(nil, nil)` (no matches) is skipped to the next. A provider that returns `(_, err)` is logged via `slog.Warn`, recorded in the health table via `recordProviderError`, and skipped to the next. Neither aborts the chain. The only signal distinguishing the two cases is `provider_settings.last_error` / `last_error_at` in the health row — surfaced in the admin UI.

### 4. Two-stage selection inside a winning provider

The chain decides **which provider** wins; within that provider's `[]Match` batch, the code iterates and keeps `max(Confidence)`. Easy to misread the loop as "first match of first provider," but a single provider can return multiple ISBN hits (Open Library returns work + edition; Google Books returns multiple printings) and we want the most confident one.

## Considered options

### Rejected: fan-out + merge-by-confidence (the `Search` shape)

Best quality but slowest-provider-bound and N× upstream cost. ISBN lookup runs in two contexts that both want fast and cheap: a user typing an ISBN into a bulk-import form (latency-sensitive) and the bookdrop ingest path (per-book cost-sensitive across thousands of files). Quality from short-circuit + admin priority is empirically good enough.

### Rejected: fan-out + first-to-respond

Fastest possible, but the winner is race-dependent — the same ISBN can return Hardcover one day and Google Books the next based on transient latency. Reproducibility matters when an admin is debugging "why did this book come in with the wrong publisher."

### Rejected: per-field priority chains (BookLore shape)

`fieldOptions.title.{p1..p4}`, `fieldOptions.author.{p1..p4}`, etc. Strictly more expressive but makes the settings UI a matrix and bloats the JSON. No request from any user; the Search path's confidence-based merge already solves the "I want the best title and the best description" case.

## Companion artifacts

- `internal/service/enrichment.go` — `LookupByISBN`.
- `internal/repo/provider_settings.go` — `priority` column schema.
- `internal/handler/enrich.go` — `EnrichISBNLookup` handler.
- ADR-0012 — bookdrop auto-enrich consumes this chain when book has an ISBN.
- ADR-0013 — graceful-degrade policy that lets one chain step swallow errors and continue.
