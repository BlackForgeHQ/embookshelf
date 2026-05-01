# Edit-side metadata write-back to file + sidecar

When a user edits book metadata in the UI (or applies an enrichment match), embookshelf writes the change to three places in fixed order: **DB → JSON sidecar → file embedded**. The DB is the canonical source; file and sidecar are export artifacts so the metadata travels with the file when copied or read by external tools (Calibre, Kobo, etc.).

## Status

accepted (2026-04-30)

## Decisions bundled here

### 1. TOML sidecar hard cutover

Plan D shipped a `.embookshelf.toml` sidecar (read+write). The new format is `<basename>.embookshelf.json` (paired filename next to the book file). **No migration code was written**: TOML is dropped entirely on cutover — no read, no write, no rename. Users with TOML files at upgrade lose the overlay; the field set falls back to whatever the embedded extractor + DB carry, and the next manual edit re-emits the JSON sidecar.

**Why no migration?** User base small, edit cycle is high-touch (the user re-saves after upgrade noticing missing fields), migration code adds a one-shot drainer + a `.bak` rename rule with its own bug surface. The pragma is "ship the new shape, ask early adopters to re-apply." Future readers debugging a missing tag should know we *chose* to drop the old format rather than ship a half-tested migration.

### 2. Lock-aware re-extract on external file change

`books.*_locked` columns (`title_locked`, `author_locked`, `genres_locked`, etc.) already gate metadata-enrichment writes. **The same locks shield re-ingest from external file changes.** When the library scan detects a file's content hash changed:

1. Re-extract from file embedded + JSON sidecar overlay.
2. For each field: if `<field>_locked = TRUE`, keep DB value, drop extracted value.
3. Apply the rest to DB.

**The non-obvious bit:** most readers expect "external edit wins, period" — embookshelf takes the position that a *locked* field is the user's promise to themselves. If they used an external editor (Calibre, Sigil, kepub-tools) to change a locked field, the locked DB value still wins on next scan. Unlocked fields follow the obvious external-wins rule.

This keeps a single mental model for locks across enrichment auto-apply, manual UI edits, and re-ingest. A field marked locked is shielded from every kind of automatic update — no further reasoning needed.

### 3. In-file write scope: local-backed libraries + explicit user actions only

The in-file embedded write step (EPUB rezip with new OPF + cover bytes; PDF `/Info` text patch) is gated by **two conditions**:

| Condition                       | In-file write fires? |
|---------------------------------|----------------------|
| `lib.BackendID == nil` (local)  | yes                  |
| `lib.BackendID != nil` (S3)     | **no** (Phase 1)     |
| Trigger = manual UI edit        | yes                  |
| Trigger = apply-enrichment      | yes                  |
| Trigger = auto-enrichment (bg)  | **no**               |
| Trigger = library scan re-ingest| **no** (loop avoidance) |
| Trigger = bookdrop approve      | **no** (file is the source) |

When the in-file step is skipped — for any reason (S3 backend, format with no in-file target, write attempted and failed) — the JSON sidecar carries a **full mirror** of the metadata instead of the per-format spillover. Single rule: `inFileWritten == false → sidecar = full mirror`.

**Why S3 skipped Phase 1?** EPUB is 1-50MB. Per-edit cost on S3 = Get + Put + storage churn. Bulk-edit a 5k-book library = 10k object transfers. Local libraries pay zero (write-temp-then-rename on the same filesystem). The decision was deliberate: ship the simple shape, revisit if user demand justifies queued/deduplicated S3 writes (Phase 2 candidate). Future reader debugging "why didn't my S3 EPUB pick up the new title" — this is why.

**Why explicit-action-only triggers?** Auto-enrichment runs in the background and can apply matches across the whole library at once. Triggering a rezip on every match means a CPU-bound stampede of EPUB rewrites for what is, from the user's standpoint, a passive update. Manual edit and apply-enrichment are explicit "make this stick" actions; rewriting the file matches user expectation. Future readers seeing DB-and-sidecar drift from file should understand it's by design — the file gets updated when the user *says* so.

## Companion artifacts

- `docs/CONTEXT.md` — Sidecar entry updated with format/role/scope.
- `docs/spec/sidecar-write.spec.md` (TBD) — full implementation contract: per-format mapping (Q4), Tags/Genres encoding (Q12), trigger contract (Q13), failure semantics (Q9).
