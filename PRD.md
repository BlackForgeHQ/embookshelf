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

| Device/Service | Sync Capabilities |
|----------------|-------------------|
| **Kobo** | Full library sync, reading progress, shelves, thumbnails |
| **KOReader** | Reading progress sync |
| **Hardcover.app** | Reading status, wishlist sync |
| **OPDS** | Library access from any OPDS-compatible e-reader app (Moon+ Reader, FBReader, etc.) |

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
| **Local JWT** | Username/password with JWT access + refresh tokens |
| **OIDC** | External identity providers (Authentik, PocketID, Keycloak, etc.) with group-to-role mapping |
| **Remote/Forward Auth** | Reverse proxy auth headers (Authentik Forward Auth, Authelia, Caddy) |

- Backchannel logout support for OIDC
- OIDC group-to-role mapping (auto-assign admin/user based on IdP groups)
- CSRF protection
- Book-level access control (`@CheckBookAccess`)

---

## 6. Deployment Requirements

### 6.1 Supported Deployment Methods

- **Docker Compose** (primary, recommended)
- **Kubernetes** (Helm chart provided)
- **Podman**

### 6.2 System Requirements

- Docker + Docker Compose
- MariaDB 11.4+
- Minimum 512MB RAM (recommended 1GB+)
- Port 6060 (configurable)

### 6.3 Storage Modes

| Mode | Behavior |
|------|----------|
| `LOCAL` | Full read/write — metadata writing, file renaming, file organization |
| `NETWORK` | Read-only — metadata stored in DB only, files never modified. For NAS/NFS/SMB mounts |

### 6.4 Volume Mounts

| Path | Purpose |
|------|---------|
| `/app/data` | Configuration, cache, database backups |
| `/books` | Book library storage |
| `/bookdrop` | Watched folder for file imports |

---

## 7. Internationalization

- Full i18n support via Weblate
- Community-driven translations
- Frontend uses Transloco for runtime language switching

---

## 8. Supported File Formats

| Category | Formats |
|----------|---------|
| **Ebooks** | PDF, EPUB, MOBI, AZW3, FB2 |
| **Comics** | CBZ, CBR, CB7 (ZIP, RAR, 7z archives) |
| **Audiobooks** | MP3, M4A, M4B (with embedded metadata extraction) |

---

## 9. Non-Functional Requirements

- **Self-contained** — No external service dependencies for core functionality (metadata providers are optional enrichment)
- **Privacy-first** — All data stays on the user's server; optional telemetry is opt-out
- **Performance** — Virtual threads (Java 25) for high-concurrency I/O; HikariCP connection pooling; Caffeine caching
- **Responsive UI** — Mobile and desktop layouts
- **Real-time updates** — WebSocket notifications for background task progress
- **Health monitoring** — `/api/v1/healthcheck` endpoint for orchestrators

---

## 10. Out of Scope

- Cloud-hosted SaaS offering
- DRM management or enforcement
- Format conversion (e.g., EPUB to PDF)
- Social features beyond multi-user sharing (no public profiles, no activity feeds)
- Mobile native apps (web-only, responsive)
