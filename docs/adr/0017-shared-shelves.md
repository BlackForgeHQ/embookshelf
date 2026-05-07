# Shared shelves are public read-only with broadcast invalidation

A "shared shelf" is a regular shelf an admin has flipped public so every user sees it in their sidebar. It is one row in `shelves` (with `is_public = true`), curated by its owning admin, broadcast via SSE on edit, and surfaced behind a `public:<slug>` URL prefix. Smart shelves cannot be shared. The Unshelved test (ADR-0016) ignores shared shelves — only the viewer's own curation counts.

## Status

accepted (2026-05-07)

## Considered options

- **Public read-only, one row many viewers.** Picked. New `shelves.is_public` column. Owner is the sole curator; admin's edits propagate to every viewer with no per-user copy. URLs are namespaced via a `public:<slug>` prefix so the existing per-user `(user_id, slug)` index keeps working unchanged; uniqueness across published shelves is guarded by a partial unique index on `shelves(slug) WHERE is_public = true`. Sidebar lists widen (`WHERE user_id = $1 OR is_public = true`); the "Add to shelf" picker stays narrow (`WHERE user_id = $1`) so non-owners never get a curate-affordance they can't honour.
- **Clone on share.** "Share" copies the shelf row plus its `shelf_books` membership into every user's account. Each user then owns an independent copy. Cost: bulk insert at share time, plus admin's later edits silently drift away from viewers' copies — un-sharing means hunting down N clones. Wrong shape for "admin curates, everyone reads".
- **Template / auto-attach for new users.** Mark a shelf as default-for-new-users; clone it into each user's account at signup with optional back-fill for existing users. Same drift problem as the clone option, plus a new "is this a template" axis to maintain. Useful for organisational defaults but not for "share my picks" — overshoots the ask.
- **Public smart shelves.** Allow `is_public` on smart shelves too, evaluating the rule against either the viewer's books (per-viewer) or the owner's books (per-owner). Per-viewer turns a shared smart shelf into a "shared filter recipe" — useful but confusing because rule fields like `rating` and `progress` live in `book_user_data` and the result list differs per user. Per-owner exposes the admin's per-user data (their ratings, their progress) to every viewer, which is a privacy regression. Disallowed via a CHECK constraint.
- **Owner-initiated, admin-approved publishing.** Any user can request "make public", admin approves. Heavier (a queue, notifications, an "approved by" trail) and not asked for. Owner-only publishing keeps editorial accountability with the admin doing the broadcast.
- **Per-user fan-out instead of broadcast SSE.** When admin edits a public shelf, enumerate every user and emit per-user invalidation events through the existing per-user SSE channel. Equivalent UX, O(users) work on every edit, and double-routes data that is by definition the same for every viewer. Picked the broadcast channel instead — one event, the hub fans out across all connections, per-user routing stays untouched for everything else.
- **Per-viewer mute and reorder.** Let viewers hide individual shared shelves from their sidebar and pin/reorder. Useful in a noisy multi-admin instance; not justified for v1 with single-digit user counts. Additive later via a `shelf_mutes(user_id, shelf_id)` table — none of the v1 decisions block it.

We picked the public read-only model because shelves already encode "who curated this" via `user_id`, and adding `is_public` reuses that ownership boundary instead of inventing a parallel one. The schema cost is one column plus one partial index; everything else (mutation gating, picker filtering, sidebar grouping) is read-time scope changes against the same table.

## Consequences

- Shared shelves are reversible at the row level: flip `is_public` off and the shelf returns to a private shelf belonging to its owner. Active viewers get redirected back to `/library` with a soft toast rather than a 404 mid-page; the SSE hub broadcasts `shelf.public.removed` so every connected client invalidates `/shelves` and the matching `/shelves/public:<slug>/books` query in lockstep.
- Owner-only mutations: `POST /api/v1/shelves/public:<slug>/books`, the corresponding `DELETE`, `PATCH /api/v1/shelves/public:<slug>`, and `DELETE /api/v1/shelves/public:<slug>` all require `caller.role = 'admin' AND shelves.user_id = caller.id`. Non-owners get 404 (same shape as today's "shelf not found" — preserves the existing API contract for missing slugs).
- Smart shelves stay private. The CHECK `(is_public = false OR is_smart = false)` is enforced in both Postgres and SQLite migration trees per the dual-dialect rule. The smart-shelf rule schema and evaluator stay unchanged.
- The `public:` URL prefix is a string convention rather than a separate identifier space. Backend resolves it at the handler layer (`strings.HasPrefix(slug, "public:")`), routes to a `WHERE is_public = true AND slug = $2` lookup, and rejects the prefix on writes by non-owners. Owner's own sidebar link is rewritten to `public:<slug>` once published — single canonical URL, no "private vs public twin" of the same shelf.
- Realtime: a new broadcast channel on `/events`. Hub emits `shelf.public.updated` (membership, rename, accent) and `shelf.public.removed` (un-publish or delete) without a per-user filter. `useRealtime()` translates both into `queryClient.invalidateQueries({ queryKey: ["shelves"] })`; if the viewer is currently on `?shelf=public:<slug>` for the affected shelf, the `removed` handler additionally calls `navigate({ to: "/library" })` and surfaces a toast.
- Unshelved (ADR-0016) is unchanged at the SQL level. The `NOT EXISTS` clause already filters by `s.user_id = $1` implicitly through the join — shared shelves owned by someone else never participated in the test. CONTEXT.md spells this out so future readers don't try to "fix" it.
- Admin role changes: demoting an admin auto un-publishes any public shelves they own. Implemented in the role-change service path rather than as a DB trigger — keeps the broadcast event emission in one place. User deletion already cascades the row away via `shelves.user_id → users.id ON DELETE CASCADE`; the same cascade triggers a `shelf.public.removed` broadcast for any of the deleted user's shared shelves.
- Sidebar grouping: a dedicated `SHARED` section between Libraries and Shelves, ordered by creation date, marked with the `users` glyph. No per-row owner attribution — the section heading carries the meaning. Viewer cannot mute or reorder in v1.
- The picker / sidebar split (`WHERE user_id = $1` for one, `WHERE user_id = $1 OR is_public = true` for the other) means the shelves repo grows a `ListVisibleToUser` alongside the existing `ListForUser`, mirroring how this repo already separates "shelves I own" from "shelves I can see".
- Reversibility floor: dropping the feature later requires the `is_public = false` migration on every public row, dropping the partial unique index, removing the `public:` URL parser branch, and dropping the broadcast channel. Bookmarks pointing at `?shelf=public:<slug>` would 404. Acceptable cost for a clearly v1 feature with a small surface; documented here so a future migration knows the blast radius.

## Companion artifacts

- `internal/migrator/migrations/postgres/000017_shared_shelves.up.sql` and `…/sqlite/000017_shared_shelves.up.sql` — add `is_public BOOLEAN NOT NULL DEFAULT false`, the `(is_public = false OR is_smart = false)` CHECK, and the partial unique index on `shelves(slug) WHERE is_public = true`. Down migrations drop in the reverse order.
- `internal/repo/shelf.go` — new `ListVisibleToUser(userID)` widening the `WHERE` to `user_id = $1 OR is_public = true`; `ListForUser` retained for the picker. Slug resolution helper splits on `public:` prefix and routes to the correct lookup.
- `internal/service/shelf.go` — owner-and-admin guard on `Publish(slug)` / `Unpublish(slug)`; cascade un-publish on role demotion; ensure `Update` / `Delete` paths emit the broadcast event when `is_public = true`.
- `internal/handler/shelves.go` — parse `public:<slug>` on all shelf routes; payload gains `is_public bool` and `owner_name string` (only populated for shared shelves the viewer doesn't own).
- `internal/sse/` — new broadcast channel; `shelf.public.updated` and `shelf.public.removed` event types.
- `ui/src/api/books.ts` — extend shelves payload type; expose helpers for prefixed-slug query keys.
- `ui/src/api/realtime.ts` — handler for the two broadcast event types; redirect-with-toast on `removed` while viewing.
- `ui/src/components/Sidebar.tsx` — new `SHARED` section under Libraries; `users` glyph; rewrite owner's link to `public:<slug>` once published.
- `ui/src/routes/_app.library.tsx` — handle `public:<slug>` shelf param transparently (search params already pass through).
- `CONTEXT.md` — "Shared shelf" glossary entry plus the Unshelved cross-reference clarifying that shared shelves don't curate-on-behalf-of viewers.
