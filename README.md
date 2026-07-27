# embookshelf

Self-hosted, multi-user digital library — Go backend + React SPA
(TanStack Start + shadcn/ui) + Postgres, with optional
S3-backed storage. EPUB, PDF, comic (CBZ), and audiobook (MP3/M4B)
readers; full-text search; per-user shelves + smart shelves +
annotations; metadata enrichment across six providers; live BookDrop
import queue with drag-and-drop upload; OIDC/SSO (Google / GitHub /
generic, multi-link); reMarkable device sync; OPDS 1.2 catalog for
e-readers. Ships as a single binary with the UI embedded.

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the technical shape
and [docs/prd.md](docs/prd.md) for the product intent + roadmap.

> **2026-07 update — SQLite is no longer supported.** embookshelf is a
> Postgres-only application ([ADR-0023](docs/adr/0023-postgres-only.md)).
> This release **refuses to boot** on a `sqlite://` DSN. Migrate your
> library in one command:
>
> ```bash
> DATABASE_URL='postgres://user:pass@host:5432/embookshelf' \
>   embookshelf import-sqlite --from ./data/embookshelf.db
> ```
>
> The target Postgres database must be empty; migrations are applied to
> it automatically. Queued background jobs don't transfer — re-trigger a
> library scan afterwards.

## Quickstart

### Single-user / self-hosted

Postgres is required. The smallest setup is the bundled compose file:

```bash
docker compose -f compose.prod.yml up -d
```

Open http://localhost:6060 and create your admin user.

To point at an existing Postgres instead, set `DATABASE_URL`:

```bash
docker run --rm -p 6060:6060 -v $(pwd)/data:/data \
  -e DATABASE_URL='postgres://user:pass@host:5432/embookshelf' \
  ghcr.io/blackforgehq/embookshelf:latest
```

### Multi-user / production (Postgres)

For shared installs, run with an explicit `DATABASE_URL`:

```bash
docker run --rm -p 6060:6060 \
  -e DATABASE_URL='postgres://user:pass@dbhost:5432/embookshelf?sslmode=disable' \
  -v $(pwd)/data:/data \
  ghcr.io/blackforgehq/embookshelf:latest
```

The Postgres path supports concurrent writes and the full bookdrop ingest pipeline.

---

## Layout

```
cmd/                       Go entry points (embookshelf, migrate)
internal/                  Go backend — 27 packages tiered into Core / IO / Cross-cutting
                           (handler, service, repo, model, migrator,
                            storage{,/local,/s3}, storageloader, coverstore,
                            fileproc, extractor, sidecar, scan, ingest,
                            auth, config, crypto, db, queue, task, search,
                            sse, opds, provider, staticfs, telemetry, …)
internal/migrator/         migrations/postgres/ (+ migrations/sqlite/, kept only for import-sqlite)
internal/staticfs/dist/    Embedded SPA shell (written by the UI build)
ui/                        TanStack Start SPA (Vite + React 19 + Tailwind v4 + shadcn/ui)
docs/                      architecture.md, PRD.md, adr/, spec/, ops/, agents/, research/
e2e/                       Playwright tests (separate Node project)
scripts/seed.sql           Dev seed (admin@local / changeme)
```

## Development

Prerequisites: **Go 1.25**, **[Bun](https://bun.sh) 1.x**, Docker.

```bash
# One-time
make db-up           # start Postgres
make ui-install      # bun install inside ui/

# Iteration — one terminal
make up              # backend (air) + Vite on :6060 and :5173
```

Open <http://localhost:5173/>. Vite proxies `/api`, `/opds`, `/events`
to the Go server on `:6060`, so cookies and SSE keep flowing. `/v1/*`
is proxied to `:4318` for browser OTLP traces in dev (see
[compose.dev.yml](compose.dev.yml)).

If you want the processes separated (different panes, logs split):

```bash
make dev             # backend only — live-reload via `go tool air`
make ui-dev          # Vite dev server only (bun run dev)
```

First-run signup creates the admin. With the seed:

```bash
make seed            # pipes scripts/seed.sql → admin@local / changeme
```

## Production build

```bash
make build           # bun run build (ui) → internal/staticfs/dist, then go build
./tmp/embookshelf
```

The binary ships self-contained. The compiled SPA is embedded via
`//go:embed all:dist` in
[internal/staticfs/staticfs.go](internal/staticfs/staticfs.go), so
there is no Node/Bun runtime in production.

## What's live end-to-end

### Auth
- Session cookies, bcrypt passwords, first-run admin bootstrap, CSRF
  via SameSite + Origin/Referer check.
- **OIDC / SSO** — Google, GitHub, and a generic OIDC provider wired as
  three independent, parallel configs. PKCE (S256) on every flow,
  server-held state cache with 5 min TTL, shared `/callback` routed by
  the slug embedded in state. Admin `Settings → OIDC / SSO` configures
  credentials, claim mappings, auto-provisioning policy, and an
  optional "force SSO" mode that hides the password form (escape hatch
  at `/login?local=true`).
- Redirect URIs fall back to the live request's scheme + host when
  `APP_URL` is unset, so local dev and self-hosted deployments behind a
  reverse proxy work without extra env wiring.
- **Forward auth (reverse-proxy headers)** — for setups that terminate
  SSO at the proxy (Authelia, oauth2-proxy, Traefik forwardAuth,
  Cloudflare Access). Stateless per-request: trusts identity headers
  only when the immediate TCP peer matches `trustedProxyCIDRs`,
  ignores `X-Forwarded-For`, no session cookie minted so proxy
  logout propagates immediately. Auto-provisioning + email auto-link
  reuse the OIDC policy row. See
  [docs/ops/forward-auth.md](docs/ops/forward-auth.md) and ADR-0022.

### Library, shelves, reading
- Multi-library model with **one root per library**, fixed at creation.
  Each library targets a backend — local FS or S3 (per-library `kind`).
  Per-user shelf CRUD, book-to-shelf toggle, full-text search
  (tsvector over title + author + series + description), sort,
  format + shelf + library filters, plus a global ⌘K command palette.
- **Smart shelves** — rule-based auto-populated shelves alongside
  hand-curated ones, both styled with an accent picker.
- **Readers** — EPUB (epub.js, paginated, TOC, CFI resume), PDF
  (pdfjs-dist, lazy-rendered pages, `page:N` resume), Comic / CBZ
  (server-extracted per-page images, keyboard nav + manga mode), and
  Audiobook (MP3/M4B via `<audio>` + range requests, duration +
  narrator from `dhowden/tag`). One `user_book_progress.resume_cfi`
  column carries every format's resume token, prefix-discriminated
  (`epubcfi(...)`, `page:N`, `time:Ns`).
- **Reading sessions + stats** — per-user time-spent rollups feed the
  Stats heatmap and `/stats/reading` endpoint; updates broadcast via
  SSE.
- **Annotations** — highlights + margin notes with a React highlight
  layer over epub.js; recent-activity feed in the Notebook view.

### Metadata
- **Editor** — the Edit-metadata page covers the full surface: title,
  subtitle, authors, description, language, publish date, year, pages,
  ISBN-10 + ISBN-13, publisher, series (name / #/ total), genres,
  moods, tags, age rating, content rating, and a tri-state public
  review toggle. Inline edits save to `PATCH /api/v1/books/:id`.
- **Streaming enrichment** — `POST /books/:id/enrich/stream` is a
  Server-Sent Events endpoint; each provider frame arrives as it
  finishes so the match list fills in as providers return. Frames are
  `event: match`, `event: provider-error`, `event: done`; client
  disconnects cancel every in-flight outbound HTTP call via context
  propagation.
- **Per-field locks** — padlock toggles next to title, subtitle,
  author, description, publisher, series, ISBN-13/10, language,
  publish date, pages, genres, moods, tags, and cover. Locked fields
  are skipped by the apply-metadata flow (`PUT /books/:id/metadata`)
  and auto-enrich. Manual `PATCH` still writes — the lock is against
  automation, not user intent.
- **Six providers** — Google Books (API), Open Library (API),
  Hardcover (GraphQL; requires token), Goodreads (HTML scrape, polite
  with 60 s cooldown on 429/403), Amazon (ISBN-10 cover fallback only),
  DuckDuckGo (Wikipedia summary). Confidence scoring uses a fuzzy
  Levenshtein tier so slight title/punctuation drift still sorts
  sensibly. Results are cached in-process for 5 min; a per-provider
  60 s cooldown kicks in on 429 so repeated refetches don't burn rate
  limits.
- **Provider config + priority** — per-provider JSONB config
  (API keys, language, cookie; stored encrypted when a KEK is set),
  enable/disable, and priority ordering. Priority drives the ISBN
  lookup chain: `POST /books/metadata/isbn-lookup` walks providers
  in rank order, first hit wins.
- **Apply flow** — streaming search returns candidate cards; each
  card has `Apply` (atomic server-side write, respects locks, optional
  cover), `Use fields` (populate form for review), and `Use cover`
  (cover-only).
- **Auto-enrich on bookdrop approve** — opt-in toggle in
  `Settings → Metadata providers`. On approval, the service runs the
  ISBN chain (falling back to fan-out at Confidence ≥ 70) and writes
  only fields empty on the extracted book, respecting existing locks.
- **Provider health** — every `Search` call records last-success /
  last-error timestamps + error string. Each row in the settings
  panel shows a badge like `ok 12m ago` or `failed 2m ago — hardcover
  401` so stale tokens don't silently rot.
- **Secrets at rest** — password-kind config fields (API keys,
  tokens) are encrypted with AES-256-GCM before hitting
  `provider_settings.config`. KEK comes from
  `EMBOOKSHELF_SECRET_KEY` (base64-encoded 32 bytes); unset = log a
  warning and round-trip plaintext. Pre-encryption rows pass through
  on read; next write upgrades them.
- **Sidecar write-back (ADR-0001)** — every metadata edit lands in
  three places: the DB, a `<basename>.embookshelf.json` sidecar
  (canonical lossless round-trip), and a Calibre-compatible
  `metadata.opf`. EPUB and PDF additionally embed the changes into
  the file itself (cover + OPF for EPUB, Info dictionary for PDF).
  Reattach on rescan reads the sidecar so user edits survive
  `library.scan` even after a rename.

### BookDrop + covers
- Watcher polls `./bookdrop` every 5 s, extracts EPUB / PDF / CBZ /
  audio metadata + cover, surfaces the file for review;
  approve/reject promotes into a real library row and moves the file
  under the target library backend (local copy or S3 PUT).
  Drag-and-drop upload from the BookDrop page hits
  `POST /api/v1/bookdrop/upload` (streamed multipart, no buffering).
- **PDF discovery** parses XMP packets (DC + Identifier Bag), decodes
  hex / UTF-16BE DocInfo strings, normalises ISBNs, and accepts a
  client-rendered page-1 cover via `PUT /bookdrop/:id/cover`
  (ADR-0015) so the queue card shows the right artwork even when the
  PDF lacks an embedded thumbnail.
- Cover images render as real `<img>` (with lazy-loading + onError
  fallback to the typographic tile) once the backend has the
  extracted bytes. SHA-256 dedup means books pointing at the same
  artwork share one blob in `coverstore`; a `coverVersion` token on
  every cover URL busts caches when admins remove or replace artwork
  via `DELETE /books/:id/cover`.
- **Format badges** on every cover (shadcn Badge) — EPUB, PDF, CBZ,
  MP3/M4B, MOBI, FB2 — color-coded per format.

### Settings (admin-only)
Left-nav panels at `/settings`:
- **Libraries** — create/delete (local or S3 backend), root immutable,
  trigger rescans, see last-scan stats, typed-name confirmation on
  delete.
- **Metadata providers** — enable/disable each provider, reorder
  priority with up/down arrows, fill provider-specific config
  (Google Books API key + language, Hardcover token, …), toggle
  auto-enrich on bookdrop approve, and see per-provider health
  badges. Google Books / Open Library / Amazon / DuckDuckGo are
  enabled by default on fresh installs; Hardcover + Goodreads land
  disabled (the former needs a token, the latter is scrape-only and
  brittle).
- **Reading guides** — LLM-written orientation per book: what it is
  about, who it suits, who should skip it, which reader problems it
  addresses (ADR-0024). **Off by default and configured here, not by
  env var** — base URL, model, credential, guide language, and how much
  book text to send. Any OpenAI-compatible endpoint works, so a local
  Ollama keeps every book on your own hardware. `Test connection`
  sends one short prompt and shows what came back. Generation never
  happens on its own: it is a button on a book, or an admin run over
  the library that shows a token estimate first.
- **Metadata defaults** — instance-wide defaults that flow into
  newly-imported books (language, default tags, …).
- **OIDC / SSO** — DB-backed config (issuer, client id/secret,
  scopes, claim mappings) per slug; `Test connection` runs an OOB
  discovery + token call without persisting.
- **Users & roles** — admin / user split, approve/deny pending OIDC
  signups, role transitions, hard delete.
- **Instance** — instance name, signup-open flag, default library.
- **Email** — SMTP transport with hot-reload on save, `Send test`
  endpoint, encrypted credentials. Powers send-to-Kindle, password
  reset, and admin invites (ADR-0021). Callers return
  `503 EMAIL_DISABLED` when the transport is off.
- **Invites** — admin mints single-use tokens; recipient lands on
  `/accept-invite` and sets a password. Pairs with the password-reset
  flow (`/forgot-password` → emailed link → `/reset`).
- **BookDrop housekeeping** — admin-only clear-processed and
  preview / wipe orphan files (cross-user blast radius, ADR-0014).

Per-user preferences live at `/account` (profile, password, linked
OIDC identities) and in a sidebar-footer dropdown (reading
preferences, device sync, sign out).

### Device sync
- **reMarkable Paper Pro** pairing with a one-time code; push any book
  to a paired device from the book detail page. OPDS is still the
  vendor-neutral fallback.
- **Send-to-Kindle** via the email transport — per-user Kindle address
  on `/account`, `POST /books/:id/send-to-kindle` attaches the file
  and ships it through SMTP.

### Platform
- **OPDS 1.2** catalog at `/opds/*` with Basic Auth. Works with
  KOReader, Moon+ Reader, FBReader, Marvin, Aldiko, etc.
- **Realtime** — server-sent events at `/events`; background job state
  transitions invalidate TanStack Query caches without polling.
- **Notifications** — sonner toasts for every mutation (library
  create, rescan, role change, OIDC save, OIDC test, library
  delete, …). No stale inline banners.
- **Job queue** — [River](https://riverqueue.com) (4 workers,
  exactly-once via shared transactions, dashboard available) behind a
  one-method `queue.Client`; a job registry declares each kind once.
  River migrations apply
  alongside the app schema when `MIGRATE_ON_START=true`.
- **Pluggable storage** — every read/write of book bytes goes through
  `storage.Storage`. Local backends use POSIX FS; S3 backends
  (MinIO, R2, B2, AWS S3) issue presigned URLs (TTL via
  `EMBOOKSHELF_PRESIGN_TTL`) or stream through the server
  (`EMBOOKSHELF_PRESIGN_FALLBACK`). Edit-time folder renames on S3
  are copy + deferred delete with a grace window
  (`EMBOOKSHELF_S3_RENAME_GRACE`, ADR-0005) so already-issued
  presigned URLs don't 404 mid-download.
- **OpenTelemetry** — server traces / metrics / logs export via OTLP
  when `OTEL_ENABLED=true`; browser SDK gates on
  `VITE_OTEL_ENABLED=true`.

## UI stack

- **React 19 + TanStack Start** in SPA mode (no SSR in prod; a
  prerender pass emits the `_shell.html` entry).
- **Tailwind v4** via the first-class `@tailwindcss/vite` plugin — no
  standalone CLI watcher, no separate generated stylesheet. Design
  tokens live in [ui/src/styles.css](ui/src/styles.css) under `@theme`.
- **[shadcn/ui](https://ui.shadcn.com)** (radix-mira style) provides
  the primitive layer (Button, Dialog, Select, Switch, Tabs, Dropdown,
  Popover, Badge, Sonner toasts, Breadcrumb, …) under
  [ui/src/components/ui/](ui/src/components/ui/). Editorial overrides
  (`.btn`, `.chip`, `.cover`, `.t-h1`, …) live alongside the shadcn
  tokens in `styles.css`.
- **Bun** is the package manager + script runner. No `npm`/`node_modules`
  indirection in CI.
- **TanStack Query** for server state, **TanStack Router** for
  file-based routes. `useRealtime` wires the SSE stream into query
  invalidation.
- **Browser OpenTelemetry** (document-load, user-interaction, fetch
  instrumentations) ships with the app and is gated on
  `VITE_OTEL_ENABLED=true` — OTLP traces route to
  [grafana/otel-lgtm](compose.dev.yml) in dev via the `/v1` proxy.

## How the SPA is served

- **Build-time**: TanStack Start is configured in **SPA mode**
  ([ui/vite.config.ts](ui/vite.config.ts)). `vite build` produces
  `ui/dist/client/{_shell.html, assets/*}`;
  [ui/scripts/sync-dist.ts](ui/scripts/sync-dist.ts) (run via bun) copies
  the shell + assets into `internal/staticfs/dist/` and duplicates
  `_shell.html` as `index.html` so Go's SPA fallback finds it.
- **Runtime**: Go serves `/api/v1/*`, `/opds/*`, `/events`, and any
  static file from the embedded FS; anything else returns `index.html`
  via a `NoRoute` handler
  ([internal/handler/router.go](internal/handler/router.go)) so the
  TanStack Router can resolve on hard reloads.

Worth knowing:

- **Vite's `outDir` stays inside `ui/`** (`dist/`). Redirecting it
  outside breaks Node's module resolution during the prerender — the
  SSR bundle can't resolve `rou3`/`h3` when it lives under `internal/`.
  The sync script is what moves the final files out.
- **`@tailwindcss/vite` replaces the old Tailwind CLI watcher.** Earlier
  iterations of this repo ran `tailwindcss -i styles.css -o generated.css`
  as a side-process because `@tailwindcss/node` collided with Start's
  prerender; that's no longer the case under the current Vite plugin +
  SPA mode combo.

## Environment

All env vars are optional unless marked required; sensible defaults
live in [internal/config/config.go](internal/config/config.go).

Not everything is an env var. Metadata providers, OIDC, email and
**reading guides** are configured in the admin UI and stored in
`app_settings`, so they can be changed without a restart and their
credentials are encrypted at rest. The only env var reading guides care
about is `EMBOOKSHELF_SECRET_KEY`, which is what encrypts the LLM key
you paste into the panel.

**Server / DB**

| Var | Default | Notes |
| --- | --- | --- |
| `DATABASE_URL` | `postgres://localhost:5432/embookshelf` | Postgres connection. Postgres is required (ADR-0023); a `sqlite://` DSN refuses to boot and points at `import-sqlite` |
| `EMBOOKSHELF_PORT` | `6060` | HTTP listen port |
| `ALLOWED_ORIGINS` | `*` | CORS / CSRF allow-list for `Origin`/`Referer` |
| `APP_URL` | _(unset, falls back to request origin)_ | Public origin; feeds the OIDC redirect URI |
| `MIGRATE_ON_START` | `true` | Apply app schema (+ River migrations on Postgres) on boot |
| `LOG_LEVEL` | _(reserved)_ | Read into config but not applied — `slog` is pinned to `info` in `main.go`. Wiring it up is a small change nobody has needed yet |
| `BOOKDROP_PATH` | `./bookdrop` | Watched folder for imports |
| `DATA_PATH` | `./data` | Covers + on-disk caches |
| `SESSION_SECRET` | _(reserved)_ | Not read today; reserved for a future JWT layer |

**Secrets**

| Var | Default | Notes |
| --- | --- | --- |
| `EMBOOKSHELF_SECRET_KEY` | _(unset — dev only)_ | Base64-encoded 32 bytes (`openssl rand -base64 32`). Encrypts provider API keys / OIDC client secrets / the reading-guide LLM key / cookies at rest with AES-256-GCM (ADR-0010). Unset = plaintext storage + loud boot warning |

**S3 storage** (only needed if you create `kind=s3` libraries)

| Var | Default | Notes |
| --- | --- | --- |
| `EMBOOKSHELF_S3_BUCKET` | _(unset)_ | Shared bucket name. Empty disables S3 library creation. |
| `EMBOOKSHELF_S3_REGION` | `us-east-1` (when bucket set) | AWS region |
| `EMBOOKSHELF_S3_ENDPOINT` | _(unset)_ | Custom endpoint (MinIO, R2, B2). Auto-prepends `https://` if scheme-less. |
| `EMBOOKSHELF_S3_ACCESS_KEY_ID` | _(unset)_ | Static credentials |
| `EMBOOKSHELF_S3_SECRET_ACCESS_KEY` | _(unset)_ | Static credentials |
| `EMBOOKSHELF_S3_FORCE_PATH_STYLE` | `false` | Path-style addressing (required by MinIO + some R2 setups) |
| `EMBOOKSHELF_PRESIGN_TTL` | `10m` | Presigned URL lifetime |
| `EMBOOKSHELF_PRESIGN_FALLBACK` | `""` (= stream) | `presign` to opt into 302 redirects for book delivery; otherwise the server streams bytes. **Presign requires bucket-side CORS** for the SPA origin. |
| `EMBOOKSHELF_S3_RENAME_GRACE` | `max(2 × PresignTTL, 1h)` | Window before the orphan sweeper deletes old keys after an edit-time rename (ADR-0005) |

**OpenTelemetry**

| Var | Default | Notes |
| --- | --- | --- |
| `OTEL_ENABLED` | `false` | Emit server traces/metrics/logs via OTLP |
| `OTEL_SERVICE_NAME` | `embookshelf` | Resource service.name |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | _(unset)_ | OTLP collector endpoint |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `grpc` | `grpc` or `http/protobuf` |
| `OTEL_EXPORTER_OTLP_INSECURE` | `true` | Disable TLS (local LGTM) |
| `OTEL_TRACES_SAMPLER_ARG` | `1.0` | Sample ratio (0.0–1.0) |
| `VITE_OTEL_ENABLED` | `false` | Browser-side SDK opt-in (build-time) |

## What's next

See [docs/prd.md § 11 — Planned](docs/prd.md) for the full greenfield
backlog. Near-term candidates:

- **CBR + AZW3/MOBI/FB2 ingest** — CBZ + audio shipped; CBR needs a
  rar decoder, the rest need format parsers.
- **Per-library permissions** — the user model is currently binary
  (admin / user); a shared instance with mixed audiences needs
  finer-grained library ACLs.
- **Audit logs + parental controls** — `audit_logs` and
  `user_content_restrictions` tables are reserved.
- **More device drivers** — Kobo cloud sync, KOReader progress sync,
  Hardcover.app, Komga import. The `DeviceDriver` interface is in
  place; each new target is one Go file.
- **SendGrid (or other API-based) email transport** — SMTP is live;
  swap is a `Sender` implementation.

## License

[AGPL-3.0-or-later](LICENSE). Hosted forks must publish their source
under the same license.
