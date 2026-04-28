# Rethink Edit Metadata + Find Metadata Online as Two Pages

## Context

Today the entire metadata workflow lives in one 1,061-line file: [`ui/src/routes/_app.book.$id_.edit.tsx`](ui/src/routes/_app.book.$id_.edit.tsx). The route is technically already a "page", but it crams two distinct jobs into a single two-column layout:

1. **A long structured form** for editing the book's stored fields (~20 inputs across 5 sections)
2. **An enrichment panel** sidebar that streams provider results in tiny `52×78px` cards next to a `240×360px` cover preview, with three confusing actions per match (`Apply` writes server-side · `Use fields` populates the form · `Use cover` is image-only)

Problems this creates:
- Both sides feel cramped — covers from external providers are hard to evaluate at thumbnail size, and the form inputs share horizontal space with the streaming panel.
- The "Find metadata online" experience has no room for a real search-refinement UI, no visual diff between current vs proposed values, and no cover gallery.
- Inline `style={{ ... }}` objects are scattered throughout, mixing with Tailwind utilities and bypassing the strong editorial design tokens already defined in [`ui/src/styles.css`](ui/src/styles.css).
- Native `<select>` elements (Age rating, Content rating, Public reviews) don't match the rest of the shadcn-styled inputs.

The intended outcome is to split this single jammed view into **two dedicated, focused pages** that each get the full canvas, while preserving the existing editorial design system (warm ivory paper, navy-tinted ink, Source Serif 4 + Inter Variable, burgundy accent, 3px radius). No backend or API changes; this is a pure frontend rethink.

---

## Routes (after change)

```
/book/$id              → detail (unchanged; "Edit metadata" button still here)
/book/$id/edit         → form-only edit page (slimmed down, redesigned)
/book/$id/find         → NEW: dedicated "find metadata online" page
```

Both `edit` and `find` link to each other in their headers. The book-detail page gets a second action: "Find metadata online" → `/book/$id/find` directly (today users have to click "Edit metadata" first to reach the enrichment panel).

The new route file follows the existing naming convention: `ui/src/routes/_app.book.$id_.find.tsx` (sibling of `_app.book.$id_.edit.tsx`).

---

## Page 1 — `/book/$id/edit` (redesigned form)

**Layout:** Single column, `max-w-[760px]` centered (book-page metaphor — currently has no max width and runs edge-to-edge).

**Header (sticky top):**
- Left: ← Back to book · serif H1 with the book title (truncated)
- Right: `Find metadata online →` link · `Save changes` (primary, disabled until dirty)
- Separator hairline below in `--color-rule-soft`

**Cover panel (top of canvas, full width):**
- Horizontal strip: `Cover` 160×240 left · stacked actions on right (`Replace from file…`, `Find covers online →`, `Remove cover` destructive-text). Lock toggle inline next to "Cover" label.
- Replaces today's `240×360` cover-in-sidebar pattern; the cover doesn't need to be the dominant visual on a form-editing page.

**Form sections** (each in a `<section>` with serif heading + hairline divider):
1. **Title & author** — title, subtitle, author
2. **Description** — full-width Textarea (min-h `200px`, `font-serif` for readability)
3. **Publication** — publisher, publish date, year, language, pages, ISBN-13, ISBN-10 (2-col grid on `≥md`)
4. **Series** — series name, book #, total
5. **Categories & tags** — genres, moods, tags as **real chip editors** (replace today's CSV `Input` + quick-add pattern)
6. **Ratings** — age rating, content rating (shadcn `Select`, replacing native `<select>`), public reviews (shadcn `Select` with No value / Allowed / Blocked)

**Per-field lock toggle** — moves from a separate column to a small lock icon button to the right of each field label. Locked fields render the input with `opacity-60` and a subtle hatched border.

**Sticky bottom bar (mobile + desktop):**
- Dirty state indicator left ("3 unsaved changes")
- `Discard` (ghost) · `Save changes` (primary)

**Form mechanics (unchanged where possible):**
- Keep plain `useState` + the existing `bookToForm` / `formToPatch` helpers from the current file.
- Add a derived `isDirty` boolean by comparing `form` to `bookToForm(book)` once per render.
- Add **inline field-level errors** for ISBN-13 (13 digits or `^\d{3}-\d{10}$`), ISBN-10 (10 chars), year (4-digit number, range 1400–current+1), pages (positive integer). Validation runs on blur and on submit. No new dependencies — small `validators.ts` helper.

**State coverage to add:**
- Loading: skeleton blocks matching each section instead of "Loading…" text
- Save error: inline banner in the sticky bottom bar (not at top), with retry button
- Empty: N/A (book always exists when this route loads)
- Dirty-leave guard: TanStack Router `beforeLeave` confirmation when `isDirty`

---

## Page 2 — `/book/$id/find` (NEW dedicated enrichment page)

**Layout:** 3-column on `≥xl`, 2-column on `md..xl`, stacked on mobile.
- **Left rail** (`w-[280px]`): search refinement form (sticky)
- **Center** (`flex-1`): streaming results grid
- **Right rail** (`w-[320px]`, only after a match is selected): "Compare & apply" diff panel

**Header:**
- ← Back to edit · serif H1 "Find metadata online" · book subtitle line ("for *The Book Title* — by Author")
- Right: small status pill showing live stream state (`Searching Hardcover…` · `4 results · done`)

**Left rail — search inputs:**
- `Title`, `Author`, `ISBN` text inputs (pre-filled from the book; editable)
- Provider chips below, showing which are **enabled**: `Google Books · Open Library · Hardcover · Goodreads · Amazon · DuckDuckGo` (each is a `Badge` variant with status dot — green=enabled, gray=disabled). Disabled ones are visually muted with a tooltip "Enable in Settings → Metadata providers". Click-through to settings.
- `Search again` primary button (disabled while streaming)

**Center — view tabs (`Tabs`):**
- `Matches` (default) — the streaming results grid below
- `Covers` — gallery of every unique `coverUrl` across all matches, larger thumbnails (`160×240`), each clickable to apply via [`applyCoverFromUrl`](ui/src/api/enrich.ts:72). Source provider rendered as caption under each cover.

**Matches tab — results grid:**
- 2-column grid of result cards on `≥lg`, single column on smaller.
- Each card (`MatchCard` redesign):
  - **Cover preview** at `120×180` (vs today's 52×78) — large enough to actually evaluate
  - Provider source as a small tinted ribbon top-left (e.g., burgundy `bg-accent-soft text-accent-ink`); confidence as a numeric badge top-right (`92%`)
  - Title (serif, h3) · authors (small, ink-3) · year · series
  - Description clamped to 4 lines with "Read more" expand
  - Single primary action: `Compare → ` (opens the right rail diff panel for this match) — **replaces today's three-button decision paralysis**.
  - Tertiary text-link: `Use cover only`
- Live skeleton rows appear while `streaming === true && providers actively responding`.
- **Provider error chips** appear inline at the top of the results column (e.g., `Hardcover: rate limited [retry]`).
- Empty state when no matches: composed illustration + helpful copy ("No matches from … Try adjusting the title, author, or ISBN, or enable more providers.").

**Right rail — Compare & apply panel** (slides in from right when a match is selected):
- Side-by-side rows: `Field · Current · New · ☐ apply`
- Pre-checked when current is empty or the field is unlocked AND new value differs.
- Locked fields shown with a lock icon and disabled checkbox.
- Cover row at top with side-by-side image previews.
- Bottom: `Apply selected fields` (primary) · `Cancel`.
- On success: toast `"Metadata applied from Hardcover."` and the panel collapses; user stays on the page to keep browsing matches. A "Done" button in the header navigates back to `/book/$id/edit` with toast.

**Streaming UX (preserves existing SSE wiring):**
- Reuses [`streamEnrichment` from `ui/src/api/enrich.ts`](ui/src/api/enrich.ts:97) verbatim — no API changes.
- Each provider gets a chip in the header that lights up when its first match arrives, and shows a checkmark when `done` frame includes it. Failed providers show ✗ with the error.
- Cancel-on-unmount is preserved via the existing returned cancel function.

**Cover-only path (fast lane):**
- Reuses [`applyCoverFromUrl`](ui/src/api/enrich.ts:72). Triggered from the per-card `Use cover only` link. Toast confirms.

**Apply path:**
- Reuses [`applyEnrichmentMatch`](ui/src/api/enrich.ts:159). The right-rail panel sends only the user-selected fields by passing `undefined` for unchecked fields in `ApplyMatchBody` (server already respects per-field locks, so this is additive caution).

---

## Files to modify / create

| File | Change |
|------|--------|
| [`ui/src/routes/_app.book.$id_.edit.tsx`](ui/src/routes/_app.book.$id_.edit.tsx) | **Rewrite.** Strip the embedded `EnrichmentPanel` and `MatchCard`. Single-column layout, sticky header + bottom bar, sectioned form, replace native selects with shadcn `Select`, real chip editors for genres/moods/tags, inline field validation, dirty-leave guard. Drop inline `style={{ ... }}` in favor of utility classes. |
| `ui/src/routes/_app.book.$id_.find.tsx` | **NEW.** Dedicated find-metadata page (3-column layout, search rail, streaming results grid, compare-and-apply right rail). |
| `ui/src/components/metadata/CoverPanel.tsx` | **NEW.** Header cover strip used on the edit page. |
| `ui/src/components/metadata/FieldLockButton.tsx` | **NEW** (extracted from edit page's existing `LockToggle`). Reused on edit + find pages. |
| `ui/src/components/metadata/ChipEditor.tsx` | **NEW.** Generic add-on-Enter / remove-on-backspace chip input. Replaces CSV pattern for genres / moods / tags. |
| `ui/src/components/metadata/MatchCard.tsx` | **NEW.** Larger, single-action card used on the find page. |
| `ui/src/components/metadata/CompareApplyPanel.tsx` | **NEW.** Right-rail diff/select/apply panel. |
| `ui/src/components/metadata/ProviderStatusChips.tsx` | **NEW.** Header pills showing per-provider stream state. |
| `ui/src/lib/metadata-validators.ts` | **NEW.** ISBN-10 / ISBN-13 / year / pages validators (small, no dependencies). |
| [`ui/src/routes/_app.book.$id.tsx`](ui/src/routes/_app.book.$id.tsx) | Add a second header action `Find metadata online` linking to `/book/$id/find` (today users must go through Edit first). |

**Files NOT touched:**
- All of `ui/src/api/*` (no API surface change)
- Backend (`internal/handler/enrich.go`, providers, streaming) — unchanged
- The existing settings provider panel — unchanged (linked-to from the find page chip tooltips)
- All other routes

---

## Reusable utilities to lean on

- [`bookToForm` / `formToPatch` / `splitCsv` in `_app.book.$id_.edit.tsx`](ui/src/routes/_app.book.$id_.edit.tsx:83) — keep verbatim, move to `ui/src/lib/book-form.ts` for sharing.
- [`patchBook`, `toggleBookFieldLocks`, `bookQueryKey` in `ui/src/api/books.ts`](ui/src/api/books.ts) — used as-is.
- [`streamEnrichment`, `applyEnrichmentMatch`, `applyCoverFromUrl`, `formatProviderList`, `PROVIDER_LABELS` in `ui/src/api/enrich.ts`](ui/src/api/enrich.ts) — used as-is.
- shadcn primitives already installed: `Button`, `Input`, `Textarea`, `Select`, `Card`, `Tabs`, `Badge`, `Tooltip`, `Skeleton`, `Separator`, `ScrollArea`, `Sheet` (none new needed).
- [`Cover` component](ui/src/components/Cover.tsx) — for the local cover preview.
- [`Icon` wrapper](ui/src/components/Icon.tsx) — Lucide icons (already in use throughout).
- Toast via `sonner` — already wired.

---

## Design audit fixes applied

Per the redesign-existing-projects audit, this rewrite addresses:
- **Native `<select>` mixed with shadcn inputs** → unified shadcn `Select` everywhere.
- **CSV tag input** → real chip editor with keyboard add/remove.
- **Three-button decision paralysis on match cards** → single `Compare →` action that opens a side-by-side diff.
- **Cramped 52×78 thumbnail covers** → 120×180 hero covers in result cards.
- **Inline `style={{...}}` objects** → utility classes throughout, leveraging existing design tokens.
- **No loading skeletons** → skeleton sections matching shape on edit page; streaming skeleton rows on find page.
- **No empty/error UI nuance** → composed empty state with provider hint + clickable "Enable in Settings" link; inline per-provider error chips with retry.
- **No dirty-state guard** → `beforeLeave` confirmation + visible "N unsaved changes" indicator.
- **No field-level validation** → blur+submit validators for ISBN, year, pages with inline messages.
- **Symmetrical/equal three-column layouts** → asymmetric 280 / 1fr / 320 layout on the find page; single 760px column on edit.

What is **preserved** because it is already good:
- Editorial typography (Source Serif 4 + Inter Variable + IBM Plex Mono).
- Warm ivory paper / navy-tinted ink palette.
- Tight 3px radius scale.
- Burgundy editorial accent.
- Per-field lock model.
- Streaming SSE provider results.

---

## Verification

1. `make ui-typecheck` — no TS errors.
2. `make ui-lint` — clean.
3. `make ui-test` — existing Vitest suite passes.
4. `make up` — load `http://localhost:5173`, log in (admin@local / changeme), open any book.
   - **Edit page**: confirm sticky header, single column, dirty indicator appears after first edit, save error inline, validation fires on blur for ISBN/year, lock toggle opacity-changes the input, navigation away while dirty triggers confirm.
   - **Find page**: open `/book/$id/find` directly and via the new header link from book detail. Confirm: provider status pills update as SSE frames arrive, large cover previews render, `Compare →` opens the right rail with side-by-side current/new, locked fields show as disabled, `Apply selected fields` writes through and toast confirms, `Use cover only` link works, errors render as inline chips, cancel-on-unmount stops the stream (verify in DevTools Network).
5. `make e2e` — adapt any existing e2e for the edit route to new selectors; add a basic find-page smoke test.
6. **Accessibility:** keyboard tab through the chip editor (add via Enter, remove via Backspace), ensure focus rings visible on every interactive element, `Compare` panel traps focus while open.
