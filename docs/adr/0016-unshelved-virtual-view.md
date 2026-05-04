# Unshelved is a virtual view, not a stored shelf row

The "Unshelved" surface — the set of books a user has not manually placed on any regular shelf — is implemented as a sidebar entry plus a `?unshelved=1` query-param filter on the books list. There is no row for it in `shelves`, no reserved slug, and no per-user provisioning step. Membership is computed at query time via a single `NOT EXISTS` subquery against `shelf_books`, with `s.is_smart = false AND s.slug NOT IN ('reading','finished')` filtering the join.

## Status

accepted (2026-05-04)

## Considered options

- **Stored per-user system row in `shelves`.** Insert one `is_smart=false` row per user with slug `unshelved`, reserve the slug at create-time so users can't collide with it, and special-case the slug in `BooksInShelfForUser` to swap the join for a `NOT EXISTS`. Mirrors how `reading`/`finished` exist as real rows today. Cost: a migration that backfills one row per existing user, ongoing per-user provisioning at signup, and reserved-word logic in `slugify`/`Create`. The stored row is purely cosmetic — `shelf_books` is never written, the rule column stays NULL, the only thing the row does is exist.
- **Real smart shelf with an `is_unshelved` rule predicate.** Add a new `model.RuleField` and compile it to `NOT EXISTS`. Users could then write smart shelves combining unshelved with other predicates ("unshelved + format=PDF"). Cost: rule schema change, validator update, JSON wire-format change, rule-editor UI for a single boolean predicate. Smart shelves are themselves user-curated, which makes a smart shelf called "Unshelved" semantically odd — it's a built-in, not user-defined.
- **Reserved slug `?shelf=unshelved` via the existing shelf param.** No row, no migration, but `BooksInShelfForUser` branches on the magic slug and `slugify`/`Create` must reject the reserved name to avoid collision with a user shelf actually called "Unshelved".
- **Orthogonal `?unshelved=1` filter param + virtual sidebar entry.** Picked. No row, no migration, no slug reservation. Stacks with `?library=`, `?q=`, `?format=` like any other filter (Q4: when `?shelf=` and `?unshelved=1` arrive together, sidebar clears the conflicting one client-side; server lets `shelf` win). The library books query gets one new `Unshelved bool` field on `BookSearchParams`; sidebar gets a fixed `NavItem` under "All Books".

We picked the virtual view because the data justifies it: every alternative pays migration or rule-system cost to materialise something the SQL already expresses cleanly. The sidebar already treats "All Books" as a virtual entry (count summed from libraries, no shelf row) — Unshelved follows the same precedent.

## Consequences

- The hardcoded slug list `('reading','finished')` now appears in the unshelved `NOT EXISTS` clause too. If a third system shelf is ever added, both the existing sidebar filter and this clause must be updated. Acceptable while the list is two slugs; if it grows, promote to an `is_system` column on `shelves` and migrate.
- `unshelvedCount` is returned alongside the regular `/shelves` payload as one extra `COUNT(*)` query — cheap, hits `idx_shelf_books_book` via the `NOT EXISTS` subquery, no new index needed.
- Cross-tab staleness for the count matches today's `bookCount` contract on regular shelves: local mutations (`addBookToShelf`, `removeBookFromShelf`, `deleteShelf`, `deleteBook`) invalidate `/shelves` via react-query `onSuccess`; the existing `bookdrop.updated` SSE handler is extended to also invalidate `/shelves` so a fresh import bumps the count without a page reload. No new SSE event types.
- OPDS does not surface Unshelved (OPDS does not expose any shelves today; this feature is a curation aid, not a reading-app target).
- The default sort for `?unshelved=1` is `b.created_at DESC`, deviating from the library default of `b.title ASC`. Triage view; newest imports are exactly what the user is looking to shelve. Mirrors the smart-shelf members default.
- Reversibility: switching to a stored row later is a one-migration change (insert per-user rows + flip the handler branch). The wire format (`?unshelved=1`) can stay or be replaced with `?shelf=unshelved` without breaking external consumers — internal-only param.

## Companion artifacts

- `internal/repo/library.go` — `BookSearchParams.Unshelved bool`, append `NOT EXISTS` clause when true.
- `internal/repo/shelf.go` — new `CountUnshelvedForUser(userID)`; reused hardcoded `('reading','finished')` slug list.
- `internal/handler/library.go` — parse `?unshelved=1`; default sort flip when set.
- `internal/handler/shelves.go` — wrap response payload to add `unshelvedCount`.
- `ui/src/components/Sidebar.tsx` — new `NavItem` under "All Books"; new `inbox` icon.
- `ui/src/components/Icon.tsx` — add `inbox` to icon union + SVG path.
- `ui/src/components/CommandPalette.tsx` — hardcoded "Unshelved" entry alongside "All Books".
- `ui/src/api/realtime.ts` — extend `bookdrop.updated` handler to also invalidate `/shelves`.
- `ui/src/routes/_app.library.tsx` — read `unshelved` search param, pass through; empty-state copy "All books are shelved.".
- `CONTEXT.md` — "Unshelved" glossary entry.
