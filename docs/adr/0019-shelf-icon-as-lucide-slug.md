# Shelf icon stored as raw lucide slug, regex-validated, lazily bundled

A shelf carries a `shelves.icon` column holding a lucide-react icon name (e.g. `book-marked`, `flame`, `bookmark-heart`). The server validates the slug with a regex (`^[a-z][a-z0-9-]{0,63}$`) only — there is no enumerated allow-list mirroring `ShelfAccents`. The UI statically imports a 12-glyph suggestion set and lazy-loads the tail through `lucide-react/dynamic`. Owner edits propagate to shared-shelf viewers via the existing `shelf.public.updated` broadcast.

## Status

accepted (2026-05-08)

## Considered options

- **Closed allow-list mirroring `ShelfAccents`.** Define `model.ShelfIcons = []string{...}` and reject unknown slugs at the handler. Mirrors the accent palette pattern: server is the source of truth, garbage can't enter the DB, tests assert the catalog. Cost: lucide ships ~1500 icons and adds them every release; a curated subset disappoints users ("why is `flame` not in the picker?"); a full snapshot pinned to a lucide version forces a Go-side migration on every UI dependency bump. Wrong shape — accents are a closed brand palette (8 colors); icons are a glyph library that grows.
- **Free-form string with no validation.** Server accepts whatever the client posts. Simplest, but gives every endpoint that touches the row implicit responsibility for sanitising — and a 4 KB string in `shelves.icon` survives every read site without anyone noticing. Loose enough to be load-bearing somewhere by accident.
- **Regex-validated raw lucide slug.** Picked. `^[a-z][a-z0-9-]{0,63}$` enforces shape (kebab-case, length-bounded) without coupling the server to lucide's release cycle. A bad slug renders as a fallback glyph in the UI — owner-only blast radius, fixable by re-picking. The DB carries presentation data, not enforced semantics.
- **Dedicated `shelf_icons` lookup table.** Foreign-key the icon to a normalised table seeded from a lucide snapshot. Adds a join to every shelf read, a seed migration on every lucide bump, and a referential-integrity story for icons removed by lucide upstream. Heavyweight for a presentation field.
- **Custom SVG/PNG upload per shelf.** Ditched at Q2 — needs a storage backend, MIME validation, SVG-XSS sanitisation, size cap, hashed key, and a moderation story for shared shelves. Picker over a curated icon set is the right shape for v1.

We picked the regex-validated raw-slug model because the trade-off is asymmetric: enumerated lists pay an ongoing maintenance tax (every lucide release, every UI bump) for a safety property — "no bogus slug in DB" — that the UI fallback already neutralises. `ShelfAccents` stays an allow-list because its job is brand fidelity (8 named tokens that map to CSS variables), not glyph identity.

## Consequences

- Migration `000035_shelf_icon` adds `icon TEXT NOT NULL DEFAULT 'library'` in both `internal/migrator/migrations/postgres/` and `…/sqlite/` per the dual-dialect rule. Backfill is one `UPDATE` with a `CASE` expression mapping today's hardcoded `BUILTIN_SHELF_ICONS` slugs to their lucide-canonical names: `reading → book-open`, `finished → check-circle-2`, `new → sparkles`, `tofinish → flag`, `wishlist → bookmark`, `is_smart=true → sparkles`, else → `library`. Zero visual change after upgrade. Down migration drops the column.
- Repo `Update(ctx, userID, slug, name, accent, icon *string, rule, ruleChanged)` signature gains a `*string` icon argument; nil means "leave unchanged" (matching `name` / `accent`). `Create` gains `icon string`; empty defaults to `'library'`. SQL column list `shelfCols`, `shelfColsReturning`, `shelfColsVisible`, `shelfColsVisibleSQLite`, the public-shelf SELECT in slug resolution, and the rowscan in `Scan` all pick up the new column — that's six callsites, all in `internal/repo/shelf.go`.
- Handler validates via `regexp.MustCompile(\`^[a-z][a-z0-9-]{0,63}$\`)` at parse time on `POST /api/v1/shelves`, `POST /api/v1/shelves/smart`, and `PATCH /api/v1/shelves/:slug` (and `public:<slug>` variant). 400 on shape failure with `error: "invalid icon slug"`. No lookup against a list — invalid means "shape wrong", not "not in catalog".
- Realtime: shared-shelf icon edits ride the existing `shelf.public.updated` broadcast — no new event types. Private-shelf icon edits invalidate `["shelves"]` via the existing `useApiMutation` `onSuccess`, same as accent edits. Cross-tab staleness for the same user matches today's accent contract.
- UI bundles lucide in two halves:
  - **Static** import of 12 suggestion glyphs (`library`, `book-marked`, `bookmark`, `star`, `flag`, `folder`, `sparkles`, `flame`, `heart`, `hash`, `clock`, `tag`) into a `STATIC_SHELF_ICONS` map. These render synchronously — no flash on the common case.
  - **Dynamic** fallback via `lucide-react/dynamic`'s `DynamicIcon` for the long tail. First render of a non-suggestion icon shows a 14×14 placeholder for ~50ms then swaps. Acceptable: most shelves use suggestion-list icons, and the placeholder occupies the same box so layout doesn't shift.
- The existing `BUILTIN_SHELF_ICONS` map at `ui/src/components/Sidebar.tsx:43` is **deleted**. Sidebar reads `shelf.icon` directly. Single source of truth.
- The custom hand-rolled `Icon.tsx` editorial palette stays for chrome and affordance icons (chevrons, close, filter, refresh, accent UI). Shelf icons are a separate visual vocabulary — one palette is brand chrome, the other is user-curated content. Don't merge them.
- System shelves (`reading`, `finished` per ADR-0016) are **not** locked from icon edit. Icon is presentation; system-ness in ADR-0016 is about exclusion-from-unshelved and progress-driven membership, neither of which touch the icon column. Migration seeds a sensible default; user can override.
- Account page gains a new `shelves` section in `_app.account.tsx` (alongside `account` / `reading` / `devices`) backed by `MyShelvesPanel.tsx`. Heavy table view: icon · name · accent · kind (regular/smart) · visibility (private/shared) · book count · created. Per-row inline icon picker, plus edit/publish/unpublish/delete actions reusing the existing `useShelfDraftDialog` and `publishShelf` / `unpublishShelf` mutations. The same data already comes from `/api/v1/shelves` — no new endpoints.
- Reversibility floor: dropping the feature later is a one-migration reversal (drop column) + restoring the `BUILTIN_SHELF_ICONS` map and the per-slug fallback in the sidebar. Bookmarks and shared-shelf URLs are unaffected (icon is not in the URL). Acceptable cost.
- Lucide upstream rename risk: if a future lucide release renames `flame → flame-icon`, every row holding `flame` renders the fallback glyph until the user re-picks. Owner-only blast radius; documented here so the fallback path doesn't get "fixed" into a hard error.

## Companion artifacts

- `internal/migrator/migrations/postgres/000035_shelf_icon.up.sql` and `…/sqlite/000035_shelf_icon.up.sql` — add column with default + `CASE`-backed backfill. Down migrations drop the column.
- `internal/model/book.go` — `Shelf.Icon string` field on the struct.
- `internal/repo/shelf.go` — extend `Create` / `Update` signatures, column lists, and row scanners to carry `icon`.
- `internal/handler/shelves.go` — parse + regex-validate `icon` on POST/PATCH bodies.
- `ui/src/components/ShelfIcon.tsx` — new `<ShelfIcon name={slug} size={n} />` wrapper: static map first, `DynamicIcon` fallback.
- `ui/src/components/ShelfIconPicker.tsx` — popover + search + virtualised grid + suggestion row.
- `ui/src/components/account/MyShelvesPanel.tsx` — new account-page panel.
- `ui/src/components/Sidebar.tsx` — drop `BUILTIN_SHELF_ICONS`; render `<ShelfIcon name={shelf.icon} />` directly. Inline edit popover gains the icon picker alongside the accent picker.
- `ui/src/routes/_app.account.tsx` — register the new `shelves` section.
- `ui/src/api/books.ts` — extend `Shelf` type and `createShelf` / `createSmartShelf` / `updateShelf` request shapes with `icon`.
- `CONTEXT.md` — "Shelf icon" glossary entry under Library curation.
