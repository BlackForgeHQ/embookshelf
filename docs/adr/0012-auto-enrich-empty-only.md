# Bookdrop auto-enrichment fills empty fields only

When a book is approved out of bookdrop, `EnrichmentService.AutoEnrich(ctx, book)` runs in the background to backfill missing metadata from external providers. The policy is "fill empty unlocked fields, leave non-empty fields alone." It is implemented by **`ApplyOptions{OnlyEmpty: true}`**, an explicit argument to `ApplyMatch`. The stored `*_locked` columns are not involved: they carry the user's intent and nothing else. Provider selection prefers the ISBN chain (ADR-0011); if the book has no ISBN, fan-out runs and applies only when the top match has `Confidence ≥ 70`.

## Status

accepted (2026-05-03), amended 2026-07-27 — §2 replaced. The transient-lock
implementation it described was **not** safe, and had been corrupting data
since it shipped; see below.

## Decisions bundled here

### 1. Empty-only policy

Auto-enrichment is the silent path — no user clicked "apply." The local extractor (EPUB OPF, PDF `/Info`, audio tag readers) usually produces trustworthy values for whatever the file carries. The right job for auto-enrich is **gap-fill**: ISBN missing → fetch it; description missing → fetch it; title already extracted → leave alone.

### 2. Implementation: an explicit `OnlyEmpty` option

`AutoEnrich` passes the policy as an argument:

```go
_, err := s.ApplyMatch(ctx, book, *match, ApplyOptions{
	MergeCategories: true,
	OnlyEmpty:       true,
	ApplyCover:      !book.Locks.Cover && !book.HasCover,
}, TriggerAutoEnrichment)
```

`ApplyMatch` gates every field on one predicate — never write a locked
field, and under `OnlyEmpty` never write a populated one — so auto-enrich
and manual-apply still share a single merge codepath.

**This replaces the original transient-lock implementation, which was
wrong.** That version cloned `book.Locks`, set every populated field's flag
true, and passed the book to `ApplyMatch`. The claim recorded here — that
"the rest revert because the overlay was on the in-memory copy" — does not
hold: pass-by-value protects the *caller's variable*, not the row. The
in-memory copy is exactly what `ApplyMatch` hands to the write step, and
`BookRepo.UpdateMetadata` writes all 15 `*_locked` columns from it.

The consequence was live from the day it shipped, not merely latent behind
the pointer refactor §2 warned about: **every auto-enriched book came out
with every already-populated field permanently locked** — locks the user
never set, which shielded those fields from all future enrichment and could
only be cleared by hand, one book at a time. It went unnoticed because
`AutoEnrich` had no tests; the "test coverage in `service` should pin this"
line in the original §2 was never acted on.

Damage already written is left in place. A lock set by this bug is
byte-identical to one a user set deliberately — `books` records no
provenance — so a repair migration would have to either clear locks users
meant to keep or guess. Unlocking a field someone chose to protect is worse
than leaving one they did not choose. Existing locks are visible and
clearable per book in the edit UI.

`AutoEnrich` and the `OnlyEmpty` gate are now covered in
`internal/service/enrichment_apply_test.go`, including a test that fails if
the lock overlay is reintroduced.

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

### ~~Rejected~~ Adopted 2026-07-27: explicit `ApplyOptions{OnlyEmpty: true}` flag

Originally rejected as "cleaner than transient-lock synthesis but duplicates
the lock-honoring branch in `ApplyMatch`". The duplication never
materialised — one predicate covers both rules — and the alternative it was
rejected in favour of was silently corrupting data. Adopted; see §2.

## Companion artifacts

- `internal/service/enrichment.go` — `AutoEnrich`, `ApplyMatch`, `ApplyOptions`.
- `internal/service/decide_effects.go` — `TriggerAutoEnrichment` definition; ADR-0001 §3 documents which write-back side-effects auto-enrich does and does not fire.
- ADR-0001 — auto-enrichment skips the in-file embedded write step (rezip / `/Info` patch). Auto-enrich is DB + sidecar only.
- ADR-0011 — ISBN chain consumed here.
