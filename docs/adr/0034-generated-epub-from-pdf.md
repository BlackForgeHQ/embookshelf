# A generated EPUB is another file of the same Book, and the book stays a PDF

The converter extension (ADR-0033) gives a PDF a Markdown rendition; this ADR
adds the second stage that ADR-0033 §1 deferred: markdown → EPUB, so a PDF
book can be carried onto e-readers as a real EPUB. It settles what the
generated EPUB *is*, where the rendering runs, and how the pipeline chains.

## Status

accepted (2026-08-10)

## Decisions bundled here

### 1. The generated EPUB is a `files` row on the same Book

The ADR-0025 question, re-asked, with the same answer for the same reason:
the EPUB is the thing the user wanted — downloadable, sendable, survives a
rescan — not commentary about the book. A new `books` row was rejected as it
was for audiobooks (forked identity: two cards, two progress values), and a
rendition-style cache without a `files` row was rejected because that shape
is for machine feed (the markdown), not user deliverables — exactly the
guide/audiobook distinction ADR-0025 §1 drew.

Book deletion already covers it: `BookDelete` iterates `files` rows through
Storage (the ADR-0025 §6 fix), and `book_epub_renditions.file_id` is
`ON DELETE SET NULL` off the files row.

### 2. `books.format` stays PDF — nothing flips

There is no format-priority recompute in the code: `books.format` is set at
ingest and `primaryFile` matches it first. The generated EPUB deliberately
does not change that — the reader keeps opening the PDF, Send-to-Kindle
keeps sending the primary file, guides keep feeding on the Markdown
rendition. Reading the generated EPUB in-app would be a rendition-dispatch
feature (ADR-0025 §3's vocabulary) and is explicitly out of scope; the EPUB
is downloadable from the Versions tab. This is the part a future reader will
trip over: a book with an EPUB `files` row whose format says PDF is
deliberate, not a bug.

### 3. The sidecar renders it — with pulldown-cmark, not pandoc

ADR-0033 §1 assumed markdown → EPUB needs "a heavy non-Go dependency, which
is exactly the kind the sidecar exists to contain". It doesn't: an EPUB is a
zip of XHTML plus OPF, `pulldown-cmark` (pure Rust) renders markdown to
XHTML, and the container is hand-built. `POST /render/epub` takes JSON
`{markdown, title, author, language}` — metadata must travel for the OPF,
and JSON beats headers because headers mangle non-ASCII titles — and answers
raw `application/epub+zip` with the converter version header; errors keep
the `{"error"}` shape. Chapters split on H1 headings (anydoc emits them from
PDF structure), with a single-chapter fallback.

### 4. One EPUB per book, tracked by `book_epub_renditions`, regeneration overwrites

The row mirrors `book_audiobooks` minus the segment machinery: state,
verbatim error, `file_id` pointer (provenance is a pointer, not a flag —
no `is_generated` column, no scan changes), `source_content_hash` of the
**PDF** (staleness answers "is this EPUB from the current book" in one
comparison, same as the audiobook), converter version. Regenerate deletes
the old files row and bytes at finalize and overwrites — but with a plain
button, no type-to-confirm: this costs sidecar CPU, not dollars.

A separate table rather than a `kind` column on `book_markdown_renditions`:
the two artifacts differ where it matters (`file_id`, files-row lifecycle),
and a discriminator would tax every query for two genuinely different shapes.

### 5. The pipeline chains through the Markdown rendition's wait pattern

The `epub.render` job requires a fresh Markdown rendition: ready → render;
missing or stale → request conversion and return a transient error, so
River's retry becomes the wait (the guide feed's exact pattern); markdown
conversion failed → the EPUB row fails with that message verbatim. No new
orchestration vocabulary.

## Companion artifacts

- `CONTEXT.md` — Generated EPUB.
- ADR-0025 — the identity decision this reapplies, and the `files`-row
  delete path it relies on.
- ADR-0033 — the converter extension and the Markdown rendition this
  chains from.
