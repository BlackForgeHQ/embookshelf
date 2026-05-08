# Embookshelf - Product Requirements Document

## 1. Product Overview

**Embookshelf** is a self-hosted, multi-user digital library management and reading platform. It enables users to organize, read, annotate, and share their complete book collection — including ebooks, comics, and audiobooks — without relying on third-party services.

---

## 2. Problem Statement

Readers with large digital book collections face fragmented tooling: separate apps for PDFs, EPUBs, comics, and audiobooks; no unified metadata management; no cross-device sync; and reliance on third-party cloud services that may change terms, shut down, or restrict access. Existing self-hosted alternatives often lack multi-user support, built-in readers, or device sync capabilities.

---

## 3. Target Users

| Persona | Description |
|---------|-------------|
| **Self-hosters** | Privacy-conscious users running home servers or NAS setups who want full control over their data |
| **Book collectors** | Users with large ebook/comic/audiobook libraries needing organization and metadata enrichment |
| **Families / Small groups** | Households sharing a single instance with per-user shelves, progress, and preferences |
| **E-reader users** | Kobo/KOReader owners who want seamless library-to-device sync |

---

## 4. Core Features

### 4.1 Library Management

- **Multi-library support** — Create multiple libraries with separate filesystem paths
- **Smart Shelves** — Custom shelves for manual book organization
- **Magic Shelves** — Rule-based dynamic shelves that auto-populate based on metadata criteria (author, category, rating, format, etc.)
- **Full-text search** — Search across titles, authors, descriptions, and categories
- **Sorting and filtering** — Sort by title, author, date added, rating, progress; filter by format, shelf, category
- **Duplicate detection** — Identify and manage duplicate books in the library

### 4.2 Metadata Enrichment

- **Automatic metadata fetching** — Pull covers, descriptions, reviews, ratings, series info from:
  - Google Books API
  - Open Library API
  - Amazon (covers and metadata)
- **Manual metadata editing** — Full control over title, author, description, categories, series, ratings
- **Cover management** — Auto-fetch, manual upload, or programmatic cover generation
- **Sidecar file support** — Read/write metadata from sidecar files alongside book files
- **Comic metadata** — Extract metadata from ComicInfo.xml (characters, creators, story arcs)

### 4.3 Built-in Readers

| Format | Reader | Key Features |
|--------|--------|--------------|
| **PDF** | PDF.js-based | Annotations, highlights, bookmarks, per-user preferences |
| **EPUB** | Reflowable reader | Custom fonts, themes, reading progress |
| **CBX/CBR/CBZ** | Comic reader | Page navigation, zoom, manga mode |
| **Audiobooks** | Audio player | Chapter navigation, playback speed, progress tracking |

- All readers track per-user reading progress
- Reading session tracking with time-spent analytics
- Custom font uploads for reader personalization

### 4.4 Device Sync

Two flavours: **pull-based** (the reader fetches the catalog) and
**push-based** (embookshelf sends a book to a paired device).

Users add a device once from **Settings → Device sync**. Each kind has a
driver that owns its pairing flow and file-transfer protocol; a single
`Send to device` dropdown on the book detail page uses whichever drivers
are registered.

| Device/Service | Direction | Status | Sync Capabilities |
|----------------|-----------|--------|-------------------|
| **OPDS 1.2** | Pull | **Live** | Library access from any OPDS-compatible e-reader app (KOReader, Moon+ Reader, FBReader, Aldiko, Marvin). HTTP Basic Auth, OpenSearch, per-book acquisition links. |
| **reMarkable Paper Pro** (RM1/RM2/Paper Pro share a driver) | Push | **Live** | One-time-code pairing via `my.remarkable.com/device/desktop/connect`; one-click push of EPUB/PDF from the book page to the device's cloud inbox. Per-push `last_sent_at` / `last_error` surfaces on the device card. |
| **Kobo** | Pull | Planned | Full library sync, reading progress, shelves, thumbnails. |
| **KOReader** | Push | Planned | Reading-progress sync via KOReader's companion protocol. |
| **Hardcover.app** | Push | Planned | Reading-status + wishlist sync. |
| **Kindle (Send-to-Kindle)** | Push | Planned | Email delivery, gated on SMTP transport (§4.7). |

#### How pairing works (reMarkable Paper Pro)

1. User opens Settings → Device sync → **Add device** → **reMarkable Paper Pro**.
2. UI links out to `https://my.remarkable.com/device/desktop/connect`.
   The user signs in and copies the 8-character one-time code.
3. User pastes the code; the server exchanges it for a long-lived
   device token and stores the token (never exposed back to the UI).
4. The device now appears under "Registered devices" with a green dot.

#### How sending works

- Each push mints a short-lived user token from the stored device
  token, then uploads the book file (EPUB or PDF) to the reMarkable
  cloud. The tablet syncs on its next connection.
- Failures record `last_error` on the device row so the user sees the
  reason without opening server logs.
- No background queue — a push is a synchronous API call that returns
  `202 Accepted` only after the upload completes.

#### Driver-pluggable

Adding a new kind (Kindle, Boox, PocketBook, …) is one Go file
implementing `DeviceDriver.Pair` + `DeviceDriver.Send`, registered once
in `main.go`. No migration or schema change is needed — `user_devices`
stores per-driver config as JSONB.

### 4.5 BookDrop (Import Pipeline)

- **Watched folder** — Drop files into `/bookdrop` directory
- **Auto-detection** — Automatically detects new files
- **Metadata extraction** — Extracts embedded metadata from files
- **Enrichment** — Fetches metadata from external providers
- **Review queue** — Users review and approve imports before adding to library
- **Batch import** — Process multiple files at once

### 4.6 Multi-User

- Per-user shelves, reading progress, and preferences
- Role-based access control (admin / user)
- Per-user reader preferences (PDF, EPUB, comic viewer settings)
- Content restrictions (parental controls)
- User-specific book reviews and notes

### 4.7 Sharing and Distribution

- **Email delivery** — Send books to Kindle, Kobo, or any email address
- **Configurable email providers** — SMTP, SendGrid, or other providers
- **OPDS feeds** — Expose library via OPDS protocol for e-reader apps
- **Komga integration** — Import/sync with Komga comic library

### 4.8 Notes and Annotations

- **Reading notes** — Create and organize notes per book
- **PDF annotations** — Highlights and annotations stored per user
- **Notebook view** — Centralized view of all reading notes across books

### 4.9 Statistics

- Library statistics (books by format, category, author distribution)
- Reading session analytics
- Per-user reading progress overview

---

## 5. Authentication and Security

| Method | Description |
|--------|-------------|
| **Local session** | Username/password, bcrypt-hashed, server-side `sessions` table, `HttpOnly; SameSite=Lax` cookie, 7-day sliding TTL |
| **OIDC** | External identity providers (Authentik, PocketID, Keycloak, etc.) with group-to-role mapping |
| **Remote/Forward Auth** | Reverse proxy auth headers (Authentik Forward Auth, Authelia, Caddy) |
| **Basic Auth (OPDS only)** | E-reader apps authenticate against the same `users` table over HTTPS; no session is created |

- Backchannel logout support for OIDC
- OIDC group-to-role mapping (auto-assign admin/user based on IdP groups)
- CSRF protection via Origin/Referer matching on every non-safe method
- Book-level access control (planned) — single-tenant permissiveness today

---

## 6. Deployment Requirements

### 6.1 Supported Deployment Methods

- **Docker Compose** (primary, recommended)
- **Kubernetes** (Helm chart provided)
- **Podman**

### 6.2 System Requirements

- Docker + Docker Compose
- PostgreSQL 16+
- Minimum 512MB RAM (recommended 1GB+)
- Port 6060 (configurable)

### 6.3 Volume Mounts

| Path | Purpose |
|------|---------|
| `/app/data` | Configuration, cache, database backups |
| `/books` | Book library storage |
| `/bookdrop` | Watched folder for file imports |

---

## 7. Supported File Formats

| Category | Formats |
|----------|---------|
| **Ebooks** | PDF, EPUB, MOBI, AZW3, FB2 |
| **Comics** | CBZ, CBR, CB7 (ZIP, RAR, 7z archives) |
| **Audiobooks** | MP3, M4A, M4B (with embedded metadata extraction) |

---

## 8. Non-Functional Requirements

- **Self-contained** — No external service dependencies for core functionality (metadata providers are optional enrichment)
- **Privacy-first** — All data stays on the user's server; optional telemetry is opt-out
- **Performance**
  - Go runtime (lightweight goroutines, single static binary)
  - `pgx` connection pooling
  - HTTP `Cache-Control` for cover / static assets
  - In-memory cache (`ristretto`) reserved for a specific hot spot rather than blanket-applied
- **Responsive UI** — Mobile and desktop layouts
- **Real-time updates** — Server-Sent Events stream (`/events`) for background task progress
- **Health monitoring** — `/api/v1/healthcheck` endpoint for orchestrators

---

## 9. Out of Scope

- Cloud-hosted SaaS offering
- DRM management or enforcement
- Format conversion (e.g., EPUB to PDF)
- Social features beyond multi-user sharing (no public profiles, no activity feeds)
- Mobile native apps (web-only, responsive)

---

## 10. Current Delivery Status

This PRD describes the full product intent. The roadmap below tracks
what is live vs. what's still planned. See
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the technical shape.

> The mid-project refactor from Go + Templ/HTMX to Go + React SPA
> (TanStack Start) is complete. Every core feature that used to live on
> the Templ stack is live on the SPA, backed by the `/api/v1/*` JSON
> API. What's left is greenfield — features that were never built on
> either stack.

### Built — core reading experience (live end-to-end)

- **Library model** — multi-library + `library_paths` + `bookdrop_items`
  queue + filesystem scanner
- **Ingest** — EPUB + PDF metadata + cover extraction via
  `fileproc.Processor`. PDF discovery covers XMP packet (DC + Identifier
  Bag), DocInfo with hex / UTF-16BE decoders, ISBN normalisation, and a
  client-rendered page-1 cover fallback (ADR-0015).
- **Cover storage** — atomic writes under `coverstore/`, promoted from
  the bookdrop namespace to `books/` on approval, content-addressable
  SHA-256 hashing for dedup, admin cover removal endpoint, cache-busting
  `coverVersion` on URLs
- **Metadata enrichment** — Google Books + Open Library concurrent
  fan-out, confidence-sorted matches, ISBN lookup endpoint, per-field
  metadata locks, allow-listed cover import (SSRF protection baked in)
- **OPDS 1.2 catalog** at `/opds/*` (Basic Auth) — root nav, All /
  Library / Recent / Search acquisition feeds, OpenSearch description,
  per-book download + cover
- **`river` background queue** with workers for `bookdrop.ingest` +
  `library.scan`
- **Pluggable storage** — local FS + S3 backends, env-driven boot-time
  reconcile, pending-orphan tracking for S3 folder renames
- **Schema migrations** auto-applied on boot (`MIGRATE_ON_START`,
  default on)

### Built — web app (JSON API + React SPA)

- **Auth** — session cookies, bcrypt passwords, first-run signup,
  CSRF via SameSite + Origin/Referer
  (`/api/v1/auth/{login,logout,signup}`, `/api/v1/me`)
- **OIDC / SSO** — multi-provider linking against Google, GitHub, and a
  generic OIDC slug. Per-provider settings UI, callback handler, link /
  unlink with last-credential lockout guard, set-initial-password flow
  for OIDC-provisioned users, encrypted client secrets via ADR-0010
  (`/api/v1/auth/oidc/*`, `/api/v1/account/oidc/*`,
  `/api/v1/settings/oidc*`)
- **User lifecycle** — admin invites with token acceptance, password
  reset (request / verify / confirm), admin user list with role updates,
  approve / deny pending signups, account deletion, per-user Kindle
  email (`/api/v1/auth/{password-reset,invites}/*`,
  `/api/v1/settings/{invites,users}*`, `/api/v1/account/kindle-email`)
- **Libraries + shelves** — list, counts, per-user shelf CRUD with
  custom icon (regex-validated), publish toggle for public / shared
  shelves (ADR-0017), book-to-shelf toggle, "Unshelved" virtual view
  (`/api/v1/libraries`, `/api/v1/shelves`,
  `/api/v1/books/:id/shelves/:slug`)
- **Books** — list (search / sort / filter / library / shelf), detail,
  metadata PATCH, cover streaming, file streaming (path-sandboxed),
  per-user progress update (`/api/v1/books`, `/api/v1/books/:id`,
  `/api/v1/books/:id/{cover,file,progress}`)
- **Metadata enrichment flow** — `/api/v1/books/:id/enrich` (provider
  fan-out) + `/api/v1/books/:id/cover-from-url` (allow-list-protected
  cover import), wired into the metadata editor as live match cards
- **BookDrop queue** — list, upload, pre-approval cover (incl. PUT for
  client-rendered PDF page-1), approve, reject; admin-only housekeeping
  (clear processed, preview / wipe orphan files — ADR-0014)
  (`/api/v1/bookdrop/*`, `/api/v1/settings/bookdrop/*`)
- **Settings → Libraries** (admin-only) — register/remove filesystem
  roots, trigger scans (`/api/v1/settings/libraries*`)
- **Settings → Device sync** — per-user device registration and push.
  reMarkable Paper Pro driver: one-time-code pairing + EPUB/PDF push.
  Extensible via `DeviceDriver` interface
  (`/api/v1/devices`, `/api/v1/books/:id/send/:deviceId`)
- **Settings → Email** (admin-only) — SMTP transport with hot-reload on
  config change, send-test endpoint, encrypted credentials. Powers
  send-to-Kindle, password reset, and invite emails (ADR-0021).
  `503 EMAIL_DISABLED` returned by callers when transport is off
  (`/api/v1/settings/email*`, `/api/v1/books/:id/send-to-kindle`)
- **Settings → Metadata + Providers** — provider toggles + credentials
  (encrypted), default-metadata policy
  (`/api/v1/settings/{providers,metadata}*`)
- **Annotations** — highlights + notes per book, recent-across-library
  feed feeding the Notebook view
  (`/api/v1/annotations`, `/api/v1/books/:id/annotations`)
- **Statistics** — library distribution dashboard + reading-session
  analytics (heatmap, minutes-in-window) backed by real
  `reading_sessions` ticks (`/api/v1/stats`, `/api/v1/stats/reading`)
- **Realtime** — `/events` SSE stream; browser `EventSource` reuses the
  session cookie and invalidates react-query caches on each published
  event (`bookdrop.updated` today)
- **Readers**
  - EPUB — epub.js, paginated, chapter TOC, typography overrides,
    progress as `epubcfi(...)` + percent
  - PDF — pdfjs-dist with per-page lazy canvas rendering, progress as
    `page:N` + percent
  - Comics (CBZ / CBR / CB7) — server-side page index +
    on-demand page streaming
    (`/api/v1/books/:id/pages`, `/api/v1/books/:id/pages/:n`)
  - Audiobooks — chapter-aware HTML5 audio player with playback-speed
    control and per-user progress
- **React SPA shell** — auth-gated layout, typed file-based routing
  (TanStack Router), server-state cache (TanStack Query), realtime-driven
  cache invalidation, Tailwind 4 design tokens
- **Healthcheck** — `/api/v1/healthcheck`

### Planned (greenfield — not yet built on any stack)

- Remote / forward-auth proxy support (OIDC native is live)
- Additional device drivers: Kobo cloud sync, KOReader progress sync,
  Hardcover.app integration, Komga import. The `DeviceDriver` interface
  exists today; each addition is a single Go file. (reMarkable Paper Pro
  is live; Kindle ships via the email transport on the `send-to-kindle`
  endpoint.)
- MOBI / AZW3 / FB2 readers (EPUB, PDF, CBZ/CBR/CB7, audiobook are live)
- Smart / Magic shelves (rule-based dynamic — schema + visuals ready
  in the sidebar; rule engine + evaluator still to build)
- SendGrid (or other API-based) email transport — SMTP is live
- Reading streaks + per-book time-spent breakdown on top of the existing
  `reading_sessions` heatmap
- Parental controls / content restrictions
- Amazon + DuckDuckGo metadata / cover fallbacks
- Self-hosted fonts (remove Google Fonts CDN dependency)
- i18n string catalog + Weblate integration
