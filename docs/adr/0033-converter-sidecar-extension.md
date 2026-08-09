# Format conversion runs in a sidecar extension, not in the binary

embookshelf wants machine-readable text for every book it holds — reading guides, tagging, and future retrieval all feed on it, and a planned PDF → EPUB path needs an intermediate text form. Native extraction covers EPUB only. This ADR settles how the other formats (PDF, DOCX, RTF, ODT, …) become text: a separately deployed Rust sidecar in `extensions/converter`, wrapping the anydoc library (MIT) behind an axum `POST /convert`, producing GitHub-Flavored Markdown.

## Status

accepted (2026-08-09)

## Decisions bundled here

### 1. A sidecar, because the binary must stay a single Go binary

No usable pure-Go converter exists for the PDF/Office family; anydoc is Rust. Linking it via cgo or shelling out to a bundled binary would trade away the single-binary property the same way `ffmpeg` would have for audiobooks (ADR-0027) — the feature goes silently dark on installs missing the dependency. A sidecar makes the dependency explicit and optional: embookshelf without the converter is fully functional, and features that need it report "extension not configured" rather than degrading.

v1 scope is one direction only: documents → markdown. The markdown → EPUB stage is future work and will live in the same sidecar when it comes (anydoc cannot produce EPUB; that stage needs a different tool, which is exactly the kind of heavy non-Go dependency the sidecar exists to contain).

### 2. Gap-filler routing: only Convertible formats go to the sidecar

EPUB extraction stays native (`fileproc`, `textsplit`). The uniform alternative — route every book through anydoc for one consistent markdown feed — was rejected because it converts a regression into a feature: an EPUB-only library, the common case and today fully self-contained, would suddenly require an optional sidecar for reading guides that already work. The converter earns its keep on formats embookshelf cannot read at all. The set is named **Convertible format** in `CONTEXT.md`, deliberately distinct from Eligible format (Kindle) and Narratable format (audiobooks).

### 3. Bytes over the wire, not links

embookshelf reads the file through `storage.Storage` and streams the body to `/convert`. The link alternative (presigned S3 URL) was rejected because it only exists for the s3 backend — a local-backend book has no URL to send without a second internal-download endpoint plus auth for it — and it hands the sidecar network reach and trust rules it otherwise doesn't need. anydoc converts from raw bytes in milliseconds; a streamed PDF on the docker network costs nothing.

Contract: request body = raw file bytes (format detected from content signatures, `Content-Type` is a hint at most); 200 = raw `text/markdown` body with the converter version in a response header (no JSON envelope around megabytes of text); errors are JSON (`415` undetectable format, `422` parse failure). `GET /healthz` for the admin panel. No auth in v1 — the sidecar binds to the internal network only; a shared bearer token via the `Setting[T]` Secrets slot is the upgrade path if that boundary ever moves.

### 4. The result is a Markdown rendition: a storage file plus a provenance row

Follows ADR-0025's shape: the markdown is written through `storage.Storage` beside the book, and a DB row records the source `content_hash` and converter version. Hash mismatch means stale — labelled, never silently treated as current, nothing auto-invalidated. Storing the text in Postgres was rejected: a full book is megabytes of text bloating backups for no query benefit, and every AI consumer already reads book bytes through storage.

### 5. On-demand, loud, via the job queue

Conversion runs as a River job enqueued by the first feature that needs the book's text and finds no fresh rendition. Eager conversion at ingest was rejected — it converts a whole library nobody asked about and fails at every scan while the optional sidecar is down. Failure handling is loud by policy: the row distinguishes "extension not configured" from "failed: <error>", both surfaced verbatim to the requesting feature. No silent fallback — the reading-guide local-library bug is the standing exhibit for what silent degradation costs here.

### 6. Configuration is a `CONVERTER` settings row

URL + Enabled as a `Setting[T]` in the settings registry — seeded at boot, admin-panel editable, secrets-capable later. An env var was rejected as invisible and restart-bound; a `storage_backends`-style registry was rejected because that pattern pays for multiple pluggable instances and there is exactly one converter.

## Considered options

Rejected alternatives are covered inline: in-binary linking (§1), uniform routing through anydoc (§2), link-passing (§3), Postgres text storage (§4), eager conversion (§5), env-var config (§6).

## Companion artifacts

- `CONTEXT.md` — Converter extension, Markdown rendition, Convertible format.
- ADR-0025 — the rendition shape §4 follows.
- ADR-0027 — the single-binary reasoning §1 inherits.
- ADR-0024 — reading guides, the first consumer.
