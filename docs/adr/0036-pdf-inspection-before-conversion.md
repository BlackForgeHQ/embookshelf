# A PDF is inspected before it is converted, and a scanned one is refused loudly — not OCR'd

The converter extension (ADR-0033) hands every PDF to anydoc. anydoc
reads the text layer; a scanned or image-only PDF has none, so the
conversion yields near-empty markdown that then silently feeds the
reading guide and the generated EPUB — the exact silent-degrade class
ADR-0033 §5 was written to refuse. This ADR adds classification ahead
of conversion and settles what happens to each class.

## Status

accepted (2026-08-16)

## Decisions bundled here

### 1. firecrawl/pdf-inspector joins the sidecar as a crate dependency

Rust, MIT, classifies a PDF as TextBased / Scanned / ImageBased / Mixed
in 10–50 ms by sampling content streams for text vs image operators —
and carries its own PDF→Markdown converter (tables, multi-column,
heading detection). A crate, not a second service: the extension stays
one process, one wire surface.

### 2. Inspection is an internal first step of `/convert`, not a new endpoint

One endpoint, unchanged happy-path wire contract, zero Go-tier changes.
The refusal rides the existing loud-failure choreography: a 422 whose
message lands verbatim on the rendition row and reaches the guide
preflight. A separate `/inspect` preflight (button-time refusal before
any job) was deferred — the bytes would upload twice and the in-convert
gate is needed regardless; the error body's new `class` field is the
seam it would plug into.

### 3. Scanned and ImageBased are refused with the evidence; no OCR

The 422 body is `{"error": "<class + per-page stats>", "class":
"scanned"|"image"}`. No OCR engine enters the stack: none exists here
today, the cost surface (tesseract/ocrmypdf/paid APIs, container size,
quality tuning) is a decision of its own, and the classification field
is deliberately machine-readable so a future OCR stage can route on it
without re-litigating this ADR.

### 4. TextBased converts through pdf-inspector; Mixed converts above a text-page ratio

pdf-inspector's converter replaces anydoc on the PDF axis — the quality
improvement is the point, and classifier + converter share one parse.
Mixed is the normal book shape (image cover page, plates), so it
converts when at least a constant threshold of sampled pages carry text
and refuses with the stats otherwise. The threshold is a converter-side
constant, not admin config — a dial nobody can reason about is not a
setting. If the classifier's own Mixed/Scanned line proves conservative
enough in practice, the threshold collapses to trusting it.

### 5. anydoc stays as the fallback for a PDF the gate approved

A TextBased/Mixed PDF that pdf-inspector's converter fails on falls
back to anydoc before any 422 — "a book that converted yesterday
refuses today" is the regression this buys out of, for ~five lines,
while anydoc is linked anyway for every non-PDF format. Each fallback
is logged; delete the path once the log stays quiet for a release.

### 6. Existing renditions are untouched; the improvement arrives via the bulk run

Staleness stays "the book's bytes changed" (source hash), never "the
converter got better" — widening that vocabulary for a one-time
migration was rejected, and auto-regeneration violates the
cost-follows-explicit-action rule (ADR-0024 §4). The admin re-runs the
existing bulk conversion when they want old books re-converted; the
recorded converter version already tells them which rows predate the
switch.

## Conditional

pdf-inspector's converter quality and its Mixed/Scanned line are
README claims until smoke-tested against 2–3 real PDFs from a live
library, compared with anydoc's current output. If quality disappoints,
it stays as the classifier only (decision 4 reverts to anydoc
conversion; everything else stands).
