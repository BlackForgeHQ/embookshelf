# embookshelf — Domain glossary

Terms with a specific meaning inside the codebase. When a term here conflicts with how a teammate is using a word, the term here wins — push back and align before changing the code.

This file complements `docs/ARCHITECTURE.md` (technical layout) and `docs/spec/` (feature specs); it records the **terms** — what a thing is called and what the term means. Use these names exactly when proposing refactors or recording decisions.

---

## Users & identity

### Identity

A credential link between an embookshelf user and an OIDC provider account. Stored in `user_identities` (one row per linked provider). Distinct from "session" (a logged-in browser) and "account" (the human-facing user record in `users`).

### OIDC provider

An OIDC identity provider slug used in URLs and DB rows. Three slugs: `google`, `github`, `generic`. The `generic` slug points at whatever issuer the admin configured (Authelia, Keycloak, Okta, etc.). A provider is "enabled" when its admin row in `app_settings` has `Enabled=true`. **Distinct** from the metadata Provider used by the enrichment subsystem (see below). **Distinct** from Forward-auth (header-injected identity from a reverse proxy) — both materialize as rows in `user_identities`, but only OIDC slugs run a redirect/exchange flow.

### External identity provider

Umbrella term for any source of `user_identities` rows: the three OIDC providers above plus the reverse-proxy `proxy` slug. Used when a statement applies to both auth surfaces (Lockout guard, Linking, Provisioning). Prefer the specific term (OIDC provider, Forward-auth) when you mean only one.

### Linking

The act of attaching an identity to a user. Two ways it happens:

1. **Panel-driven link**: signed-in user clicks Connect Google in the account panel. Initiated under `/api/v1/account/oidc/link/:slug`. Always explicit, always authed.
2. **Auto-link** (see below): performed by the login callback.

### Auto-link

Login-time linking gated by the admin flag `AllowLocalAccountLinking`. When the OIDC callback returns an identity that doesn't match any row but the email claim matches a local user, the callback attaches the identity to that user instead of rejecting the login. Relies on the IdP-verified email — GitHub explicitly rejects unverified emails (`service/oidc.go:486`); Google and generic OIDC trust the `email_verified` claim.

### Lockout guard

The invariant enforced on every unlink: a user must end the operation with at least one usable credential — either a password or a remaining linked identity. A user with no password and exactly one linked identity must set a password before that identity can be removed. Enforced at the SQL layer in a single statement so the check and the delete are race-free.

### Provisioning

Admin policy controlling whether an unknown External identity creates a new user. Three knobs in `oidc_auto_provision_details`: `EnableAutoProvisioning`, `RequireAdminApproval`, `DefaultRole`. Off by default after the first user; the first External-identity login on an empty instance (OIDC callback or first trusted-proxy header hit) is always admitted as admin to avoid an unrecoverable state. Same row, same knobs, both auth paths — the table name is historical, not OIDC-only.

### Force-only mode

Admin toggle `oidc_force_only_mode` that hides the local-password form on the login page when exactly one OIDC provider is enabled. Does not affect API auth or existing local users. Distinct from `forward_auth.hideLocalLogin` — that one hides the form whenever forward-auth is enabled, regardless of OIDC provider count.

### Forward-auth

Reverse-proxy header authentication: the upstream proxy (Authelia, oauth2-proxy, Traefik forwardAuth, Cloudflare Access) terminates SSO and injects identity headers (`Remote-User`, `Remote-Email`, …) on every proxied request. embookshelf trusts those headers and attaches the matching `users` row to the request context — no session cookie issued, no redirect dance. Materializes as a `user_identities` row with slug `proxy` and `subject = Remote-User` (the identity key) plus `email = Remote-Email` (auto-link helper). Per-request stateless: proxy logout propagates immediately because no cookie outlives the proxy session. Configured via `app_settings.FORWARD_AUTH`. Only ever applied to browser/SPA routes — OPDS endpoints stay on HTTP Basic regardless. ADR-0022.

### Trusted proxy CIDR

The `app_settings.FORWARD_AUTH.trustedProxyCIDRs` allowlist. The forward-auth middleware reads identity headers **only** if `c.Request.RemoteAddr` (the immediate TCP peer) falls inside one of these CIDRs. `X-Forwarded-For` and `X-Real-IP` are ignored — trusting them would let any caller spoof the source address and forge identity. Boot refuses to start when forward-auth is enabled and the list is empty (mirrors Cipher's bad-key boot semantics, ADR-0010). Implies a deployment shape where the forward-auth proxy is the immediate upstream of embookshelf; sandwiching another LB between them is unsupported.

### Proxy identity

A `user_identities` row with `provider = 'proxy'`, created automatically on the first trusted forward-auth hit (via auto-link by email or Provisioning). Surfaced read-only in the account panel ("Reverse proxy: <Remote-Email> — managed by your administrator") — no Connect/Disconnect buttons, because deleting the row would just be re-created on the next request. Counts as a credential for Lockout guard purposes.

### Unshelved

A virtual view of books a user has not manually placed on any **regular** shelf. System shelves `reading` and `finished` are excluded from the test (they auto-populate from progress, not curation), and smart shelves are ignored (their membership is query-time, not stored). Per-user: a book can be unshelved for one user and shelved for another. Surfaced as a fixed sidebar entry under "All Books" and as the `?unshelved=1` library filter — never a row in `shelves`. Implemented with a single `NOT EXISTS` subquery over `shelf_books`. Distinct from "All Books" (every book) and from a smart shelf (rule-driven).

Shared shelves (see below) do **not** count as curation for the viewer — the test still considers only shelves the viewer owns. A book that sits on admin's shared "Top Picks" but on no shelf of mine is still Unshelved for me.

### Shared shelf

A regular shelf that an admin has flipped public so every user sees it in their sidebar under a dedicated `SHARED` section. Stored as `shelves.is_public = true`; the same row backs every viewer (one row, many readers — admin's edits propagate). Only admins can publish, and only their own shelves (`user_id = caller.id`). Smart shelves cannot be shared — a CHECK constraint forbids `is_public = true AND is_smart = true`, because smart-shelf rules touch per-user fields (rating, progress) that don't translate cross-user.

URL form is `?shelf=public:<slug>` (and the matching `/api/v1/shelves/public:<slug>/...`); the prefix disambiguates from the per-user `(user_id, slug)` index. Slug uniqueness across public shelves is enforced by a partial unique index on `shelves(slug) WHERE is_public = true`. Mutations (add/remove book, rename, accent, un-publish, delete) are owner-only — non-owners cannot curate. Non-owners never see shared shelves in the "Add to shelf" picker, only in the sidebar.

Realtime updates fan out via a broadcast channel on `/events` (`shelf.public.updated` / `shelf.public.removed`); per-user events stay scoped. Un-publishing or deleting redirects active viewers back to `/library` with a toast — the shelf vanishes softly rather than 404-ing them mid-page. Admin role demotion auto un-publishes any public shelves the demoted user owns.

### Shelf icon

The lucide-react icon name a shelf renders with across every surface — sidebar row, library header, command palette, "Add to shelf" picker, account "My shelves" panel. Stored as `shelves.icon TEXT NOT NULL DEFAULT 'library'`; one column, single source of truth (no per-slug fallback map). Owner-curated like name and accent; for shared shelves the owner's icon propagates to every viewer through the existing `shelf.public.updated` broadcast.

System slugs (`reading`, `finished`) are not locked — icon is presentation, not behavior. The migration backfills visual continuity (`reading → book-open`, `finished → check-circle-2`, smart shelves → `sparkles`, etc.) so existing instances render unchanged after upgrade; the user is free to override.

Server-side validation is **regex only** (`^[a-z][a-z0-9-]{0,63}$`), not an allow-list. Trade-off recorded in ADR-0019: lucide ships ~1500 icons and adds new ones every release, so a Go-side enum would churn for no real safety win. A typo'd slug renders as a fallback glyph — fixable in 5s by re-picking. Distinct from `ShelfAccents` (closed palette of 8, allow-list enforced).

Picker UX: search-driven popover (sidebar inline) / inline panel (account page) over the lucide name list. Twelve suggestion glyphs are statically imported for the common case (no flash); the long tail loads via `lucide-react/dynamic`. Default for new shelves is `library` (regular) or `sparkles` (smart), set client-side at create-time.

---

## BookDrop housekeeping

### Clear processed

Housekeeping op that deletes every `bookdrop_items` row in a terminal state (`imported`, `rejected`) and best-effort sweeps any pre-approval cover bytes still on disk. The source files under `BOOKDROP_PATH` are NOT touched — the watcher will re-discover any file still on disk on its next tick. Distinct from Wipe BookDrop.

### Wipe BookDrop

Admin-only housekeeping op that recursively removes every file under `BOOKDROP_PATH` and drops orphaned `bookdrop_items` rows whose path no longer exists. Files referenced by rows in `processing` state are skipped to avoid killing live extraction. Cross-user blast radius: erases other users' pending uploads, hence the admin gate and the type-to-confirm dialog. Distinct from Clear processed (DB-only).

### Pending orphan

A storage key whose Book is no longer referenced by `files.location` (or sidecar/cover at `{folder_path}/`) and which is queued for deletion after a grace window. Materialised as a row in `pending_orphans` (library_id, key, eligible_at, reason, book_id). Created on every S3 edit-time folder rename: old keys go in with full grace; new keys from a half-failed rename go in with a short grace. A background sweeper deletes keys past `eligible_at`. Local backends do not produce pending orphans — `os.Rename` is atomic.

---

## Storage layer

### Storage

The `internal/storage.Storage` interface; backend-agnostic read/write of book bytes. Two adapters today: LocalFS and S3. See `docs/spec/storage.spec.md` §2.

### Backend

A concrete `Storage` rooted somewhere (a directory, a bucket+prefix). Identified by `storage_backends.id` in the DB.

### Resolver

`storage.Resolver`; maps a backend id (or library) to its `Storage`. Constructed at boot from `storage_backends` rows.

### Library

A logical collection of books pinned to one Backend via `libraries.backend_id` and `libraries.root`. Two creation flavors:

- `kind=local` (default) — **managed**: filesystem path is auto-derived as `${DATA_PATH}/libraries/{slug}/`; the directory is created at library-create time (`os.MkdirAll`, idempotent — pre-staged folders are re-adopted). Operator does not pick the path. Existing libraries created before this convention keep their explicit paths; only new libraries are managed.
- `kind=s3` — backend-managed: prefix `libraries/{slug}/` inside the shared S3 bucket; symmetric naming with the local layout.

Library deletion: `?purge=true` query param deletes the on-disk folder (local) or the bucket prefix (s3). Default off — files preserved on row delete.

### LibraryStore

`service.LibraryStore`; the deep seam that turns a `libraryID` into a `LibraryHandle{Library, Storage, Placer}` plus delivery glue (`BookSource`, `Open`, `Relativize`). Composes `LibraryRepo` + `Resolver` + `PlacerBuilder` behind one `For(ctx, libraryID)` method. Stateless — each call does a fresh PK lookup. Used by `BookDropService.Approve`, the file-serve handler, library scan, and files backfill. Replaces the scattered `lib, _ := libs.GetByID(); resolver.Resolve(*lib.BackendID); ...` chain that appeared at every callsite. (Bookdrop ingest still calls `Resolver` directly because it has no library_id at ingest time.)

### Source

`storage.Source`; the random-access byte view of a single object. `io.ReaderAt + io.Closer + Size() int64`. Returned by `Storage.Open(ctx, key)`. **Distinct** from `storage.Get` (sequential streaming via `io.ReadCloser`) — Source is for callers that need to seek (zip directories, PDF XREF, MP4 atoms). **Distinct** from `service.BookSource` — that's a delivery decision, not a byte primitive.

### Sidecar

Portable per-book metadata file at the LeafBook folder root. Two flavors:

- `metadata.opf` (Calibre) — XML, **read-only** for compat.
- `metadata.embookshelf.json` (native) — JSON, **read+write**. One file per Book, lives at `{library_root}/{Author}/{Title}/metadata.embookshelf.json` (per ADR-0003). Pre-existing per-file `<basename>.embookshelf.json` files are read once on re-scan but never written; new writes always emit the folder-root file.

The earlier `.embookshelf.toml` sidecar is **dropped** — no read, no write, no migration.

The JSON sidecar is **spillover-only on local-backed libraries**: holds fields the book's file format couldn't carry natively. EPUB OPF takes everything (including cover bytes) → JSON sidecar usually empty. PDF `/Info` takes only Title/Author/Description/Tags → JSON sidecar holds Subtitle, Language, Publisher, ISBN, Series, SeriesIndex, Genres, etc.

**Full-mirror sidecar** whenever the in-file write step is skipped, for any reason:

- Format has no in-file write target (CBZ/CBR/CB7, MOBI, AZW3, FB2, audio in Phase 1).
- Library is **S3-backed** (`libraries.backend_id IS NOT NULL`) — Phase 1 skips in-file write to avoid Get+Put per edit.
- In-file write attempted and failed (failure fallback so edit survives).

Single rule: `inFileWritten == false → sidecar = full mirror`. `inFileWritten == true → sidecar = spillover for that format`. Triggered by **manual edit** or **apply-enrichment** only — auto-enrichment, scan re-ingest, and approve do not write file/sidecar.

Read path (ingest): file embedded → OPF (if present) → JSON, each layer overlays the previous, **lock-aware** (per-field `*_locked` flags shield DB values from re-extract). Write path (user edits): DB (canonical) → JSON sidecar → file embedded (EPUB cover+text rezip; PDF `/Info` text only; audio deferred). Each step is sequenced and atomic; scan skips re-extract when `files.content_hash` matches our recorded write (hash-stamp guard).

### BookDrop

Pre-approval staging area; files land here before being approved into a Library. Each file becomes a row in `bookdrop_items` with extracted metadata + cover. Approving creates the `books` row and the `files` row.

### Files row

One entry in the `files` table per physical artifact tied to a `book_id`. Carries `location` (relative to `library.root`), `size`, `mtime`, `etag`, `content_hash` (sha256), `format`.

### Tier

Hot / warm / cold; assigned by `internal/tagging.Classify` based on last-read time. Drives S3 lifecycle transitions via `tag:tier=...` on the object.

---

## Content identity

### Content hash

sha256 of a book file's bytes. Authoritative identity in the new schema.

### ETag

Opaque change token from a backend (S3 returns one; LocalFS leaves it empty). Use to *detect* whether a single object changed since last observation. **Never** use to compare two objects for equality — multipart uploads make ETag input-dependent.

---

## Workers

### Library scan

`task.LibraryScan`; on-demand walk-then-diff over a Library, scoped to drift detection only (ADR-0018). Acts on two diff buckets: **New** entries get a hash + `Files.GetByContentHash` lookup — a same-library hash match means external rename, update the row's `location`; no match means ignore (no `books` row materialised, scan is never an ingest path). **Missing** entries are soft-flagged via `MarkMissing` for the 24h purge sweeper. Changed and Unchanged are no-ops apart from clearing `missing_since` on reappearance. No periodic timer — admin triggers the worker explicitly. Distinct from earlier scan-as-ingest behavior (ADR-0004, superseded).

### Walker

`internal/scan.Walk`; streams `WalkEntry{Location, Size, Mtime, ETag}` from a Storage.

### Differ

`internal/scan.Diff`; pure function classifying walk × DB rows into `Changeset{Unchanged, Changed, New, Missing}`.

### Relocate by hash

`scan.RelocateByHash` (or inline equivalent in `task.LibraryScan`); for a New walk entry, hashes the bytes and queries `Files.GetByContentHash` in the same library. On hit, updates the existing `files.location` to the new path — the rename safety net under ADR-0018. On miss, returns without side effect; scan is never an ingest path.

### Drainer

`task.Drain[T]`; the loop shape used by boot-time backfills that read pending rows from a predicate query, do per-item work that may fail per item, and exit when the predicate is empty or no item in a batch made progress. Owns logging + the in-run skip set so closures stay focused on the work itself. Used by Files backfill (sha256 fill) and Covers backfill (legacy → hash-keyed). Distinct from a schema-bootstrap backfill (`migrator.BackfillStorageV2`), which runs once after `migrate.Up`, sentinel-gated, DB-only.

### Files backfill

`task.RunFilesBackfill`; one-shot at-boot worker that fills `files.content_hash` for rows backfilled by the migration with NULL hashes.

### Covers backfill

`task.RunCoversBackfill`; one-shot at-boot worker that re-keys legacy book-id-keyed cover files to the hash-keyed layout.

### Missing purge

`task.LoopMissingPurge`; hourly sweeper that deletes `files` rows whose `missing_since` is older than 24h.

---

## Service layer

### Approve

`BookDropService.Approve`; the orchestration that turns a `bookdrop_items` row into a `books` row + `files` row + cover. Five side effects in sequence.

### Bookdrop ingest

The worker pipeline that takes a staged file path, computes its hash, dispatches a Processor by extension, extracts metadata + cover, persists to the bookdrop row.

### Processor

`fileproc.Processor`; per-format metadata extractor. `Extract(ctx, src Source) (Metadata, error)`. One implementation per format (EPUB, PDF, CBZ, MP3, M4B).

### BookSource

`service.BookSource`; a *delivery decision* for the file-serve handler: `{Kind: "local", Path}` (stream via `c.File()`) or `{Kind: "presign", URL, TTL}` (302 redirect). Built by `LibraryHandle.BookSource(ctx, book)`. **Distinct from `storage.Source`** — that's a byte-access primitive; this is a routing answer.

### Placer

`service.Placer`; the seam Approve uses to materialize a bookdrop file at its final library location. Two adapters: `LocalPlacer` (filesystem rename + collision-suffix under the library root) and `BackendPlacer` (stream-upload to a `Storage` then drop the local source). Returns `PlaceResult{Location, Size, Mtime}` — the values the `files` row needs. The `PlacerBuilder` factory injected at boot picks the adapter from `Library.BackendID`. Approve never branches on local-vs-S3.

### MetadataWriter

`service.MetadataWriter`; the **edit-side write pipeline** module. Owns the `DB → JSON sidecar → file embedded → folder rename` sequence for user-driven edits only. Three triggers in scope: `manual_edit`, `apply_enrichment`, `auto_enrichment`. The other ADR-0001 §3 rows (`bookdrop approve`, `library scan re-ingest`) deliberately route around this module — for those, the file *is* the source, so a writer that rewrites the file would loop. Single entry point: `Write(ctx, book, trigger) (Outcome, error)`. Decision lives in `decideEffects` (pure); execution is a flat orchestration of four private steps (DB, sidecar, in-file embed, folder rename). Stamps `files.content_hash` after a successful in-file write so the next library scan recognises its own write and skips re-extract.

### Effects

`service.Effects{DB, InFileFormat, Sidecar, FolderRename}`; the plan returned by `decideEffects(trigger, handle, format, fieldChanges)`. Encodes the trigger × backend × field-changed matrix from ADR-0001 §3 + ADR-0003 §6. `FolderRename = true` iff trigger ∈ {manual_edit, apply_enrichment} AND backend == local AND `Author` or `Title` changed. Pure function — no I/O — so combinations are scenario-tested without standing up storage or repos.

### Outcome

`service.Outcome{InFileWritten, SidecarMode, FolderRenamed}`; what the executor returns alongside `error`. `SidecarMode` is decided post-hoc from `InFileWritten` per ADR-0001's `inFileWritten == false → sidecar = full mirror` rule. `FolderRenamed` is set when ADR-0003 §6 fired. Outcome is consumed by tests, optionally by SSE telemetry/audit; callers that don't care can discard.

---

## Library layout (ADR-0003)

### Folder layout

Every Book lives in its own folder named `{Author}/{Title}/{filename}` under the library root. Sentinels: empty Author → `Unknown Author`, empty Title → `Untitled`. Path segments sanitized by `internal/layout/sanitize.go` (replace `/ \ : * ? " < > |`, NFC-normalize, cap 200 bytes). Multi-author authors stay one string. Collisions resolved by `uniqueDestination` ` (2)`, ` (3)` suffix on the title segment.

### LeafBook

A directory under the library root that holds the files for one Book — all `files` rows tied to a single `book_id` plus the JSON sidecar at folder root. Materialized by `BookDropService.Approve` via the Placer at the `{Author}/{Title}/` path. Distinct from Container.

### Container

A directory under the library root that holds only subdirectories — the `Author/` layer in `Author/Title/file.epub`. Created implicitly by the Placer when an Author folder doesn't yet exist; no row anywhere in the DB.

### Primary file / primary format

The highest-priority supported file inside a LeafBook (EPUB > PDF > CBZ > AZW3 > MOBI > FB2 > M4B > MP3). Drives `books.format` (single string, denormalized cache) and is the source for in-file metadata writes. UI derives the full format set from `files` rows when needed.

### Sentinel folder

`Unknown Author` and `Untitled` are reserved literal strings used when the corresponding Book fields are empty. Predictable depth: every Book sits at depth 2 under the library root.

### Folder rename

`os.Rename(oldDir, newDir)` invoked by `MetadataWriter` after the DB → sidecar → in-file pipeline succeeds, when `Author` or `Title` change via a `manual_edit` or `apply_enrichment` trigger on a local-backed library. S3 backends never rename. `auto_enrichment` and scan re-extract never rename — DB drifts from disk, accepted.

### Lazy layout migration

Existing libraries keep their current shape. Place-time uses the new layout for new approves; edit-time rename lazily moves actively-curated Books into the new shape. No boot-time relocation. Inactive flat-layout Books stay flat indefinitely. Mirrors ADR-0002's "no automatic move" principle. Mixed-layout libraries are expected during the transition tail; all reads use `files.location` + `books.folder_path` from DB, not disk-walk inference.

### Extract

`extractor.Extract(ctx, store, src, format, key) ExtractResult`; shared extraction primitive returning format metadata + cover bytes + audio fields + sidecar overlay. Sole consumer is the bookdrop ingest path — under ADR-0018, scan never extracts. Lives in `internal/extractor/` to avoid the import cycle with `internal/ingest/`'s bookdrop watcher (which imports `queue`, which imports `task`).

---

## Metadata enrichment (ADRs 0008–0013)

### Metadata provider

`provider.Provider`; strategy adapter for one external metadata source (Google Books, Open Library, Hardcover, Goodreads, Amazon, DuckDuckGo). Single method: `Search(ctx, Query) ([]Match, error)`. ID is a `provider.Source` constant declared in the Catalog. **Distinct** from the OIDC provider used for authentication.

### Catalog

`provider.Catalog`; literal `[]Info` in `internal/provider/catalog.go`. Single source of truth for which provider IDs the binary knows + their default rate limits + `DefaultEnabled` flags. Walked by `Build()`, the settings handler DTO, and the `provider_settings` seed. New provider = code + rebuild (ADR-0008).

### Match

`provider.Match`; normalized hit from any provider. Includes Title, Authors, ISBN, Description, Cover URL, Series, Year, Categories, Language, plus a provider-scored `Confidence` (0–100 heuristic; higher = better guess). Consumers merge by Confidence.

### Query

`provider.Query{Title, Author, ISBN}`; search input. Empty fields ignored. ISBN-only is the identity-match shape used by `LookupByISBN`.

### Resilient client

`provider.NewResilientClient(name, rps, burst)`; the shared `*http.Client` decorator stack used by every provider adapter. Three layers, outermost first: token-bucket rate limiter (`x/time/rate`) → circuit breaker (`sony/gobreaker/v2`, trips at 60% failure over ≥5 requests, 30s recovery) → retryable transport (`hashicorp/go-retryablehttp`, 3 retries with backoff, treats 429/5xx as breaker failures). One client per provider, named for breaker telemetry.

### EnrichmentService

`service.EnrichmentService`; the fan-out coordinator. Owns `providers []provider.Provider`, the in-process result cache (5min TTL, 512-entry cap), the cover store + book repo handles, and the `Cipher` for password-field secrets. Three external entry points: `Search` (batch fan-out + merge by Confidence), `SearchStream` (SSE-friendly per-provider channel), `LookupByISBN` (priority chain, first non-empty wins).

### Prospective metadata

Candidate matches presented to the user for review before persisting. Streamed over SSE via `EnrichStream` so fast providers render in <1s while slow scrapers fill in (ADR-0009). Distinct from auto-enrich's headless gap-fill (ADR-0012) and from `LookupByISBN`'s short-circuit chain (ADR-0011).

### ISBN chain

`LookupByISBN`'s ordered walk through enabled providers by `provider_settings.priority` ASC (ranked first, unranked fall back to catalog order). First provider returning ≥1 match terminates the walk; within that batch, max-Confidence match wins. Used by `POST /api/books/metadata/isbn-lookup` and by `AutoEnrich` when the book has an ISBN. ADR-0011.

### Auto-enrich

`EnrichmentService.AutoEnrich(ctx, book)`; headless background gap-fill triggered by bookdrop approve. Empty-only policy: clones `book.Locks` in-memory and sets every non-empty field as locked for the duration of the apply, leaving DB locks unchanged (ADR-0012). Prefers ISBN chain; falls back to `Search` with Confidence ≥ 70 threshold. Triggers `TriggerAutoEnrichment` — ADR-0001 §3 keeps this off the in-file embedded write path.

### Provider settings row

`provider_settings(id, enabled, config, priority, last_success_at, last_error_at, last_error)`. One row per Catalog entry. `config` is JSONB; `password`-kind fields (Hardcover token, Amazon cookie) are AES-256-GCM encrypted in place; non-secret fields (region, language, enabled flags) stay plaintext for `psql` legibility (ADR-0010). `priority` is a single nullable int; ranked-first then unranked-in-catalog-order.

### Cipher

`crypto.Cipher` interface with two implementations: `AESGCM` (prod, KEK from `EMBOOKSHELF_SECRET_KEY` base64-decoded 32 bytes) and `Noop` (dev fallback when env unset). Boot semantics are asymmetric: invalid key refuses startup; unset key warns and falls back to Noop. ADR-0010.

### Configurable / SchemaProvider

Optional interfaces a `Provider` may implement. `Configurable.Configure(rawJSON)` accepts plaintext config from the settings UI; `SchemaProvider.ConfigSchema()` returns `[]ConfigField` describing inputs the admin UI should render (text / password / select / textarea). Password-kind fields drive the per-field encryption walk in `EnrichmentService.transformConfigFields`.

### Graceful degrade

Fan-out policy: per-provider error → log + write to health table + return nil from goroutine; siblings unaffected. `errgroup` is used purely for ctx propagation (client disconnect cancels all in-flight HTTP calls), **not** for error short-circuit. `_ = g.Wait()` is deliberate. ADR-0013. Don't "fix" it.

### Provider health

`provider_settings.last_success_at` / `last_error_at` / `last_error`; updated fire-and-forget via detached goroutines with their own 3s ctx so request-side cancellation doesn't lose the write. Surfaced in admin Settings as the universal "is this provider working" signal — the only place per-provider failure is visible regardless of which entry point triggered the call.

### Apply match

`EnrichmentService.ApplyMatch(ctx, book, match, opts, trigger)`; the lock-honoring merge. For each unlocked field, overwrite from match if non-empty; categories union when `opts.MergeCategories`; cover imported when `opts.ApplyCover` and unlocked. Routes through `MetadataWriter` if wired (so DB → sidecar → file pipeline runs per ADR-0001), else direct to repo. Single codepath shared by manual UI apply and auto-enrich.

---

## Email delivery (ADRs 0020–0021)

### Email subsystem

The set of features that send transactional mail: password reset, admin invite, Send-to-Kindle. Gated by a single feature flag in `app_settings.EMAIL.enabled`. When off, the password-reset link hides on the login page, the Send-to-Kindle button disables, the admin invite UI redirects to email settings, and the affected APIs return 503 `{"error":{"code":"EMAIL_DISABLED"}}`. ADR-0020.

### Sender

`email.Sender`; the transport seam. `Send(ctx, Message) error`. One real implementation: `SMTPSender` built on `github.com/wneessen/go-mail`. A `NoopSender` is wired when the email subsystem is disabled so domain code never branches on enabled-ness. **No** provider catalog mirroring `internal/provider` — the SMTP transport covers Brevo, Mailjet, SES (SMTP endpoint), Postmark, Mailgun, Gmail, and self-hosted Postfix without a discriminator. Adding a second adapter (Resend HTTP) is a localised change behind the same interface, deferred until a real user is blocked. ADR-0020.

### Notifier

`service.Notifier`; orchestration above `Sender`. Knows about reset tokens, invite tokens, the Send-to-Kindle attachment build (via `LibraryHandle`), and the public URL used to render absolute links in templates. Three entry points: `SendPasswordReset(ctx, user, token)`, `SendAdminInvite(ctx, invite, invitedBy)`, `SendToKindle(ctx, book, user, source)`. Templates live in `internal/email/templates/` (HTML + plaintext, embedded via `//go:embed`, parsed once at boot). Distinct from `Sender` — Sender does bytes; Notifier does domain.

### SMTP config

`app_settings.EMAIL` JSON: `{enabled, smtp:{host,port,username,password,tls}, from:{address,name}, publicUrl}`. Server-wide outbound — one config row, used by every email send (auth + Kindle). `smtp.password` is AES-GCM encrypted in place per ADR-0010; the same per-field encryption walk that operates on `provider_settings.config` is generalised to `app_settings`. `tls` enum: `none | starttls | tls` for ports 25 / 587 / 465 respectively. Distinct from per-user SMTP — there is none; users supply only a kindle email target.

### Public URL

`EMAIL.publicUrl`; the absolute base URL embedded in email links (`{publicUrl}/reset?token=...`, `{publicUrl}/accept-invite?token=...`). Stored in `app_settings`, **not** an env var, because background workers (the Send-to-Kindle queue job, future async invite re-sends) lack request context and can't infer the host from `c.Request`. Validated at save time: parses as URL, scheme `http` or `https`, no trailing slash. Inferring from `Host` / `X-Forwarded-Host` is rejected — header spoofing in a reset email is a phish vector.

### Reset token

A 32-byte random value (`crypto/rand`, base64url-encoded, ~43 chars) issued on `POST /api/v1/auth/password-reset/request`. Stored as `sha256(token)` in `password_reset_tokens(token_hash, user_id, created_at, expires_at, used_at)`; the plaintext exists only in the email and the URL. Single-use (consumption sets `used_at`), 1h expiry, one row per request. The request endpoint always returns 202 regardless of whether the email exists — identical response shape prevents account enumeration. Rate-limited per-user (1 / 5min) and per-IP (10 / hour).

### Invite token

The admin-invite analogue of a reset token. Stored in `user_invites(token_hash, email, role, invited_by, created_at, expires_at, accepted_at, user_id)`; carries the invitee's email, target role (`user` | `admin`), and the inviting admin's id. Same crypto shape (32-byte random, sha256 at rest, single-use), but a 7-day expiry to survive a weekend onboarding gap. Acceptance via `POST /api/v1/auth/invites/accept {token, password}` creates the user, marks the invite consumed, and returns a session. Distinct table from reset because the lifecycle and fields diverge — an invite has no user yet at creation, a reset always does.

### Send-to-Kindle

Per-book action that emails the primary file as an attachment to the user's `kindle_email`. Eligible formats: **EPUB and PDF only** (intersection of embookshelf's supported set and Amazon's current ingestion list). 50 MB hard cap, no in-binary conversion, no MOBI/AZW3/CBZ delivery. The button is disabled with a tooltip on every other format. Async via the existing `queue.Client` (`EnqueueSendToKindle`); the worker does `LibraryHandle.For` → `Storage.Open` → re-check format/size → `Sender.Send` → SSE event (`kindle.sent` / `kindle.failed`). Subject = book title, attachment filename = `{Title} - {Author}.{ext}` sanitised, body empty. ADR-0021. **Distinct** from generic email — has attachments, no body templating, format-gated.

### Kindle email

`users.kindle_email TEXT`; the per-user destination address for Send-to-Kindle. Validated by regex `^[a-z0-9._-]+@kindle\.com$` (cheap shape check; Amazon-side bounces handle non-Kindle addresses anyway). Empty means the user has not configured Send-to-Kindle — the button surfaces "Set Kindle email" linking to the account panel instead. Editable in the account panel only. Distinct from `EMAIL.from.address` (server-wide outbound sender) — Amazon requires the user to add the From address to their "approved senders" list once.

### Eligible format

The set `{epub, pdf}`; the formats Send-to-Kindle accepts. Defined as a constant in `internal/email/` and checked at three points: the UI button (reads `book.format`), the HTTP handler (rejects with 415 `FORMAT_NOT_SUPPORTED`), and the queue worker (re-validates after `LibraryHandle.For` to defend against re-import races). Distinct from `books.format` (primary file format cache) — eligible format is a delivery filter, not a property of the book.

---

## Vocabulary discipline

Avoid these substitutes — they drift the meaning:

- "component" / "service" / "package" → say **module** when discussing depth/seam, **adapter** when discussing implementations of an interface.
- "API" / "signature" → say **interface** (includes invariants, error modes, ordering, not just types).
- "boundary" → say **seam** (boundary is overloaded with DDD bounded contexts).
- "Storage source" / "BookSource" used interchangeably → no. `storage.Source` = bytes; `service.BookSource` = delivery target.
- "Provider" alone is ambiguous — say **OIDC provider** (auth, redirect flow) or **metadata provider** (enrichment) or **Forward-auth** (proxy header trust). For statements that span OIDC + forward-auth, say **External identity provider**.
- "Trusted proxy" alone is ambiguous — say **Trusted proxy CIDR** (the IP allowlist) when discussing the gate; "the upstream proxy" when discussing the deployment.
- "Email provider" — **don't use**. There is no email-provider abstraction. Say **Sender** (transport seam) or **SMTP config** (the `app_settings.EMAIL` row). ADR-0020.

When proposing a deepening, use the architecture vocabulary: **module**, **interface**, **implementation**, **depth**, **seam**, **adapter**, **leverage**, **locality**, plus **deletion test** and **one-adapter-is-hypothetical-two-is-real**.

ADRs live under `docs/adr/NNNN-title.md`. Format: see `~/.claude/skills/grill-with-docs/ADR-FORMAT.md`.
