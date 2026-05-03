# Bookdrop auto-enrichment fills empty fields only, via transient locks

When a book is approved out of bookdrop, `EnrichmentService.AutoEnrich(ctx, book)` runs in the background to backfill missing metadata from external providers. The policy is "fill empty unlocked fields, leave non-empty fields alone." It is implemented by **cloning `book.Locks` in-memory, marking every currently-non-empty field as locked**, and calling the regular `ApplyMatch` with that synthesized lock set. The DB locks are never mutated. Provider selection prefers the ISBN chain (ADR-0011); if the book has no ISBN, fan-out runs and applies only when the top match has `Confidence ≥ 70`.

## Status

accepted (2026-05-03)

## Decisions bundled here

### 1. Empty-only policy

Auto-enrichment is the silent path — no user clicked "apply." The local extractor (EPUB OPF, PDF `/Info`, audio tag readers) usually produces trustworthy values for whatever the file carries. The right job for auto-enrich is **gap-fill**: ISBN missing → fetch it; description missing → fetch it; title already extracted → leave alone.

### 2. Implementation: transient lock synthesis

`AutoEnrich` reads `book.Locks` (a copy — `book` is passed by value), then:

```go
applyLocks := book.Locks
if strings.TrimSpace(book.Title) != "" { applyLocks.Title = true }
if strings.TrimSpace(book.Author) != "" { applyLocks.Author = true }
// … one branch per field …
book.Locks = applyLocks
_, err := s.ApplyMatch(ctx, book, *match, …, TriggerAutoEnrichment)
```

`ApplyMatch` already honors `book.Locks` for every field write. Reusing that gate means auto-enrich and manual-apply share one merge codepath; no parallel "is field empty" branch in the writer. The DB row's real lock columns are not touched — `ApplyMatch` writes the post-merge book back, including `Locks`, but the synthesized true-flags only persist if a real lock was already set; the rest revert because the overlay was on the in-memory copy.

**Hidden invariant: `book` must remain pass-by-value down to `ApplyMatch`.** If a future refactor turns it into `*model.Book`, the synthesized locks become persistent corruption — every auto-enriched book ends up with every field locked. Test coverage in `service` should pin this. If the codepath needs to evolve, prefer making the empty-only policy explicit in `ApplyOptions{OnlyFillEmpty: true}` rather than a different lock-synthesis trick.

### 3. ISBN chain preferred over fan-out

If `book.ISBN` is non-empty, `LookupByISBN` runs first. ISBN is an identity match: when Hardcover (or whoever ranks first) returns a hit for `978-…`, that's the right book. Skipping fan-out saves N-1 upstream calls per import — material when a 5k-book bookdrop processes overnight.

If `book.ISBN` is empty, fall back to `Search(Query{Title, Author})` and apply only when top `Confidence ≥ 70`. Rationale: scorer's 65 tier is "title contains" (every Goodreads search hit clears that); 70 demands a stronger token alignment, filtering pure prefix-noise like "Java" matching every Java book ever written.

### 4. Cover applied if-and-only-if both: cover unlocked AND `!book.HasCover`

Even with empty-only policy, cover handling has its own gate because cover existence is a boolean (HasCover), not an "empty string" check. We import a cover from URL only when there is no existing cover at all, regardless of where the book got it (extracted from EPUB, manually uploaded, prior auto-enrich).

### 5. Categories union, not replace

`ApplyOptions{MergeCategories: true}` means `book.Genres` ∪ `match.Categories` instead of overwrite. Auto-enrich is gap-fill semantically; for genres specifically, "gap-fill" reads as "add what's missing," not "ignore the candidate because the book already has one tag."

### 6. Confidence threshold is hard-coded at 70

Not configurable. Keep it constant until someone shows a real-world false-positive rate that forces a tunable. A configurable threshold is cheap to add later; a configurable threshold added prematurely is a knob admins ignore and tickets blame.

## Considered options

### Rejected: always overwrite unlocked fields

The "provider data > extractor data" assumption is wrong for self-hosted libraries. Users frequently hand-edit OPF or sidecar before approving; auto-enrich shouldn't second-guess that.

### Rejected: never auto, manual review required (BookLore proposal-table shape)

BookLore stages provider candidates in `metadata_fetch_proposal` for a user to review. For embookshelf's "drop a folder, walk away" use case, that's too much friction — the user wanted enrichment to happen, not to come back to a queue. ADR-0012's empty-only policy plus the existing per-field locks gives users a finer-grained way to opt-out (lock the fields they want to protect) without a review step.

### Rejected: explicit `ApplyOptions{OnlyFillEmpty: true}` flag

Cleaner than transient-lock synthesis but duplicates the lock-honoring branch in `ApplyMatch`. Worth revisiting if/when the value-vs-pointer invariant in §2 becomes load-bearing for some other reason.

## Companion artifacts

- `internal/service/enrichment.go` — `AutoEnrich`, `ApplyMatch`, `ApplyOptions`.
- `internal/service/decide_effects.go` — `TriggerAutoEnrichment` definition; ADR-0001 §3 documents which write-back side-effects auto-enrich does and does not fire.
- ADR-0001 — auto-enrichment skips the in-file embedded write step (rezip / `/Info` patch). Auto-enrich is DB + sidecar only.
- ADR-0011 — ISBN chain consumed here.
