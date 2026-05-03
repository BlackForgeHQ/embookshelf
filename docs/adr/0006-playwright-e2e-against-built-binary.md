# ADR-0006: End-to-end testing with Playwright against the built binary

- Status: Accepted (2026-05-03)
- Deciders: Bohdan Shaparenko (@shbodya)

## Context

embookshelf ships as a single Go binary that embeds the SPA via
`//go:embed all:dist` and falls back to `index.html` from Gin's
`NoRoute` for client-side routing. Unit tests cover Go (`go test`) and
the SPA (`tsc --noEmit` + vitest), but neither exercises the full
stack — the embed path, the SPA fallback, the `/api`, `/opds`,
`/events` surfaces, the BookDrop watcher, or the EPUB/PDF readers —
end-to-end in a real browser. We need a regression net for the
golden paths (auth, library, book detail, BookDrop, readers,
shelves, settings, stats, realtime SSE, OPDS smoke).

## Decision

Add Playwright as the end-to-end runner, in a sibling `e2e/`
directory that runs against the built binary on `:6060`.

**Tool: Playwright.** First-class `request` context for OPDS API
smoke without booting a page; multi-context support for the realtime
spec; `webServer` config can boot either `make up` or the built
binary; tracing + UI mode for flake debugging.

**Location: sibling `e2e/`, not under `ui/`.** The SPA's Vite config
disables SSR and generates `routeTree.gen.ts`, both of which collide
with a test-runner tsconfig living under `ui/`. Isolating in a
sibling directory keeps the runner config independent.

**Package manager: npm, not bun.** Playwright's browser-download
paths and CI cache keys are documented against npm; bun support is
best-effort. Cost: contributors run `bun install` in `ui/` and
`npm install` in `e2e/`.

**Target: built binary on `:6060`, single-origin.** `make build &&
./tmp/embookshelf` serves the SPA + API + `/opds` + `/events` from
one origin. Tests therefore exercise the `//go:embed` + `NoRoute`
SPA fallback every run — these are exactly the failure modes that a
Vite-proxy lane would mask. A second project against `make up` on
`:5173` is contemplated for fast iteration once the `/api` proxy is
untangled, but is not in scope here.

**Browsers: Chromium only for now.** The SPA targets all three
engines but engine-specific bugs are not the regression class we are
guarding against today. firefox/webkit projects can be added without
schema or fixture changes.

**Server boot: manual.** Caller runs `make build && ./tmp/embookshelf`
(plus `make db-up && make seed`) before invoking tests. No
`webServer` autoboot — keeps CI explicit and lets local iteration
reuse a long-running instance.

**Database: shared dev Postgres, no per-test reset.** Specs that
mutate state restore via `finally` blocks using a fresh request
context. A dedicated e2e database or `beforeEach` truncate can be
added later if cross-spec leaks cause flakes.

**Reader and BookDrop fixtures: hand-built minimal files, committed.**
`e2e/fixtures/files/sample.epub` (~1.5 KB) and `sample.pdf`
(~0.7 KB) are minimal valid archives (EPUB ZIP + PDF Info dict),
committed to the repo. Generating at test time would pull EPUB/PDF
builder libraries into `e2e/` deps; copyright-clean real books are
not small enough. Cost: regenerating either fixture is a manual
build.

## Consequences

Positive:

- Embed + `NoRoute` regressions are caught on every run, not only
  during release smoke.
- Playwright's `request` context covers OPDS without a page, keeping
  the OPDS spec a thin smoke check while the deep feed-shape
  validation stays in Go tests.
- Sibling `e2e/` with isolated tsconfig means SPA route-tree
  generation and runner tsconfig do not interfere.

Negative:

- Two package managers in the repo (`bun` for `ui/`, `npm` for
  `e2e/`). Contributors hit this on first checkout.
- Manual server boot — forgetting `make build` produces an obvious
  but unfriendly connection error in `globalSetup`.
- No browser matrix until firefox/webkit projects are added.

Neutral:

- Authentication is bootstrapped once in `globalSetup.ts` and
  persisted to `storageState`. Specs reuse the cookie via
  `test.use({ storageState })` rather than re-logging in per test.

## Alternatives considered

**Cypress.** Rejected: weaker multi-context support (the realtime
spec opens two browser contexts as the same user) and no first-class
`APIRequestContext` for the OPDS path. Multi-engine support is
single-browser-legacy.

**WebdriverIO.** Rejected: heavier setup, no clear win over
Playwright for this stack.

**E2E under `ui/` on bun.** Rejected: SSR-off Vite config and the
generated `routeTree.gen.ts` collide with a runner tsconfig sharing
the same root; Playwright's documented browser-download / CI cache
behaviour is npm-shaped.

**Run only against `make up` (Vite proxy on `:5173`).** Rejected: the
embed + `NoRoute` path is the production deployment shape; a
proxy-only lane would never catch regressions there.

**Per-test database isolation (truncate + reseed in `beforeEach`).**
Rejected for now: per-suite shared state plus `finally`-block cleanup
has held. Reconsider if cross-spec leaks start producing flakes.
