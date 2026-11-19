# A PDF converts page by page: missing pages are recorded, not refused, and bookmarks are the chapters

ADR-0036 put a gate in front of PDF conversion: classify the file,
refuse the verdicts with no text, measure the output, refuse what came
out too thin. Probing a real library showed the gate's evidence was the
weak part. pdf-inspector's *whole-document* classifier stops at the
first page without text, so a book with an image cover — the ordinary
shape of a book — was classed ImageBased and refused, and its per-page
OCR flags marked pages of plain prose as needing OCR. The same engine's
*per-page* entry point gave the right answer on every file tried: our
fixtures and a 353-page encrypted publisher PDF. This ADR moves the PDF
path onto that evidence and settles what a partly readable PDF becomes.

## Status

accepted (2026-11-18). Supersedes ADR-0036 §4 and the post-smoke-test
gate design; §1–§3 (one engine, inspection inside `/convert`, no OCR)
and §6 stand.

## Decisions bundled here

### 1. One engine, asked twice

PDFs bypass `anydoc::to_markdown_bytes` and call pdf-inspector — which
is what anydoc does internally — directly. The **whole-document pass**
writes the text (tagged headings, ruled tables and cross-page flow are
its alone) with page markers on, so its output can be attributed to
pages. The **per-page pass** runs only for the pages the first one left
empty, and fills in what it finds. A page neither pass finds text on is
*missing*.

Rejected: markkit and pdfcraft as engines. markkit garbled about 28% of
the words of the real book (its font decoding, not encryption — anydoc
reads the decrypted bytes byte-identically) and refuses every encrypted
PDF; pdfcraft's PDF→Markdown lives in an unpublished desktop-app engine
with workspace-local patches and has worse reading order. Neither adds
a crate here; lopdf (already in the lock through pdf-inspector) becomes
a direct dependency for the bookmarks.

### 2. Partly readable PDFs convert; the missing pages are recorded

A PDF with text on some pages converts. The answer carries
`X-Converter-Page-Count` and `X-Converter-Missing-Pages` (1-based,
compact: `1,10-15`, empty when none); the markdown rendition row stores
both, and the book's Versions tab shows them beside the Markdown file.
Recorded rather than refused because what was refused was usually most
of a book — and recorded rather than dropped because silently missing
pages is the failure ADR-0036 existed to prevent. This retires the
`mixed`, `sparse` and `image` refusals.

### 3. Only a PDF with no text on any page is refused

422 with `class: "scanned"`, the seam a future OCR stage routes on
(ADR-0036 §3). Encrypted PDFs that need a password to *open* are 422
with their own sentence; publisher PDFs that only restrict copying open
with the empty password and convert.

### 4. "Missing" means no extractable text — nothing more

A deliberately blank page and a scanned one look the same from the text
alone, so both are listed. The per-page pass's own "garbled" flag is not
used to drop text: on the real book it fired on clean contents pages
(dot leaders), which the whole-document pass renders correctly. Text the
engine does produce is kept, as it was before.

### 5. Existing renditions are untouched

The record starts empty (`page_count = 0` reads "not recorded") and is
filled when a book is next converted. The bulk run still leaves ready
rows alone; widening it was rejected for the reason ADR-0036 §6 gives.

### 6. Top-level bookmarks are the chapters

When a PDF has two or more top-level bookmarks, each becomes a `#`
heading at the start of the page it points to, and the engine's own
`#` headings step down to `##`. The EPUB stage (ADR-0034) splits on
`#`, so its chapters are the author's rather than font-size guesses —
the real book went from 2 chapters to 24. A PDF without bookmarks keeps
the engine's headings, and the EPUB stage now splits on `##` when the
text has a single `#` over several `##` (that book's shape without
bookmarks).
