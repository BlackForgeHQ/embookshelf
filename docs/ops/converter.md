# Converter extension

The converter is an optional sidecar (ADR-0033) that turns document
bytes into GitHub-Flavored Markdown — the **Markdown rendition** that AI
features feed on. Today that means reading guides for PDFs: without the
converter a PDF guide is metadata-only; with it, the guide reads the
book's actual text.

embookshelf runs fine without it. Every feature that needs it says
"converter extension is not configured" instead of degrading silently.

## What it is

- A Rust service (axum + [anydoc]) in `extensions/converter`, published
  as its own image: `ghcr.io/blackforgehq/embookshelf-converter`.
- Wire contract: `POST /convert` — raw file bytes in, raw
  `text/markdown` out, converter version in `X-Converter-Version`;
  errors are JSON `{"error": "..."}` (415 undetectable format, 422
  unparseable document). `GET /healthz` answers reachability.
- **No auth in v1.** It must only be reachable on the compose-internal
  network. Never publish its port to the internet.

[anydoc]: https://github.com/firecrawl/anydoc

## Production

The service ships in `compose.prod.yml` behind a profile, so a plain
`up -d` never pulls it:

```bash
docker compose -f compose.prod.yml --profile converter up -d
```

Pin a version with `CONVERTER_VERSION=0.1.0` in `.env` (defaults to
`latest`; release tags follow `embookshelf-converter-vX.Y.Z`).

Then, as an admin: **Settings → Converter** → set the base URL to
`http://converter:6070`, enable, save. The card's status probe should
answer "Reachable" with the sidecar's version.

## Development

```bash
make converter-up      # build + start on :6070 (compose.dev.yml)
make converter-stop
make converter-test    # cargo test (needs a local Rust toolchain)
```

Point the admin setting at `http://localhost:6070`.

Smoke test without the app:

```bash
curl --data-binary @book.pdf http://localhost:6070/convert
```

## How conversion runs

Nothing converts eagerly. The first feature that needs a book's text —
or an admin `POST /api/v1/books/:id/markdown` — enqueues a
`markdown.render` job. The job reads the book through its library's
storage (local and S3 alike), POSTs the bytes to the sidecar, and
writes the markdown into the book's own folder as `{Title}.md`. The
`book_markdown_renditions` row records state, the storage location, the
source file's content hash and the converter version.

A rendition whose recorded hash no longer matches the book's current
file is **stale**: labelled and re-converted on the next request, never
silently fed to a model.

## Failure states, verbatim

The row's error channel is surfaced word-for-word (ADR-0033 §5):

| You see | Meaning | Fix |
| --- | --- | --- |
| `converter extension is not configured` | CONVERTER row disabled or missing a URL | Enable it in Settings → Converter |
| `Unreachable: dial tcp …` (settings card) | Sidecar down or wrong URL | Check the container, the URL, the network |
| `PDF has no extractable text (Scanned, N pages): OCR is required` | anydoc found only images | No fix in v1 — the converter does not OCR |
| Other 422 messages | Document structurally unusable (encrypted, malformed) | The message names the part; usually the file itself |

Transient failures (network, sidecar restart) are retried by the job
queue with the error visible on the row in the meantime; document-level
refusals (415/422) are permanent and never retried — re-trigger
conversion after fixing the cause.

## Formats

Convertible = **PDF only** today: the intersection of what anydoc
converts and what embookshelf can ingest but not read natively. EPUB is
deliberately excluded — native extraction serves it with no sidecar
(gap-filler routing, ADR-0033 §2).
