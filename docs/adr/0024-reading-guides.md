# LLM-generated reading guides live outside the book metadata pipeline

embookshelf stores a lot of books, and a title plus a publisher blurb is a poor basis for deciding whether to read one. A **Reading guide** is a short LLM-written orientation — what the book is about, who it suits, who it does not, which reader problems it addresses — generated on request and stored in its own table, deliberately outside the ADR-0001 metadata write-back pipeline.

## Status

accepted (2026-07-27)

## Decisions bundled here

### 1. A Reading guide is not metadata, and does not travel with the file

`Book.Description` already exists: the publisher blurb, filled by a Metadata provider or extracted from the EPUB OPF. It is locked per-field, mirrored into the Sidecar, and embedded into the file itself on edit. A Reading guide is a different genre of text and a different kind of artifact — derived, regenerable, and ours rather than the publisher's.

Putting it on `books` would drag it into `UpdateMetadata`, which means into the Sidecar and into the reader's EPUB. Someone copying a book out of their library would be handing along machine-written commentary embedded as if it were the book's own metadata. It also inherits the field-list problem: one column on `books` must be threaded through seven positional lists in `internal/repo/book.go` (`bookCols`, the INSERT column list, the hand-numbered `VALUES` run, `Create`'s SELECT projection, `UpdateMetadata`'s SET clause and its argument slice, and `scanBook`).

So: `book_reading_guides`, one row per book, FK with `ON DELETE CASCADE`. Regeneration is an UPSERT.

### 2. Input follows what the format can give — EPUB gets full text, the rest get metadata

Guide quality tracks how much the model actually saw, and no full-text extraction exists in the codebase today. `pdf_strings.go` decodes `/Info` string literals (metadata); `EPUBProcessor.Extract` reads the OPF and cover, not the spine; there is no PDF library in `go.mod` at all.

EPUB full text is a contained piece of work — unzip, walk the spine, strip XHTML — with no new dependency. PDF needs a new dependency and still fails on scans with no text layer. CBZ would need OCR and audio would need transcription; neither is in scope.

Rather than restrict the feature to EPUB, every book gets a guide and each row records a **Source kind** (`full_text` | `metadata`) that the UI shows. This matters editorially, not just technically: for an obscure or self-published book, a metadata-only guide is largely the model inventing plausible content, and the reader deserves to know which one they are looking at.

### 3. One OpenAI-compatible seam, configured by base URL

Full book text leaves the instance under this setting, and embookshelf is self-hosted software. A single OpenAI-compatible adapter covers OpenAI, OpenRouter, Ollama, LM Studio and vLLM, so an operator who does not want their library read by a third party points `baseURL` at `http://localhost:11434/v1` and the text never leaves their machine. The API key is stored in an encrypted config field alongside the provider secrets (ADR-0010).

### 4. Generation is always an explicit action

Two triggers: a button on the book page, and an admin bulk **Guide run** that shows an estimated token volume before it starts. Both go through the existing River queue with SSE progress.

Nothing generates on bookdrop approve, deliberately, and this is where the feature diverges from auto-enrich (ADR-0012). Auto-enrich is a cheap metadata lookup, so running it per import is reasonable. A full-text LLM call is orders of magnitude more expensive; a 5,000-book import with a checkbox left on would produce a bill nobody asked for. Cost has to follow visibly from something a person did.

### 5. A Guide run never overwrites hand-edited text

Guides are user-editable. `edited_by_user` makes a row untouchable to bulk generation, which reports what it skipped; the per-book button still overwrites, because there the user is looking at the guide they are replacing.

This mirrors the per-field locks on book metadata, and it exists because the inverse has already bitten this codebase once: `AutoEnrich` persisted a synthesized lock overlay and permanently locked every populated field on every auto-enriched book, silently, for want of a test (see ADR-0012 §2 as amended).

### 6. One guide language, set in settings

The table is keyed on `book_id` alone. Guides are written in a single configured language regardless of the book's own — a library is read by one person or one household who want text they can read, not a Japanese guide attached to a Japanese book. Changing the setting means regenerating. `Book.Language` is also frequently empty or wrong, so keying off it would be unreliable as well as unhelpful.

## Considered options

### Rejected: columns on `books`

One row, no join. But it puts generated prose inside `UpdateMetadata`, and therefore inside the Sidecar and the embedded file metadata, which is exactly what a Reading guide must not be. Also multiplies the seven-positional-list problem by the number of fields.

### Rejected: a JSONB column on `books`

Cuts the field-list work to one edit, but hides the structure from the schema and still routes the text through `UpdateMetadata` into the Sidecar.

### Rejected: full text for every format

Best quality, but requires a PDF text extractor (new dependency, defeated by scanned PDFs), OCR for CBZ, and transcription for audio. Cost also scales badly: a 100k-word book is roughly 130k tokens, so a thousand-book library is a serious bill even before the formats that cannot participate.

### Rejected: metadata only, for every format

Cheapest and uniform, and it needs no new extraction code at all. Rejected because the resulting guides are only as good as the model's prior knowledge of the title, which collapses for exactly the long-tail books where a guide would help most.

### Rejected: vendor-specific adapters (OpenAI SDK + Anthropic SDK)

Better use of vendor features — prompt caching in particular is real money on full-text input. Rejected for the first version because it adds two SDKs to a single-binary application and leaves local models as a third, separate path, which is the one self-hosted users most need.

### Rejected: calling this an "AI agent"

The original framing. It is a single-shot generation with no tools and no loop. Naming it an agent would put a term in `CONTEXT.md` that promises machinery the code does not have, and send the next reader looking for a control loop that was never written.

## Open questions

- How the four fields are reliably extracted from the model — JSON mode versus a tool call — and what happens when the response does not parse.
- Books longer than the model's context window. Truncation, chunk-and-summarize, or refuse and fall back to `metadata` as the Source kind.
- Whether to cache on a hash of the input so a repeated run does not pay twice for an unchanged book.

## Companion artifacts

- `CONTEXT.md` — Reading guide, Source kind, Guide generator, Guide run.
- ADR-0001 — the metadata write-back pipeline this deliberately sits outside.
- ADR-0010 — the encrypted-secret storage the API key reuses.
- ADR-0012 — auto-enrich, whose cost profile and whose overwrite bug both informed §4 and §5.
