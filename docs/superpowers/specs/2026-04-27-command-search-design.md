# Command-Powered Search & Palette

Status: Approved (design)
Date: 2026-04-27

## Goal

Add the shadcn `Command` (cmdk) primitive to the embookshelf SPA and use it to power two related search surfaces:

1. An inline **library page combobox** that replaces the current `Input` in `TopBar` when used on the library route. Typing still filters the book grid (current behavior preserved) and a popover shows the top matching books for fast jump-to-book navigation.
2. A global **⌘K command palette** mounted in the authed app shell. Searches books, shelves, and libraries; filters static lists of navigation targets and quick actions; opens dialogs and runs handlers by keyboard.

Both surfaces are powered by a new lightweight `GET /api/v1/search` endpoint.

## Non-goals

- App-wide light/dark theme toggle (no target exists today; out of scope).
- Building a new "scan everything" admin endpoint — the palette deep-links to Settings.
- Visual regression testing infrastructure.
- Replacing the existing `?q=` filter on `/api/v1/books` (still used by the library grid).

## Architecture

### New files

- `ui/src/components/ui/command.tsx` — standard shadcn Command primitive (adds `cmdk` dep). Exports `Command`, `CommandDialog`, `CommandInput`, `CommandList`, `CommandGroup`, `CommandItem`, `CommandSeparator`, `CommandEmpty`, `CommandShortcut`.
- `ui/src/components/LibrarySearchCombobox.tsx` — Popover-anchored Command used inside the library TopBar.
- `ui/src/components/CommandPalette.tsx` — global ⌘K dialog. Mounted once in `_app.tsx`.
- `ui/src/api/search.ts` — `searchSuggest(q, limit)` typed client.
- `ui/src/lib/commandStore.ts` — Zustand store lifting dialog open-state shared between Sidebar and CommandPalette.
- `ui/src/hooks/useLogout.ts` — shared logout mutation used by Sidebar and CommandPalette.
- `internal/handler/search.go` — Gin handler.
- `internal/service/search/search.go` + `search_test.go` — service orchestration.
- Repo additions in existing `internal/repo/` files: `Books.SearchSuggest`, `Shelves.SearchSuggest`, `Libraries.SearchSuggest`.
- `e2e/tests/command-palette.spec.ts` — Playwright happy paths.

### Touched files

- `ui/src/components/TopBar.tsx` — add `searchVariant?: "input" | "command"` (default `"input"`) and `commandHint?: boolean` (default `true`, renders the "Search ⌘K" affordance in the right slot).
- `ui/src/routes/_app.tsx` — mount `<CommandPalette />`, attach the global `⌘K` keydown listener.
- `ui/src/routes/_app.library.tsx` — pass `searchVariant="command"` to `TopBar`.
- `ui/src/components/Sidebar.tsx` — replace local dialog open `useState` for "create shelf" and "user settings" with subscriptions to `commandStore`. Dialog JSX remains in Sidebar.
- `internal/handler/router.go` — register `authed.GET("/search", h.Search)`.

## Backend

### Endpoint

`GET /api/v1/search` (auth-required, mounted in the existing `authed` group).

Query params:

| Param | Required | Default | Notes |
|-------|----------|---------|-------|
| `q`   | yes      | —       | Trimmed; min length 1. Empty after trim → 400. |
| `limit` | no     | 8       | Capped at 20. |

Response:

```json
{
  "books":     [{ "id": "...", "title": "...", "author": "...", "cover": "..." }],
  "shelves":   [{ "slug": "...", "name": "...", "accent": "..." }],
  "libraries": [{ "id": "...", "name": "..." }]
}
```

`books[].cover` matches the existing cover URL convention used by the `/api/v1/books` listing.

### Service

`internal/service/search/search.go`:

```go
func (s *Service) Suggest(ctx context.Context, userID string, q string, limit int) (Result, error)
```

Runs three repo calls in parallel via `errgroup`:

- `repo.Books.SearchSuggest(ctx, userID, q, limit)` — title/author ILIKE; selects `id, title, author, cover_path`; ordered by best match (reuse the ranking from the existing `?q=` SQL).
- `repo.Shelves.SearchSuggest(ctx, userID, q, limit)` — name ILIKE on the user's shelves.
- `repo.Libraries.SearchSuggest(ctx, userID, q, limit)` — name ILIKE; repo enforces visibility (admin sees all; non-admin sees libraries with assigned books, matching the existing `/libraries` rule — verify in implementation and mirror).

If any of the three errors, the call fails (no partial results in v1).

### Errors

- 400 — empty `q` after trim, or `limit` not parseable.
- 401 — no session (handled by middleware).
- 500 — DB error.

No rate limiting in v1.

### Backend tests

- `internal/service/search/search_test.go`:
  - empty `q` rejected at the handler boundary
  - `limit` honored and capped at 20
  - books match on title and author (case-insensitive)
  - shelves filtered to caller's shelves (cross-user isolation)
  - libraries filtered to caller's visible set; admin sees more than non-admin
  - all three groups returned in a single call
- `internal/handler/search_test.go`: 200 happy path, 400 empty `q`, 401 unauthenticated.

## Frontend

### `ui/src/components/ui/command.tsx`

The standard shadcn Command primitive, generated to match the project's `radix-mira` style and zinc base. Adds `cmdk` to `ui/package.json` dependencies.

### `LibrarySearchCombobox`

Props:

```ts
type Props = {
  value: string
  onSearchChange: (next: string) => void
}
```

Behavior:

- Local controlled `CommandInput` styled to match the current 280px field with a leading search icon.
- Wrapped in a Radix `Popover`. Anchor = the input.
- On every keystroke, call `onSearchChange(value)` synchronously so the grid filter updates live (option B from brainstorming).
- `useDebounce(value, 200)` → `useQuery({ queryKey: ['search', debounced], queryFn: () => searchSuggest(debounced, 8), enabled: debounced.length >= 2, staleTime: 30_000 })`.
- Popover open when `value.length >= 2` and the input has focus. Closes on blur, Escape, or selection.
- Renders only the **Books** group (top 8). Shelves and libraries belong to the global palette.
- Each `CommandItem`: 24px cover thumb, title, dim author. `onSelect` → `router.navigate({ to: '/book/$id', params: { id } })` then close.
- Loading: skeleton row. Empty (after debounce, length ≥ 2, zero rows): `<CommandEmpty>No matches</CommandEmpty>`.
- Pressing `Enter` with no highlighted item is a no-op (filter is already applied via live state).

### `CommandPalette`

Mounted once in `ui/src/routes/_app.tsx` (so it covers all authed routes; not mounted on `/login` or the `/read/:id` reader to keep the reader's keyboard surface clean).

Open state:

- Local `useState(false)`.
- Global keydown listener attached at `_app.tsx`: when `(e.metaKey || e.ctrlKey) && e.key === 'k'` → `e.preventDefault()` + toggle. Cleaned up on unmount.
- A "Search ⌘K" button rendered in `TopBar`'s right slot via the `commandHint` flag (default true) opens it on click.

Uses shadcn's `CommandDialog`. Groups in display order:

1. **Quick actions** — always visible, fuzzy-matched by cmdk on label + keywords.
   - Open Bookdrop intake → `router.navigate({ to: '/bookdrop' })`
   - New shelf → `commandStore.setShelfDraftOpen(true)`
   - Open user settings → `commandStore.setUserSettingsOpen(true)`
   - Toggle sidebar → `useSidebar().toggleSidebar()`
   - Library scan (admin only) → `router.navigate({ to: '/settings', search: { section: 'libraries' } })`
   - Sign out → `useLogout().mutate()`
2. **Navigation** — always visible, same fuzzy match.
   - Library, Bookdrop, Notebook, Stats, Settings (each with sidebar icon and route).
3. **Books**, **Shelves**, **Libraries** — populated from the same debounced (`200ms`) `searchSuggest` query as the combobox; rendered only when `value.length >= 2`. Each capped at 8 (server limit).

Selection:

- Books → `/book/$id`
- Shelves → `/library?shelf=<slug>`
- Libraries → `/library?library=<id>`
- Quick actions → run handler

Every `onSelect` calls `setOpen(false)` and clears `value`.

States:

- `value.length < 2` → Quick actions + Navigation only (launcher mode).
- Query in flight → skeleton placeholders inside Books/Shelves/Libraries to avoid layout jump.
- All three search groups empty → `<CommandEmpty>No matches</CommandEmpty>` below the static groups.

Admin gating: Library scan is conditionally rendered using the same `me.isAdmin` check Sidebar already uses.

A11y: `CommandDialog` provides aria roles and focus trap; Esc closes.

### `commandStore` (Zustand)

```ts
type CommandStore = {
  shelfDraftOpen: boolean
  userSettingsOpen: boolean
  setShelfDraftOpen: (open: boolean) => void
  setUserSettingsOpen: (open: boolean) => void
}
```

Sidebar replaces its existing local `useState` for these dialogs with `useCommandStore` selectors and passes them as `open` / `onOpenChange` to the unchanged dialog JSX. Dialog markup stays in Sidebar; only open-state moves.

### `useLogout` hook

Wraps the existing `apiLogout` + `useMutation` + invalidations + redirect that Sidebar currently inlines. Sidebar refactors to use this hook; CommandPalette uses it directly. Single source of truth for logout side-effects.

### `searchSuggest` API client

```ts
export type SuggestBook = { id: string; title: string; author: string; cover: string }
export type SuggestShelf = { slug: string; name: string; accent: string }
export type SuggestLibrary = { id: string; name: string }

export async function searchSuggest(q: string, limit = 8): Promise<{
  books: Array<SuggestBook>
  shelves: Array<SuggestShelf>
  libraries: Array<SuggestLibrary>
}>
```

Uses the existing `api()` helper.

### TopBar additions

```ts
type TopBarProps = {
  // ...existing
  searchVariant?: "input" | "command"  // default "input"
  commandHint?: boolean                // default true
}
```

When `searchVariant === "command"`, render `LibrarySearchCombobox` in the slot currently occupied by the plain `Input`. When `commandHint === true`, render a small "Search ⌘K" button in the right slot **prepended** to the existing `right` content (so callers' right slots still work). Clicking dispatches a custom `'embookshelf:open-command'` event that `CommandPalette` listens for — keeps `TopBar` free of palette state.

## Settings deep link

The "Library scan" quick action navigates to `/settings`. Implementation will check whether `_app.settings.tsx` already accepts a `?section=libraries` param. If a small extension (scroll-into-view on mount when `section=libraries`) is contained, add it. Otherwise, drop the param and just navigate to `/settings`.

## Frontend tests

- `LibrarySearchCombobox.test.tsx`:
  - typing fires `onSearchChange` synchronously (grid filter behavior preserved)
  - popover opens at `length >= 2` after debounce
  - selecting a suggestion calls `router.navigate` with the right `id`
  - empty result shows "No matches"
- `CommandPalette.test.tsx`:
  - `⌘K` toggles open
  - quick actions and navigation render with empty input
  - search groups render after debounce when input has value
  - admin-only "Library scan" hidden for non-admin users
  - selecting a quick action calls the right store setter / navigation
- `commandStore.test.ts`: setters flip the booleans.

## E2E tests

`e2e/tests/command-palette.spec.ts`:

- Open palette via `⌘K`, type a known seeded book title, press Enter, assert URL `/book/:id`.
- Open palette, type "settings", press Enter, assert URL `/settings`.

## Out of scope

- App-wide theme toggle.
- Recent / pinned items in the palette (no persistence in v1).
- Server-side ranking beyond the existing `?q=` ILIKE pattern (e.g., trigram, full-text index) — follow-up if needed.
- Mobile-specific palette UX tweaks (uses default `CommandDialog` sizing).
