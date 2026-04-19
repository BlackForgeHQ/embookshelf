# Playwright E2E Testing — Implementation Plan

Greenfield plan for adding end-to-end browser tests to embookshelf using
[Playwright](https://playwright.dev/). This document is the spec; no
code exists yet. Implement in a follow-up iteration.

## 1. Goals

- Catch regressions across the full stack: Go server + Postgres + the
  TanStack Start SPA + the embedded-binary serving path.
- Cover the golden paths that the README lists as "live end-to-end"
  (auth, library, book detail, BookDrop, readers, settings, OPDS is
  covered via API, realtime SSE).
- Run locally against `make up` (dev) and in CI against the production
  binary so the embed + `NoRoute` fallback is also exercised.

Non-goals: unit tests (Go has `go test`, frontend has `tsc --noEmit`),
visual regression, performance/load.

## 2. Layout

New top-level directory, isolated from the Vite build:

```
e2e/
  package.json              # playwright + @playwright/test + dotenv only
  playwright.config.ts      # projects: chromium, firefox, webkit; dev + prod
  tsconfig.json
  .env.example              # BASE_URL, DB_URL, ADMIN_EMAIL, ADMIN_PASSWORD
  fixtures/
    auth.ts                 # loginAs(page, role) helper, storageState cache
    db.ts                   # truncate + reseed between suites (psql in docker)
    sse.ts                  # waitForEvent(page, kind) helper over /events
    bookdrop.ts             # drop a fixture EPUB into ./bookdrop
    files/
      sample.epub
      sample.pdf
      cover.jpg
  tests/
    auth.spec.ts
    library.spec.ts
    book-detail.spec.ts
    bookdrop.spec.ts
    reader-epub.spec.ts
    reader-pdf.spec.ts
    shelves.spec.ts
    settings.spec.ts
    stats.spec.ts
    realtime.spec.ts
    opds.spec.ts            # request-context only, no page
```

Rationale for a sibling directory instead of `frontend/tests/`: the
SPA's Vite config disables SSR and the test runner's isolated `tsconfig`
avoids collisions with the `routeTree.gen.ts` generator.

## 3. Test Environment

Two Playwright projects so we can run against both surfaces:

| Project      | baseURL                  | Server                      | When |
|--------------|--------------------------|-----------------------------|------|
| `dev`        | http://localhost:5173    | `make up` (Vite + air)      | local iteration |
| `prod`       | http://localhost:6060    | `./tmp/embookshelf` (binary)| CI, release checks |

Playwright's `webServer` config block boots the right stack per project:

- **dev**: `command: make up`, `reuseExistingServer: true`, wait for
  `/` on 5173.
- **prod**: `command: make build && ./tmp/embookshelf`, wait for `/` on
  6060. This validates the `//go:embed` + `NoRoute` SPA fallback.

Database reset strategy: each suite calls `resetDb()` from `fixtures/db.ts`
which shells out to the docker compose service (`compose.dev.yml`) to
truncate mutable tables (books, shelves, shelf_books, user_book_progress,
annotations, bookdrop_items, reading_sessions, sessions) and re-runs
`scripts/seed.sql`. Users, libraries and seed books are restored in one
shot — fast enough for per-file `beforeAll`.

Credentials come from `scripts/seed.sql` (`admin@local` / `changeme`).
Store in `e2e/.env` so CI can override.

## 4. Fixtures & Helpers

- **`loginAs(role)`** — posts to `/api/v1/auth/login` via the
  `request` context, saves cookies to `storageState`, returns the path.
  Spec files consume it via `test.use({ storageState })` so the UI
  doesn't re-do the login dance per test.
- **`apiFixture`** — pre-authenticated `APIRequestContext` for tests
  that need to set up state (create a shelf, seed progress) without
  clicking through the UI.
- **`sseFixture`** — opens an `EventSource` against `/events` inside
  the page and resolves a promise when a specific event type arrives.
  Used by BookDrop + realtime specs.
- **`dropFile(name)`** — copies `fixtures/files/<name>` into
  `./bookdrop/` (bind-mounted into the container). Paired with the
  5 s watcher interval in the BookDrop subsystem.

## 5. Test Plan (per file)

### 5.1 `auth.spec.ts`
- Login succeeds → redirects to `/`.
- Bad password → stays on `/login`, shows error.
- Accessing `/library` unauthenticated → redirect to `/login`.
- Logout clears session (direct API call, re-hit a protected route → 401).
- First-run bootstrap: with an empty users table, `/login` becomes a
  signup form; submitting creates the admin.

### 5.2 `library.spec.ts`
- Seeded books render on `/library` with covers and metadata.
- Full-text search narrows the grid.
- Format filter (EPUB / PDF / CBZ / MP3) narrows the grid.
- Shelf filter shows only shelf members.
- Sort dropdown reorders (title, author, recently added, rating).
- Clicking a cover navigates to `/book/:id`.

### 5.3 `book-detail.spec.ts`
- `/book/:id` renders title, author, description, palette-coloured cover.
- Inline edit of title persists (reload shows new value).
- "Use fields" on an enrichment match card updates the book.
- "Use cover" replaces the cover image.
- Read button navigates to `/read/:id`.

### 5.4 `bookdrop.spec.ts`
- Drop a file → within ~7 s it appears on `/bookdrop` (watcher is 5 s).
  Prefer the SSE fixture over polling.
- Metadata + cover extracted from the EPUB.
- Approve → item disappears from BookDrop, appears in `/library`.
- Reject → item disappears; file moved to the rejected pile.

### 5.5 `reader-epub.spec.ts`
- `/read/:id` loads an EPUB, paginates forward with arrow keys.
- TOC drawer opens; clicking an entry jumps to that chapter.
- Close + reopen → resumes at the same CFI.

### 5.6 `reader-pdf.spec.ts`
- `/read/:id` loads a PDF, scrolls through lazily-rendered pages.
- Close + reopen → resumes at `page:N`.

### 5.7 `shelves.spec.ts`
- Create shelf from sidebar → appears in the list.
- Toggle a book onto a shelf from book detail.
- Delete shelf → gone; books are untouched.

### 5.8 `settings.spec.ts`
- `/settings/libraries` (admin) — register a filesystem root.
- Trigger a scan → last-scan stats update.
- Non-admin (second seeded user) cannot see the admin panel.

### 5.9 `stats.spec.ts`
- `/stats` renders reading-session heatmap seeded via API.
- Logging a reading session via API updates the current-streak counter.

### 5.10 `realtime.spec.ts`
- Open two browser contexts as the same user. Mutating a book in
  context A causes context B's react-query cache to invalidate and
  re-render without a manual reload.

### 5.11 `opds.spec.ts`
- Uses the Playwright `request` context, not `page`. Sends Basic Auth
  to `/opds/`, asserts the Atom feed advertises catalog entries with
  `application/epub+zip` links. Smoke test only; deep feed validation
  stays in the Go test suite.

## 6. Makefile integration

Add:

```makefile
.PHONY: e2e-install
e2e-install: ## Install Playwright deps + browsers
	cd e2e && npm install && npx playwright install --with-deps

.PHONY: e2e
e2e: ## Run Playwright against local dev (make up must be running)
	cd e2e && npx playwright test --project=dev

.PHONY: e2e-prod
e2e-prod: build ## Run Playwright against the built binary
	cd e2e && npx playwright test --project=prod

.PHONY: e2e-ui
e2e-ui: ## Playwright UI mode for iterating on specs
	cd e2e && npx playwright test --ui
```

## 7. CI

GitHub Actions job (or the equivalent for whichever CI lands):

1. Spin up Postgres service container.
2. Run migrations + seed.
3. `make build` to produce the self-contained binary.
4. Start it in the background, wait for `:6060`.
5. `make e2e-prod`.
6. Upload `e2e/playwright-report/` and `e2e/test-results/` as
   artifacts on failure.

Run on PRs touching `frontend/**`, `internal/handler/**`,
`internal/service/**`, or the e2e directory.

## 8. Data-testid discipline

Playwright locators should prefer `getByRole` + `getByLabel` first,
then `getByTestId` for things that don't have stable accessible names
(palette chips, the cover-upload dropzone, the SSE connection dot).
Whenever a test reaches for a brittle CSS selector, add a testid to
the component instead.

## 9. Phased rollout

1. **Phase 1 — scaffolding** ✅: `e2e/`, config, `dev` project,
   `auth.spec.ts`, `library.spec.ts`. Green locally against the built
   binary on `:6060`.
2. **Phase 2 — coverage** ✅: added `book-detail`, `dashboard`,
   `shelves`, `stats`, `settings`, `opds`, `realtime`, `notebook`,
   `bookdrop` (+ ingest round-trip), `reader-epub`, `reader-pdf` — each
   reader backed by a committed minimal fixture
   ([sample.epub](../e2e/fixtures/files/sample.epub), [sample.pdf](../e2e/fixtures/files/sample.pdf)).
   Deferred: multi-context realtime cache-invalidation (needs a
   `bookdrop.updated` trigger that doesn't mutate shared state).
3. **Phase 3 — CI**: GitHub Actions job spinning up Postgres, running
   migrations + seed, `make build`, starting the binary, `make e2e`,
   uploading `playwright-report/` + `test-results/` on failure. Flag
   as required check on `main`.
4. **Phase 4 — hardening**: flake triage (retries=1, trace on first
   retry), visual-diff spot checks for the readers if we've hit
   regressions there.

## 10. Known constraints

- **Books created via BookDrop approve can't be deleted via API** — no
  `DELETE /books/:id` endpoint. `reader-epub.spec.ts` and
  `reader-pdf.spec.ts` work around this via
  [e2e/fixtures/reader.ts](../e2e/fixtures/reader.ts) `ensureFixtureBook`,
  which caches a fixture book by title (`E2E Sample Book`,
  `E2E PDF Sample`) and reuses it across runs. The fixture file stays
  in `./bookdrop/` for as long as the book row exists.
- **Fixture files are committed** in
  [e2e/fixtures/files/](../e2e/fixtures/files/) —
  `sample.epub` (~1.5 KB) and `sample.pdf` (~0.7 KB). Both are built
  by hand (minimal valid archive + Info dict) and drive both the
  BookDrop ingest and the reader specs.
- **PDF metadata extraction is shallow** —
  [internal/fileproc/pdf.go](../internal/fileproc/pdf.go) scans the
  file header + trailer tail for a parenthesized Info dict and falls
  back to the filename. Hex-literal strings and non-linearized PDFs
  whose Info dict lives past the 1 MB window slip through to the
  filename path. Acceptable for seed-style books; a proper PDF
  library is the next upgrade.

## 11. Open questions

- Dedicated `e2e` Postgres database vs. sharing dev? Sharing has been
  fine so far; revisit if cross-spec state leaks cause flakes.
- Per-test isolation vs. per-file: per-file (or just "assume shared
  dev state, design tests around it") is working — specs that mutate
  restore via `finally` blocks using a fresh request context.
