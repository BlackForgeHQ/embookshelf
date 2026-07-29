# embookshelf — Domain glossary

Terms with a specific meaning inside the codebase. When a term here conflicts with how a teammate is using a word, the term here wins — push back and align before changing the code.

This file complements `docs/ARCHITECTURE.md` (technical layout) and `docs/spec/` (feature specs); it records the **terms** — what a thing is called and what the term means. Use these names exactly when proposing refactors or recording decisions.

---

## Database

### SQLite import

`embookshelf import-sqlite --from <file.db>`; the one-shot migration off the retired SQLite backend (ADR-0023). Reads a SQLite library and writes it into the Postgres database named by `DATABASE_URL`, translating the two encodings that genuinely differ — JSON-text arrays become `text[]`, RFC3339 TEXT becomes `timestamptz`. Refuses a non-empty target rather than interleaving two libraries, and runs in one transaction so a failure leaves Postgres untouched.

Three classes of data deliberately do not transfer, all reported at the end rather than dropped silently: queued jobs (the SQLite polling queue and River don't share a table — re-trigger a Library scan afterwards); **orphan rows**, because SQLite runs with `PRAGMA foreign_keys` off by default so an older database can hold rows whose parent is gone and Postgres rejects them, each skipped via a per-row savepoint and counted; and **unknown tables**.

The table list is hand-maintained and ordered parent-before-child, which is what lets the copy run in one pass — but hand-maintained is a hazard when the failure mode is silent data loss. Two guards: `excludedTables` declares every table deliberately left out *with the reason*, so a future reader can tell a considered omission from a forgotten one; and a test asserts every table in the live schema is either imported or excluded, so adding a table to a migration without listing it fails CI. At runtime, any source table the build recognises as neither is reported as a warning naming it — the operator hears about data staying behind instead of getting exit code 0.

This importer is the only reason two SQLite artifacts survive: the `modernc.org/sqlite` driver registration, and the SQLite migration tree (an operator can upgrade straight from an old release, so an old source database must be migrated forward before its rows map onto the current schema). Both are marked for deletion with the importer.

---

## Instance settings

### Setting

`repo.Setting[T]`; one typed `app_settings` row. `Get` / `Set` / `SeedIfAbsent` are implemented once; a domain declares only what differs — its key, defaults, and optionally Normalize, Validate, and Secrets. Four declarations today: EMAIL, FORWARD_AUTH, the three OIDC provider rows, and the auto-provision row. A missing row is never an error: `Get` returns the declared default, and a stored row is unmarshaled *onto* that default so partial JSON keeps its defaults.

Declaring `Secrets` is how a row opts into at-rest encryption — it returns pointers to the secret fields, which the implementation runs through the [[Slot transformer]] on every write and reverses on every read. Callers only ever see plaintext. `AppSettingsRepo` holds the Cipher rather than taking it per call, so a new accessor cannot silently store a secret in plaintext (the gap that left OIDC client secrets unencrypted until the mechanism was unified).

**Distinct** from `provider_settings` — a separate table whose secret keys are declared at runtime by each metadata provider, not by struct fields. It shares the Slot transformer and the same placement of the obligation (`ProviderSettingsRepo` holds the Cipher too), but not `Setting[T]`.

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

Login-time linking gated by the admin flag `AllowLocalAccountLinking`. When an External identity matches no row but its email claim matches a local user, the Provisioner attaches the identity to that user instead of rejecting the login. Status-gated on both auth surfaces: a pending or denied user never auto-links — the outcome is pending/denied, not a session. The flag gates only the first identity (local-password → SSO crossover); a user with at least one identity attaches further providers without it. Relies on the IdP-verified email — GitHub explicitly rejects unverified emails; Google and generic OIDC trust the `email_verified` claim.

### Password reset lifecycle

`service.PasswordResetService`; the seam owning who may request a reset link, what a link proves, and what spending one does. Three steps — `Request`, `Verify`, `Confirm` — over narrow interfaces (user store, token store, session evictor, issuer), so the whole lifecycle is exercisable without a database or an SMTP server.

Enumeration-opaque by construction (ADR-0020): `Request` returns nil for every skipped case — unknown address, OIDC-only account with no local password, rate-limited — and the HTTP layer answers 202 unconditionally. A returned error is for the log only and must never reach the wire. `Verify` collapses unknown, expired and already-spent into a single "invalid".

`Confirm`'s step order is load-bearing: hash the new password **before** consuming the token, so a password that fails policy costs the user nothing and the link still works on the retry. Consuming first left a user who typed something too short with an error *and* a dead link, blocked from asking for another by the 5-minute rate limit.

### Session eviction

Dropping every session belonging to a user, via `SessionRepo.DeleteForUser`. Runs after a password changes — by reset or from the account page — because a session established with the old password would otherwise outlive it, which defeats the reason people reset a compromised account. Deliberately signs out the caller too. **Distinct** from logout (one session, by id) and from `PurgeExpired` (a sweeper, not a security action). **Not** run by Set-initial-password: an OIDC-only user setting their first password has no old credential to invalidate, and evicting would sign them out mid-flow.

### Lockout guard

The invariant enforced on every unlink: a user must end the operation with at least one usable credential — either a password or a remaining linked identity. A user with no password and exactly one linked identity must set a password before that identity can be removed. Enforced at the SQL layer in a single statement so the check and the delete are race-free.

### Provisioning

Admin policy controlling whether an unknown External identity creates a new user. Three knobs in `oidc_auto_provision_details`: `EnableAutoProvisioning`, `RequireAdminApproval`, `DefaultRole`. Off by default after the first user; the first External-identity login on an empty instance (OIDC callback or first trusted-proxy header hit) is always admitted as admin to avoid an unrecoverable state. Same row, same knobs, both auth paths — the table name is historical, not OIDC-only.

The policy has a single implementation: `service.Provisioner` (identity match → Auto-link → auto-provision, returning a semantic outcome — resolved / pending-approval / denied / not-allowed / email-required). The OIDC callback and Forward-auth are thin adapters that map outcomes to their own error vocabulary (landing-page redirect vs plain 401). Neither auth surface owns or duplicates the policy.

### OIDC provider registry

`OIDCService.providers`; the slug → operations map built once at construction. Each entry pairs a provider's authorize-URL builder with its callback exchange, because both dispatch on the same slug and previously did so in separate `switch` statements that had to stay in step by hand. Adding a provider is one entry in `newProviderRegistry`; nothing else in the file switches on a slug.

Shaped as a struct of funcs rather than an interface, matching the [[Job registry]]: the per-provider work already exists as methods, so a registration is a pair of method values and no bodies move. GitHub is the odd one — not an OIDC provider at all, so it has no discovery document and its issuer is the `githubIssuer` constant rather than something the exchange reports.

### OIDC state

`service.stateStore`; the in-process map tying an authorize redirect to the callback that returns. Holds the PKCE verifier, the nonce, the provider slug, and the exact `redirect_uri` sent to the IdP (the callback must replay it to the token endpoint, and it varies with request origin when `APP_URL` is unset). A state is single-use — `take` deletes it, so a replayed callback finds nothing — and entries older than `stateTTL` (5 min) are reaped on write rather than on a timer.

**Process-local, which makes login single-instance.** Two replicas behind a load balancer will fail any callback that lands on the replica that did not mint the state. Sharing it means moving state into a signed cookie or a table — a real design change, not a tidy-up. Until then: run one instance, or pin OIDC callbacks to one.

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

### Book patch

`model.BookPatch`; a partial edit to a Book — nil field means "leave alone", non-nil means "set to this" — carrying the book editing rules on `Apply(*Book)`. Domain-level rather than a wire shape, so a bulk edit, a CLI, or an import path gets the same invariants as the metadata PATCH endpoint instead of re-deriving them.

The rules it owns: short text fields trimmed (Description kept verbatim, it is prose); Rating clamped 0–5; Pages and SeriesTotal clamped non-negative while SeriesIndex is not; Genres/Moods/Tags trimmed then de-duplicated case-sensitively per DedupTags; PublicReviews tri-state with clear beating set.

Two behaviours that surprise callers, both pinned by tests: setting `PublishDate` **also sets Year** (Year is a denormalised display column and would otherwise go stale), so a patch carrying both ends up with the date's year; and an unparseable `PublishDate` is silently ignored rather than rejected. Field *content* — ISBN shape, a plausible year — is validated only in the browser, so a non-UI caller can store neither.

### Lock vocabulary

`model.LockSpecs`; the single declaration of the fifteen per-field locks. One `LockSpec` per lock carries the three facts that used to be restated in five places — the wire name (a `model.LockField` constant), the `books` `*_locked` column, and the flag on `BookLocks` — so every projection is derived: the sparse DTO map, the toggle endpoint, the repo SELECT / scan / UPDATE block, and the [[Apply match]] writability predicate.

Exists because three of those five edits failed **silently**. A missing serializer entry meant the flag never reached the client; a missing toggle case meant the endpoint accepted the key, validated it against the old `LockFields` slice, and did nothing; a missing writability check meant the field was never protected from a metadata provider match. Only the repo column list failed loudly, and only on a count mismatch. Same drift class as the [[Event catalog]] and the [[Job registry]], and the same answer.

**Adding a lock field is one entry in `LockSpecs`** (plus its migration). Validation and application are now the same lookup — `ParseLockField` then `BookLocks.Set` — so a key that validates is by construction a key that applies.

Two projections cannot be derived by a loop and are pinned by parity tests instead. `internal/model`'s test restates the vocabulary exhaustively (the [[Job registry]]'s `registry_test.go` shape) and uses reflection to prove each spec's `Flag` closure targets its own `BookLocks` field — a copy-pasted closure pointing at the neighbour is the [[Column-order coupling]] hazard in a new place. `internal/service`'s test probes `ApplyMatch` per lock and requires every lock to either demonstrably change what a match writes, or be declared — with a reason — as having no provider source (`subtitle`, `moods`, `tags`, `pages`) or as gating a side effect (`cover`).

### Editable field set

`model.editableFields`; the twelve fields of `EditableMetadata` declared once, with `IsZero`, `MergeEditable`, `Book.Editable()` and `Book.ApplyEditable()` derived from it rather than hand-walked. Each entry states how to test the field for empty, how to copy it, and how it projects on and off a `Book`.

`published_date` is the one entry with **no** Book projection, and that is now stated rather than implied: `Book.PublishDate` is a `*time.Time`, so the layout conversion belongs at the boundary that knows the layout ([[Book patch]]'s `applyPublishDate`). Previously this showed up only as the field's absence from two of the four walkers, which reads like an oversight.

**Distinct** from [[Book patch]], the second and wider editable surface: `BookPatch` also carries Format, Year, Rating, Palette, ISBN10, SeriesTotal, AgeRating, ContentRating, Pages and PublicReviews, none of which a [[Sidecar]] holds. What must not drift is the overlap — a parity test maps every editable field onto its `BookPatch` field, because a field the sidecar can carry that no patch can set is a field the edit UI cannot reach. `BookPatch.Apply`'s per-field branches were left in place: each carries its own normaliser (trim, clamp, de-duplicate), so folding them into the walk would relocate the rules rather than remove them.

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

Read path (ingest): file embedded → OPF (if present) → JSON, each layer overlays the previous, **lock-aware** (per-field `*_locked` flags shield DB values from re-extract). Write path (user edits): DB (canonical) → file embedded → JSON sidecar (EPUB cover+text rezip; PDF `/Info` text only; audio deferred). Each step is sequenced and atomic; scan skips re-extract when `files.content_hash` matches our recorded write (hash-stamp guard).

### BookDrop

Pre-approval staging area; files land here before being approved into a Library. Each file becomes a row in `bookdrop_items` with extracted metadata + cover. Approving creates the `books` row and the `files` row.

### Intake

Registering a file into BookDrop. One seam, `BookDropService.Intake` (a file already in the staging directory — the watcher's path) and `Accept` (bytes arriving over HTTP, which must be written first). Both validate the format, record the size from disk, insert the `bookdrop_items` row, and hand the item to the worker pool.

Both hold the wipe read-lock across the *whole* sequence, which is the invariant the seam exists to protect: a row must not be inserted for bytes a Wipe BookDrop is deleting. Taken per file rather than per scan, so a wipe waits for one intake rather than for an entire directory walk. Uploads previously bypassed the lock entirely and could leave a row pointing at a file the wipe had already removed.

A client-supplied filename is a suggestion only — reduced to its base, de-dotted, and stamped — so the bytes always land directly in the staging directory whatever the name contains.

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

### Audio format

`fileproc.IsAudioFormat`; whether a `books.format` value denotes an audiobook (`MP3`, `M4B`). Audio takes a different ingest path — duration and narrator come from tag metadata rather than a text extractor — so the service, task, and extractor packages all ask this. It lives in `fileproc` because format dispatch is that package's job and all three already import it; previously the same four lines were copied into each, so adding a format meant three edits nothing forced to agree.

Says nothing about origin: it answers "is this file audio", not "did we make it". A generated audio [[Rendition]] keeps `books.format = EPUB`, so `IsAudioFormat(book.Format)` is false for a book that has a narration — check for an audio `files` row, or `book_audiobooks`, when the question is about the [[Audiobook run]].

### Column-order coupling

The hazard this codebase used to carry: several positionally ordered lists that had to agree by hand — a `*Cols` constant's SELECT order, its `scan*` function's `Scan` destinations, and for `books` a 39-column `UPDATE` against its argument slice. A count mismatch failed loudly at runtime, but swapping two **same-type adjacent** columns in one list did not: it compiled, it ran, and every row silently got those fields crossed. `909f6bf` fixed exactly this in five `UserRepo` methods. ADR-0023 removed the *dialect* axis of the duplication; the *column-position* axis went with [[Projection]].

**Still live where no Projection exists.** `users`, `annotations`, `devices`, `bookdrop_items`, `sessions`, `user_invites` and the rest still state their column list and their scan destinations separately. `UserRepo.TouchLastSeen` still numbers `$2` before `$1` on purpose so its `(id, at)` argument order works — correct, and a trap for anyone tidying it, especially since every caller discards its error.

Guarded by round-trip tests in `book_test.go` and `user_test.go`: every field gets a value distinct from every other field of its type, so a crossing surfaces as a mismatch. The 15 lock flags alternate rather than being uniform, which catches any adjacent swap. Both tests were verified by deliberately introducing a crossing and confirming they fail — a positional test that passes proves nothing until you have seen it fail for the right reason.

### Projection

`repo.projection[T]`; a table's column list declared once, as an ordered slice of entries that each carry a column's name, its SQL rendering, its scan destination and — where the table has a full-row update — its bound argument. Four tables have one: `books`, `shelves`, `libraries`, `files`.

Every SQL context is derived from it rather than restated: `selectList(alias)` for aliased SELECTs, `returningList(table)` for RETURNING clauses that have no alias in scope, `scan(row, &dst)` for the destinations, and `updateSet(first)` for the SET text plus its argument accessors in one traversal. A computed column (`book_count`, `owner_name`, `progress`) carries an `expr` with the `{alias}` token where the table alias belongs; `with(name, expr)` swaps one entry in place for a query that genuinely computes it differently — the visible-shelves query filling in `owner_name`, the create-book CTE that has no progress row to join.

What this buys is that a column's position and its destination cannot be stated separately, so the adjacent-swap failure is unrepresentable within a projection. What it does not buy is *correctness of the pairing*: nothing in Go knows that `isbn` belongs in `Book.ISBN`. That still takes a round-trip test against a real row. `projection_test.go` covers the shape — golden SQL text, ascending placeholder numbering, one distinct destination per column — with no database.

Scan destinations are always the model field. Anything that used to happen after the `Scan` is an `sql.Scanner` adapter instead (`db.TextArray`, `nullText`, `chaptersJSON`, `shelfRuleJSON`), because a post-scan fixup is a second positional list by another name.

Deliberately **not** an ORM (ADR-0023): it renders column lists, never joins, predicates or ordering. The `INSERT` column lists are also outside it — those name the insertable subset, which is a third membership question, and `Create`'s round-trip test already guards them.

### Error envelope

The JSON shape every non-2xx API response uses: `{"error": "<display message>", "code": "<CODE>"}`, where `code` is omitted unless the case is one a client should branch on. `handler.writeError` and `writeErrorCode` are the only writers — no handler builds the shape by hand, which is what stops a bespoke variant reappearing.

Flat by design, and that flatness is a contract with the TypeScript `ApiError` type. Five handlers used to nest `{code, message}` *under* `error`, so the client assigned an object into a string-typed field: the code was unreadable and the message rendered as `[object Object]` anywhere it reached a toast. Callers worked around it — the invites panel fetched email settings separately rather than branch on the `EMAIL_DISABLED` it was already being sent. Codes are Go constants listed in `AllErrorCodes` and mirrored as a TS union; messages are for display and free to change, codes are not.

### Event catalog

`internal/sse`'s `Catalog`; the single declaration of every SSE event the server publishes. One typed payload struct per event, each naming itself (`EventName`) and stating its [[Audience]]. Emitters call `Hub.Publish(payload)` and never hand-marshal a map or type a name, so a field rename is a compile error rather than a silently-changed wire shape. Quirks that used to live at call sites — the `public:` prefix on shared-shelf slugs — are stamped in the payload instead.

Exists because the vocabulary was two hand-kept lists: string literals across `service`/`task`, and a hand-typed union in `ui/src/api/realtime.ts`. They drifted — `kindle.sent` / `kindle.failed` were published with no listener, so Send-to-Kindle reported nothing to the user. Two Go tests now parse the client union and assert it equals the Catalog in both directions, so the next divergence fails the build. The tests parse the union block specifically, not the whole file: every name also appears as a handler key, and a substring search would still pass after a union entry was deleted.

### Audience

`sse.Audience`; who receives an event, declared by the payload rather than decided by the caller. `Everyone()` is the default for instance-wide surfaces (BookDrop has no per-user rows and its routes are `authed`, not admin-only; Shared shelves are public by definition). `User(id)` restricts delivery to one user's subscriptions — all of them, so every open tab sees it.

Routing replaced client-side filtering. `Hub` fanned every event to every subscriber, so a per-user event had to carry its recipient in the payload and rely on the client to ignore other people's — which meant the recipient's id reached every connected browser. Send-to-Kindle results are the only user-scoped events today; their `UserID` is tagged `json:"-"` and never reaches the wire.

### Job registry

`queue.registry(deps)`; the single list of job kinds the binary knows. One `register[T](work)` entry per job declares the kind, the args type, and the work function together, and derives River's typed-worker plumbing from them; the per-job `Deps` structs are assembled once. Adding a job is one line: the `Client` interface does not widen (kind travels with the payload via `jobs.Args`'s `Kind()` method), and no second registration site exists. Confines the River driver import to `internal/queue`; `internal/task` does not import it.

Crash recovery is River's JobRescuer, which reclaims jobs left `running` by a killed process after a timeout (default 1h). The SQLite polling backend that used to sit behind this same interface is gone (ADR-0023).

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

`BookDropService.Approve`; the orchestration that turns a `bookdrop_items` row into a `books` row + `files` row + cover. Five side effects in sequence, then the [[Auto-enrich]] trigger: Approve reads the `METADATA_AUTO_ENRICH` setting itself and, when it is on, dispatches a `bookdrop.auto_enrich` job through its injected `jobs.Enqueuer` rather than running the fan-out inline. The decision belongs to Approve, so every caller — the HTTP endpoint, the queue, a CLI — gets it; it used to live in the approve handler, which meant only HTTP callers enriched and the approve response waited on the providers. Degrades closed on a settings read error, and a refused dispatch is logged, never fatal — the books row is already committed.

### Bookdrop ingest

The worker pipeline that takes a staged file path, computes its hash, dispatches a Processor by extension, extracts metadata + cover, persists to the bookdrop row.

### Processor

`fileproc.Processor`; per-format metadata extractor. `Extract(ctx, src Source) (Metadata, error)`. One implementation per format (EPUB, PDF, CBZ, MP3, M4B).

### BookSource

`service.BookSource`; a *delivery decision* for the file-serve handler: `{Kind: "local", Path}` (stream via `c.File()`) or `{Kind: "presign", URL, TTL}` (302 redirect). Built by `LibraryHandle.BookSource(ctx, book)`. **Distinct from `storage.Source`** — that's a byte-access primitive; this is a routing answer. **Distinct from `OpenBook`** — only the file-serve handler wants a routing answer; every other caller wants bytes.

### OpenBook

`LibraryHandle.OpenBook(ctx, book) (io.Reader, int64, io.Closer, error)`; the way in-process callers get a book's bytes, wherever they live — storage-backed or legacy on-disk path. Send-to-Kindle and device push both use it, so neither knows the delivery vocabulary and both work identically on local and S3-backed libraries. Deliberately never presigns: a presigned URL answers "what do I tell the browser", which is useless to a caller that needs the bytes. Reaching around it with `os.Open(book.Path)` is what silently broke device push on S3 libraries.

### Backend-backed

`LibraryHandle.IsBackendBacked()`; whether a Library's bytes live in a Storage backend rather than the local filesystem. Named once so callers stop re-deriving it from `libraries.backend_id`. Two policies branch on it: the in-file metadata embed (local only, ADR-0001) and the folder-rename strategy (`os.Rename` vs copy + Pending orphan, ADR-0005).

### Handler dependency groups

`handler.PlatformDeps` / `LibraryDeps` / `DiscoveryDeps` / `AccountDeps` / `EmailDeps`; the seams a Handler cannot work without, split by surface and supplied through constructors so omitting one is a compile error at the composition root. **Distinct** from the domain Library — `LibraryDeps` is wiring, not a collection of books.

`handler.Options` holds the complement: seams that may legitimately be nil, each nil-guarded at its use site where nil selects a documented fallback (no storage backend → local file serving; no OIDC provider configured; no worker pool; forward-auth disabled → every request falls through to `RequireAuth`).

The split exists because a 31-field `Deps` struct made every seam optional by construction, and one of them — the provider settings surface — shipped unassigned and nil-dereferenced on every request to it. Required-vs-optional was already a real distinction in the code, expressed only as the presence or absence of a nil check; this moves it into the type system.

### Book-scoped seam

`handler.bookScoped(fn)`; the one place that turns a book-scoped route into a loaded book. Takes the session user, resolves the `:id` route parameter through the [[bookStore]], answers 404 for a book that is not there and 500 for a lookup that failed, and calls the handler body with a `bookScope{UserID, Book}`.

Fails closed **by construction, not by convention**: a body's type is `func(*gin.Context, bookScope)`, so it cannot run without a resolved book and cannot be registered without the wrapper — wiring one directly is a compile error. That is the property the previous shape lacked. The five-line preamble — take the user id, load the book, branch on not-found, write 404, write 500 — was part of the interface every book endpoint had to learn and restate, at roughly two dozen call sites across eight files. Restating it is what let audiobook status send the lookup error to the blank identifier and report on a zero-value Book, and what let the reading-guide routes skip the existence check altogether.

Two response vocabularies over one resolve: `bookScoped` for the JSON API ([[Error envelope]]), `opdsBookScoped` for the OPDS surface (HTTP Basic, plain text). That difference is real between the surfaces; the resolve and the branch are not duplicated.

Routes that need only existence — add-to-shelf, progress, cancel a run — take the scope and ignore `Book`. The check stops being an idiom a handler can forget.

### bookStore

`handler.bookStore`; the handler tier's read view of the catalog — `GetByID` plus `Search`. Declared as an interface rather than taking `*repo.BookRepo` so a book-scoped handler body is reachable in a test with a fake, which the preamble made impossible: every body began by talking to a real database. **Distinct** from `LibraryService`, which is the Library lifecycle module and no longer fronts the catalog at all.

### Book detail response

`handler.writeBookDetail(c, userID, bookID, outcome, logMsg)`; the one module that answers "the current wire representation of book X for user Y". Owns three rules that were previously restated at five sites across the library, enrichment and bookdrop surfaces: reload the row after a write (so the response carries repo-computed fields and stays in lockstep with a fresh GET), turn a nil shelf-slug slice into an empty one (a JSON `null` where the client's type says `string[]`), and attach [[Outcome]] warnings when the write degraded.

They had already drifted, which is the argument for the module: bookdrop approve hard-coded `Shelves: []string{}` instead of querying, and two of the five carried no warnings. `attachWarnings` — the shared warning attachment the three edit endpoints already used — is absorbed into it rather than sitting beside it.

Callers hand it a book id and it writes the response. A read passes the zero `Outcome` and an empty log message; a plain GET therefore pays one extra primary-key lookup, which is the price of the five sites having one answer instead of five.

### Book file sandbox

`service.SandboxPath`; the allow-list gate every filesystem read or delete of a book file passes through. Roots are `BOOKDROP_PATH` plus every Library with a local path; a path must resolve inside one of them after cleaning. Fails closed — no configured roots admits nothing. Serving (`handler.sandboxPath`) and deleting (`LibraryService.DeleteBook`) share the one implementation so a change to the rule cannot apply to one and miss the other — the reason it lives in `service` rather than in the HTTP layer with its other caller.

### Placer

`service.Placer`; the seam Approve uses to materialize a bookdrop file at its final library location. Two adapters: `LocalPlacer` (filesystem rename + collision-suffix under the library root) and `BackendPlacer` (stream-upload to a `Storage` then drop the local source). Returns `PlaceResult{Location, Size, Mtime}` — the values the `files` row needs. The `PlacerBuilder` factory injected at boot picks the adapter from `Library.BackendID`. Approve never branches on local-vs-S3.

### MetadataWriter

`service.MetadataWriter`; the **edit-side write pipeline** module. Owns the `DB → file embedded → JSON sidecar → folder rename` sequence for user-driven edits only. Three triggers in scope: `manual_edit`, `apply_enrichment`, `auto_enrichment`. The other ADR-0001 §3 rows (`bookdrop approve`, `library scan re-ingest`) deliberately route around this module — for those, the file *is* the source, so a writer that rewrites the file would loop. Single entry point: `Write(ctx, book, trigger) (Outcome, error)`. Only the DB step fails the call; a nil error means the books row was updated and nothing more, so callers read `Outcome.Degraded()` / `Outcome.Warnings()` to learn whether the sidecar and in-file copies kept up. All three edit endpoints — metadata PATCH, field-lock toggle, Apply match — put those warnings on the response, in one shape, via the handler's `attachWarnings`. Decision lives in `decideEffects` (pure); execution is a flat orchestration of four private steps (DB, in-file embed, sidecar, folder rename) — the embed precedes the sidecar because the sidecar's mode is chosen from whether it landed. Stamps `files.content_hash` after a successful in-file write so the next library scan recognises its own write and skips re-extract.

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

`fileproc.ExtractBook(ctx, store, src, format, key) ExtractResult`; shared extraction primitive returning format metadata + cover bytes + audio fields + sidecar overlay. Sole consumer is the bookdrop ingest path — under ADR-0018, scan never extracts.

Lived in its own `internal/extractor/` package until it had one caller and no second consumer: ADR-0018 made BookDrop the only ingest path, and a one-adapter seam is a hypothetical one. Its cost was visible in `formatToPath`, which synthesized fake filenames (`"x.epub"`) so it could feed an extension-keyed dispatcher a format slug — a module converting its own input backwards to satisfy the interface it wrapped. Folding it into `fileproc` puts it where format dispatch already lives and replaces that inverse mapping with [[DispatchFormat]].

### DispatchFormat

`fileproc.DispatchFormat(format) Processor`; the slug-keyed twin of `Dispatch`, which keys off a file extension. Both live in `fileproc` because format dispatch is that package's job. When a caller has both, the key wins: the key is what the bytes actually are, the slug is what a row claims they are.

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

`service.EnrichmentService`; the fan-out coordinator. Owns `providers []provider.Provider`, the in-process result cache (5min TTL, 512-entry cap), and the cover store + book repo handles. Carries **no** `Cipher` — it reads `provider_settings` but never writes config, and the rows it reads arrive decrypted from the repo. It held one until #166, assigned at construction and never read: a dead ADR-0010 obligation. Three external entry points: `Search` (batch fan-out + merge by Confidence), `SearchStream` (SSE-friendly per-provider channel), `LookupByISBN` (priority chain, first non-empty wins).

### Prospective metadata

Candidate matches presented to the user for review before persisting. Streamed over SSE via `EnrichStream` so fast providers render in <1s while slow scrapers fill in (ADR-0009). Distinct from auto-enrich's headless gap-fill (ADR-0012) and from `LookupByISBN`'s short-circuit chain (ADR-0011).

### ISBN chain

`LookupByISBN`'s ordered walk through enabled providers by `provider_settings.priority` ASC (ranked first, unranked fall back to catalog order). First provider returning ≥1 match terminates the walk; within that batch, max-Confidence match wins. Used by `POST /api/books/metadata/isbn-lookup` and by `AutoEnrich` when the book has an ISBN. ADR-0011.

### Auto-enrich

`EnrichmentService.AutoEnrich(ctx, book)`; headless background gap-fill triggered by [[Approve]], which reads the enable setting and queues a `bookdrop.auto_enrich` job (`task.BookDropAutoEnrich`) — the worker re-reads the book row and calls this, so nothing runs on the approving caller's goroutine and a provider that was down is retried by River. Empty-only policy carried by `ApplyOptions{OnlyEmpty: true}`, an explicit argument (ADR-0012 §2): fill blank fields, leave populated ones alone. The stored `*_locked` columns are never written by this path — they record the user's intent only. The earlier implementation synthesised a lock overlay instead and persisted it, permanently locking every auto-enriched book on every field it already had. Prefers ISBN chain; falls back to `Search` with Confidence ≥ 70 threshold. Triggers `TriggerAutoEnrichment` — ADR-0001 §3 keeps this off the in-file embedded write path.

### ProviderSettingsService

`service.ProviderSettingsService`; the admin surface over `provider_settings` — which metadata providers are enabled, their config blobs, their ranking, and the health telemetry the Settings panel renders. Also owns `LoadConfigs`, the boot-time push of stored config into the running provider instances.

Owns **no** crypto. Config crosses this module as plaintext in both directions; the encrypt/decrypt seam is `ProviderSettingsRepo` (ADR-0010 §4). It held the secret walk until #166, which made encryption a property of this call path rather than of the row — `SetConfig` stored whatever blob it was handed, so a second writer would have stored plaintext with nothing to catch it.

Split out of `EnrichmentService`, which had grown to six unrelated concerns behind one struct. This half was a clean cut: no fan-out or apply path calls it. **Cover intake deliberately stayed behind** — `ApplyMatch` calls `ImportCoverFromURL`, so separating it would have added a dependency without removing coupling (one adapter is a hypothetical seam, not a real one).

### Provider settings row

`provider_settings(id, enabled, config, priority, last_success_at, last_error_at, last_error)`. One row per Catalog entry. `config` is JSONB; `password`-kind fields (Hardcover token, Amazon cookie) are AES-256-GCM encrypted in place by `ProviderSettingsRepo`, which holds the Cipher and a `SecretKeyFunc` the way `AppSettingsRepo` holds the Cipher and a Setting's `Secrets`; non-secret fields (region, language, enabled flags) stay plaintext for `psql` legibility (ADR-0010). Every value leaving the repo is plaintext and every value entering it is encrypted, so no accessor can forget — a read that cannot decrypt errors rather than handing back ciphertext. `priority` is a single nullable int; ranked-first then unranked-in-catalog-order.

### Cipher

`crypto.Cipher` interface with two implementations: `AESGCM` (prod, KEK from `EMBOOKSHELF_SECRET_KEY` base64-decoded 32 bytes) and `Noop` (dev fallback when env unset). Boot semantics are asymmetric: invalid key refuses startup; unset key warns and falls back to Noop. Ciphertexts carry an `enc:v1:` prefix so pre-encryption rows pass through unchanged and the next write upgrades them — no migration. ADR-0010.

### Slot transformer

`crypto.TransformSlots(op, slots)`; the one mechanism every secret-bearing persistence path shares. Applies Encrypt/Decrypt across a set of string slots, skipping empties and nil pointers, all-or-nothing on error so a half-encrypted value never reaches the DB. Callers differ only in how they enumerate slots: a Setting declares them as pointers to its secret struct fields; metadata provider config discovers them from the provider's `ConfigSchema()` at runtime.

### Configurable / SchemaProvider

Optional interfaces a `Provider` may implement. `Configurable.Configure(rawJSON)` accepts plaintext config from the settings UI; `SchemaProvider.ConfigSchema()` returns `[]ConfigField` describing inputs the admin UI should render (text / password / select / textarea). Password-kind is a single declaration driving two things — how the admin UI renders the input, and whether the value is encrypted at rest — so the two cannot drift apart. `provider.SecretKeyLookup` snapshots those keys into the `id → []string` function `ProviderSettingsRepo` uses to find its slots, which is how the repo stays free of the provider catalog.

### Cover host allow-list

`service.AllowedCoverHosts` + `coverURLAllowed`; the gate every cover download passes. https only, host on the list (exact, or suffix for entries starting with `.`), and path under that entry's optional `Prefix` — the prefix exists so a host shared by many tenants cannot be used wholesale.

Enforced on the caller's URL **and on every redirect target**, via `coverRedirectPolicy` installed as the fetch client's `CheckRedirect` (5 hops max). Validating only the first URL was a real hole: `CoverURL` arrives in the request body of `PUT /books/:id/metadata`, which is `authed` rather than admin-only, so any signed-in user could name an allow-listed host they control and have it redirect the server to a link-local or loopback address.

### Enrichment cache

The in-process result cache inside `EnrichmentService`, keyed by normalized `(title|author|isbn)` with a 5-minute TTL. Exists to protect provider quota — Google Books allows roughly 100 requests per 100s per IP, and an admin tabbing between books burns that fast.

Its freshness rules are asymmetric and worth knowing before relying on either entry point: **`Search` is cached, `SearchStream` is not**, and editing provider settings does **not** invalidate it, so an admin who fixes an API key can still see a stale match list until the TTL lapses. This is pinned by tests rather than changed — the quota protection is deliberate — but it is a property callers must know, not one they can discover from a signature.

### Graceful degrade

Fan-out policy: per-provider error → log + write to health table + return nil from goroutine; siblings unaffected. `errgroup` is used purely for ctx propagation (client disconnect cancels all in-flight HTTP calls), **not** for error short-circuit. `_ = g.Wait()` is deliberate. ADR-0013. Don't "fix" it.

**Selection degrades closed, execution degrades open** (ADR-0013 §4). A provider that fails mid-fan-out is skipped and the rest continue; a failure to read `provider_settings` — which decides who runs at all — fails the search instead, rather than querying providers an admin disabled.

### Provider health

`provider_settings.last_success_at` / `last_error_at` / `last_error`; updated fire-and-forget via detached goroutines with their own 3s ctx so request-side cancellation doesn't lose the write. Surfaced in admin Settings as the universal "is this provider working" signal — the only place per-provider failure is visible regardless of which entry point triggered the call.

### Apply match

`EnrichmentService.ApplyMatch(ctx, book, match, opts, trigger)`; the lock-honoring merge. For each unlocked field, overwrite from match if non-empty; categories union when `opts.MergeCategories`; cover imported when `opts.ApplyCover` and unlocked. Always routes through `MetadataWriter`, which `EnrichmentService` and `LibraryService` both take as a required constructor argument — there is no direct-to-repo fallback and no wiring that skips the ADR-0001 pipeline. Returns `(model.Book, Outcome, error)`: the Outcome travels with the book so the endpoint can report a degraded write, the same as the other two edit endpoints. Single codepath shared by manual UI apply and auto-enrich; only `AutoEnrich` discards the Outcome, and only because ADR-0001 §3 makes its write DB-only.

---

## Reading guides (ADR-0024)

### Reading guide

A short LLM-written orientation for one book: what it is about, who it suits, who it does not, and which reader problems it addresses. Stored in `book_reading_guides`, one row per book.

**Distinct** from `Book.Description` — that is the publisher blurb, arrives from a Metadata provider or the EPUB OPF, and participates in locks, Sidecar and enrichment. A Reading guide is derived text: it is not metadata about the book and never reaches the Sidecar or the file's embedded metadata. Hence a separate table — a column on `books` would pull it into `UpdateMetadata` and therefore into the ADR-0001 write-back pipeline.

**Not an agent.** Single-shot generation, no tools and no loop; calling it an agent would promise machinery that does not exist.

### Source kind

What a given Reading guide was built from: `full_text` or `metadata`. EPUB yields full text; PDF, CBZ and audio have none and get a metadata-only guide. Stored beside the text and shown in the UI, because a metadata-only guide for an obscure book is substantially the model filling in gaps.

### Guide generator

The service that assembles the input, calls the model, and records the result. Its only outbound seam is an OpenAI-compatible endpoint configured by base URL, so cloud and a local Ollama are the same adapter. The key lives in an encrypted field, like provider secrets (ADR-0010).

**Distinct** from a Metadata provider: that one looks up facts via `Search(Query) []Match`; this one writes prose.

### Guide run

An admin-triggered bulk generation across the library. Shows an estimated volume before starting — cost is always the result of an explicit action, and nothing generates on approve. Skips guides marked `edited_by_user` so a run cannot erase hand-written text; the per-book button always overwrites, because there the intent is visible.

---

## Audiobook generation (ADRs 0025–0028)

### Rendition

One of the ways a single Book can be consumed: its text, or its generated narration. Both are `files` rows on the same `books` row — narrating a book produces another artifact of the same work, not another work (ADR-0025).

Introduced because `books.format` stops being the reader's dispatch key. `books.format` remains the *primary* format cache (`EPUB` outranks `MP3`, so a narrated EPUB stays `EPUB`), and `read.$id.tsx` dispatches on the rendition the user selected instead. Flipping `books.format` to `MP3` would have made every downstream surface work untouched and was rejected as destructive: the book stops being readable as text, the in-file embed retargets to a format with no embed path, and Send-to-Kindle's [[Eligible format]] gate turns the button off.

The hazard worth knowing: several call sites still branch on `book.format`, and after this they are answering a subtly different question than the one they appear to. Reading and listening share one progress value, bridged by the [[Alignment map]].

### Narratable format

The set `{epub}`; the formats audiobook generation accepts. EPUB is the only format with a text extractor — there is no PDF library in `go.mod`, CBZ is images, MOBI/AZW3/FB2 have no extractor. Gated at three points like its namesake: the UI button, the handler (415 `FORMAT_NOT_NARRATABLE`), and the queue worker.

**Distinct** from [[Eligible format]] (`{epub, pdf}`, Send-to-Kindle). Different set, different filter — do not reuse the term. Also distinct from ADR-0024's Source kind: a guide degrades to `metadata` for a PDF, whereas narration has no degraded mode, since nobody wants a narrated blurb.

### TTS engine

A speech-synthesis backend: `openai` (any OpenAI-compatible `/v1/audio/speech`, including local Kokoro and openedai-speech), `elevenlabs`, `azure`. Two methods on the seam — synthesis and `ListVoices`, the latter feeding the generate dialog, fetched on open and not cached.

**Distinct** from Metadata provider, OIDC provider and Forward-auth. Never say "provider" alone for this.

Unlike metadata providers, engines are **selected, not fanned out** — fan-out would generate and bill the same book three times — so `provider_settings.priority` semantics and ADR-0013's graceful degrade do not apply. A failed engine is a failed generation.

### TTS catalog

The Go literal declaring which engines the binary knows, plus their endpoints, per-request character caps, and a default price per million characters. Hard-coded and rebuilt to change, like the metadata [[Catalog]] (ADR-0008).

Its existence reverses the stance of ADR-0020 and ADR-0024, which both refused a catalog. Those refusals turned on the vendors differing in billing rather than capability; speech engines differ in per-request cap (3,000 → unbounded), SSML support, word-timing output, and price by a factor of twenty. Here the second and third adapters are real on day one.

### Audiobook settings row

`app_settings.AUDIOBOOK`; one typed `repo.Setting[T]` row holding the selected engine, the default voice, and a per-engine sub-struct (`enabled`, `apiKey`, `baseURL`, `model`, `defaultVoice`, `pricePerMillionChars`). `Secrets` returns the three API keys, which is the whole opt-in to AES-GCM at rest (ADR-0010).

Deliberately **not** a `provider_settings`-shaped table: that machinery exists for runtime-discovered, divergent config, and TTS config is uniform and closed at compile time. Using `Setting[T]` also picks up the safer encryption path — `AppSettingsRepo` holds the Cipher, so a new accessor cannot silently store plaintext.

`pricePerMillionChars` is admin-editable and catalog-prefilled: the pre-flight estimate shows real money, and a stale default is the operator's to correct rather than a bug we cannot close.

### Audiobook run

An admin-triggered generation of one Book's narration. Admin-only at the router and **per book — there is no bulk run** (ADR-0028 §1). A reading-guide [[Guide run]] over a thousand books costs about ten dollars; the same shape here costs eight thousand to a hundred and seventy thousand, so the surface is not built rather than gated.

One audiobook per Book (`book_audiobooks.book_id` is the PK, mirroring `book_reading_guides`). Regenerate is a destructive overwrite behind a type-to-confirm. Cancel is a state the workers check before each engine call — the only stop-loss on a run in progress — and sweeps [[Staging area]] immediately; failure retains it, because a retry is likely.

State lives in `book_audiobooks` (state, engine, voice, model, `segment_chars`, `source_content_hash`, `file_id`, error). Engine, voice, model and `segment_chars` are the run's **pins**: whatever it started with wins over the current settings row for its whole life, so an admin editing settings mid-run cannot produce a book narrated half in one voice — or, in the case of the cap, split two different ways. Provenance is the `file_id` pointer, not a flag on `files`. Staleness is `source_content_hash` against the book's current EPUB hash — surfaced as "generated from an older copy", never auto-invalidated.

The `state` column is a **summary, not the fact**. [[Coverage]] over the Segment rows is the fact, and where the two disagree Coverage wins: `model.NextForRun(state, coverage)` derives the run's next transition from the counts, consulting `state` only for the three conclusions that outrank them (ready has its file, canceled was stopped on purpose, failed needs no second failure). `AudiobookService.Status` applies that rule on every read, so a run whose segments all landed is finalizable on sight however it is labelled — see [[Reconcile-on-read]].

Generation is **not** one of `MetadataWriter`'s triggers: no Sidecar, no in-file embed, no folder rename. In particular `books.narrator` is never written — that column means "what this file's tags said"; the synthesized voice lives on `book_audiobooks.voice`.

### Segment

One unit of synthesis: a row in `book_audiobook_segments` (sequence, chapter title, character range, staged path, duration, start offset, state, error) and one River job on the dedicated `audiobook` queue.

Boundaries come from EPUB spine items, titles from a `toc.ncx` / EPUB3 `nav` parser mapping TOC hrefs back to spine items; non-prose front matter (cover, nav, copyright) is skipped. A cap of ~40,000 characters splits an oversized chapter into several segments sharing one title, and is also the fallback when an EPUB has no usable structure. Splits land on sentence boundaries — a mid-sentence split is audible at every one of ~180 seams.

The split itself happens twice: once at plan time, and again in every segment job, because the EPUB stays the source of truth for what a character range contains rather than a stored copy of the prose. Both go through `service.SegmentBook` at the run's own pinned cap, so the two cannot disagree by construction, and `service.SegmentTextAt` refuses a segment whose re-extracted range is not the one the planner stored. That check is the [[Alignment map]] earning its storage: a book re-uploaded with a paragraph added keeps its segment count and moves every later offset, which the segment-count comparison this replaced waved through and narrated from text nobody planned.

Chapter granularity is load-bearing twice over: a book-length job would outlive River's ~1h JobRescuer window and be silently re-run and re-billed, and a failure at call 170 of 180 would otherwise cost the whole book. On failure the run fails but every completed segment is retained; Retry re-enqueues only `pending` and `failed`.

**Distinct** from `model.Chapter` — a Chapter is the playback view written to `books.chapters` at finalize; several Segments may share one.

A Segment's result is never written on its own. `BookAudiobookRepo.RecordSegment` writes the row, reads [[Coverage]], and moves the run in one transaction, locking the run row `FOR UPDATE` first so concurrent workers cannot each read a snapshot missing the other's write and both conclude the run is unfinished. It replaced a write-then-advance pair whose two halves a killed process could separate.

### Coverage

`model.AudiobookCoverage{Total, Done, Failed}`; the three counts of an [[Audiobook run]]'s Segments, taken in one query so they cannot be read a moment apart and disagree while segments are landing. Progress is done-over-total on persisted rows rather than job state, which is what survives a reload and a restart on a job measured in tens of minutes (ADR-0028 §7).

It is also the run's **authority on its own lifecycle**, not merely a progress bar: `Complete()` (every Segment landed) and `Settled()` (none still outstanding) are what `NextForRun` decides transitions from, in preference to the `book_audiobooks.state` column. Same shape and same reasoning as the reading-guide run's `CountCoverage`.

### Alignment map

The `(char_start, char_end) ↔ (start_s, start_s + duration_s)` correspondence between a book's text and its narration, letting one progress value serve both [[Rendition]]s. Not a separate structure — it *is* `book_audiobook_segments`, since embookshelf generated the audio from the text and knows both sides for free.

Persisted from the first version even though cross-rendition sync itself is deferred: regenerating a 500 MB audiobook later purely to recover data we already had would be absurd.

### Staging area

`${DATA_PATH}/audiobooks/{book_id}/`; where per-Segment MP3s live until finalize concatenates them. Local filesystem, outside `storage.Storage`, following the `coverstore` precedent for derived bytes — the output is not a library artifact until it is finished.

Finalize joins the frames byte-wise (same engine, voice and settings across a run, so no transcode and no muxer), writes ID3v2 `CTOC`/`CHAP` chapter frames plus standard tags and cover art, hands the single file to `Placer`, and clears staging.

Reclaim is `BookAudiobookRepo.ListStaleStaging`, swept hourly, following `LoopMissingPurge` and `LoopOrphanedKeys`. It matches any run untouched for 7 days **except** a pending or running one whose Coverage is complete — that run is a single finalize away from a finished book, so reclaiming it would convert something recoverable into audio that has to be bought again. Keyed on the segment rows rather than on the state column, so a run stranded outside `failed`/`canceled` is still reclaimable; the earlier `state IN ('failed','canceled')` predicate left such a run parking gigabytes forever.

MP3 rather than M4B because every engine emits MP3 and none emits M4B; M4B needs AAC, and no usable pure-Go AAC encoder exists. Requiring `ffmpeg` was rejected — it would make the feature silently dark on installs without it, trading the single-binary property for container polish (ADR-0027).

### Reconcile-on-read

Where a stranded [[Audiobook run]] recovers. `AudiobookService.Status` reads the run and its [[Coverage]] and applies `NextForRun` before it answers, so the poll the UI already makes every four seconds is also what re-dispatches a finalize job a crash lost. `Retry` runs the same rule first, ahead of its already-running and nothing-outstanding refusals — both of which used to fire on precisely the stranded run.

Chosen over the two alternatives deliberately. It cannot live in the write alone: `RecordSegment` commits to Postgres and the finalize job goes to River, two systems no transaction spans. And a sweeper would be a second schedule carrying a second copy of the completeness rule, running hourly, for an observation already happening on every page load. Best effort by construction — a queue that is down costs the recovery, not the read.

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
- "Provider" alone is ambiguous — say **OIDC provider** (auth, redirect flow) or **metadata provider** (enrichment) or **TTS engine** (narration) or **Forward-auth** (proxy header trust). For statements that span OIDC + forward-auth, say **External identity provider**.
- "Eligible format" ≠ **Narratable format**. The first is `{epub, pdf}` and belongs to Send-to-Kindle (ADR-0021); the second is `{epub}` and belongs to audiobook generation (ADR-0028). Never reuse one for the other.
- "Audiobook" alone is ambiguous once generation exists — say **ingested audiobook** (an MP3/M4B that arrived through BookDrop, whose `books.format` *is* audio) or **generated audiobook** / **audio Rendition** (synthesized from an EPUB, whose `books.format` stays `EPUB`). [[Audio format]] is the ingest-side predicate and says nothing about origin.
- "Trusted proxy" alone is ambiguous — say **Trusted proxy CIDR** (the IP allowlist) when discussing the gate; "the upstream proxy" when discussing the deployment.
- "Email provider" — **don't use**. There is no email-provider abstraction. Say **Sender** (transport seam) or **SMTP config** (the `app_settings.EMAIL` row). ADR-0020.

When proposing a deepening, use the architecture vocabulary: **module**, **interface**, **implementation**, **depth**, **seam**, **adapter**, **leverage**, **locality**, plus **deletion test** and **one-adapter-is-hypothetical-two-is-real**.

ADRs live under `docs/adr/NNNN-title.md`. Format: see `~/.claude/skills/grill-with-docs/ADR-FORMAT.md`.
