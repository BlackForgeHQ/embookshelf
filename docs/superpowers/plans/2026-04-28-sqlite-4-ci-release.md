# SQLite CI, E2E & Final Tweaks — Implementation Plan (Plan 4 of 4)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire SQLite into CI and Playwright e2e so both backends are exercised on every workflow run, and clean up the last stale doc reference left over from Plan 3 landing.

**Architecture:** Adds a sibling SQLite job to each existing CI job (`go-test`, `migrations-sanity`, `playwright`). The Postgres path is left untouched — same image, same env, same steps. The SQLite path drops the `services.postgres` block, sets `DATABASE_URL=sqlite://./data/embookshelf.db`, and skips the PG-flavored `scripts/seed.sql` (which uses `pgcrypto` functions). The e2e `globalSetup` is taught to bootstrap a fresh DB via the existing `/api/v1/auth/signup` endpoint when login fails, so the same Playwright suite runs unchanged against either backend. One stale sentence in `docs/architecture.md` referring to "until the SQLite queue worker lands (Plan 3 …)" gets updated now that Plan 3 has merged.

**Tech Stack:** GitHub Actions, Playwright, modernc.org/sqlite (already on classpath from Plan 1), no new runtime deps.

**Companion spec:** [`docs/superpowers/specs/2026-04-28-sqlite-support-design.md`](../specs/2026-04-28-sqlite-support-design.md). §7 (Testing Strategy) and §8 (Rollout & Docs).

**Out of scope:**
- Forward/backward data migration tool between PG and SQLite (deferred per spec §9).
- Re-enabling the `pull_request` / `push` triggers on the workflows (currently `workflow_dispatch` only — that switch is a separate operational decision).
- Changes to `Dockerfile`, `compose.prod.yml`, or `release-please` config — those landed in Plan 2B with the breaking-change footer (`feat!` on commit `4009de1`).

---

## Pre-read: scope decisions locked in by Plan 4

1. **Each existing CI job gets a SQLite sibling, not a matrix.** A `strategy.matrix` on `go-test` would force the SQLite lane to spin up the postgres service container even when unused (services attach to the job, not to a matrix entry). Two distinct jobs is cleaner and lets CI parallelize them properly. Names: `go-test` (PG, existing) → `go-test-sqlite` (new). Same pattern for `migrations-sanity` and `playwright`.

2. **`go-test-sqlite` runs the full `go test ./...`, not just `internal/repo/...`.** The unit/integration coverage worth gating on is broader than just repos. The PG `go-test` job already does this; the SQLite job mirrors it. The repo-test harness (`internal/repo/repotest`) reads `REPOTEST_DIALECT` and skips when the dialect isn't selected, so non-repo tests run identically across both jobs.

3. **`REPOTEST_DIALECT` defaults differ between local and CI.** Local default is `sqlite` (no PG container needed for a quick `go test ./...`). CI sets it explicitly: PG job exports `REPOTEST_DIALECT=postgres`, SQLite job exports `REPOTEST_DIALECT=sqlite`. Belt-and-suspenders — explicit is better than implicit in CI.

4. **`globalSetup` becomes self-bootstrapping.** Today it does `POST /auth/login`, which requires `scripts/seed.sql` to have run. That seed file uses `crypt()` / `gen_salt()` from `pgcrypto`, both PG-only. Two options were considered: (a) maintain a parallel `scripts/seed.sqlite.sql`, or (b) teach `globalSetup` to call `/auth/signup` when login returns 401. Option (b) is strictly better — it removes a maintenance vector (two seeds drifting), works for any fresh-install demo, and matches what a new operator actually does on first run. The PG e2e job keeps loading `scripts/seed.sql` because its admin email matches the signup we'd otherwise do, so login succeeds on the first try and the signup branch is never taken.

5. **SQLite e2e job does not run `scripts/seed.sql`.** Skipping that step is what makes the signup-fallback in `globalSetup` actually exercise. The DB starts empty; the binary applies migrations on boot (`MIGRATE_ON_START=false` is overridden to `true` for the SQLite lane); `globalSetup` signs up the admin; the suite proceeds.

6. **`DATA_PATH` per-job is a fresh tmpfs-style directory.** Both e2e jobs already set `DATA_PATH=./data`. The SQLite DSN resolves `sqlite://./data/embookshelf.db` against that, so the database file ends up at `./data/embookshelf.db`. No new env vars needed.

7. **Migrations-sanity for SQLite mirrors the PG flow.** Same up → down-loop → up cadence, same `cmd/migrate` invocation. The CLI already routes by `DATABASE_URL` scheme (Plan 1). No PG service container.

8. **No CI trigger changes.** All four workflows currently run on `workflow_dispatch` only (deliberate — see commit `152bad4`). Plan 4 adds jobs but does not change triggers. Operators kick CI manually today; that stays.

9. **Architecture doc fix is one paragraph.** [`docs/architecture.md`](../../architecture.md) line 108 hedges "except for bookdrop ingest and library scans, which require Postgres until the SQLite queue worker lands (Plan 3 …)". With Plan 3 merged (`3f7d74d`), drop the caveat.

---

## File Structure

```
.github/workflows/
├── ci.yml                        # MODIFY: add go-test-sqlite, migrations-sanity-sqlite
└── e2e.yml                       # MODIFY: add playwright-sqlite job

e2e/
└── global-setup.ts               # MODIFY: try login → fall back to signup → retry login

docs/
└── architecture.md               # MODIFY: drop "until SQLite queue worker lands" caveat (line 108)
```

No new files. No production-code changes. The plan is workflow YAML + one TypeScript helper + one doc paragraph.

---

## Task 1 — Architecture doc fix

**Files:**
- Modify: `docs/architecture.md:108`

- [ ] **Step 1: Replace the stale Plan 3 caveat**

Find the current paragraph at line 108:

```text
embookshelf runs against either Postgres or SQLite, selected by `DATABASE_URL`. The same binary, same UI, and same feature set work on both backends. Postgres is required for multi-user / multi-writer installs (the queue uses River). SQLite is the zero-dependency default and serves single-user installs end-to-end except for bookdrop ingest and library scans, which require Postgres until the SQLite queue worker lands (Plan 3 of the SQLite-support effort).
```

Replace with:

```text
embookshelf runs against either Postgres or SQLite, selected by `DATABASE_URL`. The same binary, same UI, and same feature set work on both backends — including bookdrop ingest and library scans, which run on River (Postgres) or a single-goroutine polling worker (SQLite) behind a shared `queue.Client` interface. Postgres is recommended for multi-user / multi-writer installs (River supports horizontal scaling and dashboards). SQLite is the zero-dependency default and serves single-user installs end-to-end.
```

- [ ] **Step 2: Verify nothing else references "Plan 3" as pending**

Run: `grep -rn "Plan 3" docs/architecture.md README.md docs/prd.md`

Expected: no hits referring to Plan 3 as future work. Hits inside `docs/superpowers/plans/` and `docs/superpowers/specs/` are fine — those are historical artifacts.

- [ ] **Step 3: Commit**

```bash
git add docs/architecture.md
git commit -m "docs(architecture): drop \"Plan 3 not landed\" caveat now that the SQLite queue worker has shipped"
```

---

## Task 2 — Add SQLite go-test job to CI

**Files:**
- Modify: `.github/workflows/ci.yml` (after the existing `go-test` block, around line 60)

- [ ] **Step 1: Add the `go-test-sqlite` job**

Append after the `go-test` job (between `go-test` and `go-build`):

```yaml
  go-test-sqlite:
    name: go-test-sqlite
    runs-on: ubuntu-latest
    timeout-minutes: 10
    env:
      REPOTEST_DIALECT: sqlite
    steps:
      - uses: actions/checkout@v6

      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: true

      - run: go test -race -coverprofile=coverage-sqlite.out ./...

      - uses: actions/upload-artifact@v7
        if: always()
        with:
          name: go-coverage-sqlite
          path: coverage-sqlite.out
          if-no-files-found: warn
          retention-days: 14
```

- [ ] **Step 2: Pin `REPOTEST_DIALECT=postgres` on the existing `go-test` job**

Find the existing `go-test` job. Above the `steps:` line, add:

```yaml
    env:
      REPOTEST_DIALECT: postgres
      TEST_DATABASE_URL: postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable
```

Then add a `services:` block (the current `go-test` job has no DB service — repo tests need one for the PG dialect):

```yaml
    services:
      postgres:
        image: postgres:16-alpine
        env:
          POSTGRES_USER: embookshelf
          POSTGRES_PASSWORD: embookshelf
          POSTGRES_DB: embookshelf
        ports:
          - 5432:5432
        options: >-
          --health-cmd "pg_isready -U embookshelf"
          --health-interval 5s
          --health-timeout 3s
          --health-retries 20
```

- [ ] **Step 3: Verify the YAML lints**

Run: `actionlint .github/workflows/ci.yml` (if installed locally) or just `cat` it back and read carefully.

Expected: no errors. Required structure: `env`, `services`, `steps` all at the same indent level.

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add SQLite go-test lane and pin Postgres dialect on the existing one"
```

---

## Task 3 — Add SQLite migrations-sanity job to CI

**Files:**
- Modify: `.github/workflows/ci.yml` (append after the existing `migrations-sanity` job, end of file)

- [ ] **Step 1: Add the `migrations-sanity-sqlite` job**

Append at the end of `ci.yml`:

```yaml
  migrations-sanity-sqlite:
    name: migrations-sanity-sqlite
    runs-on: ubuntu-latest
    timeout-minutes: 10
    env:
      DATABASE_URL: sqlite://./data/embookshelf.db
    steps:
      - uses: actions/checkout@v6

      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: true

      - name: Create data directory
        run: mkdir -p ./data

      - name: migrate up
        run: go run ./cmd/migrate up

      - name: migrate down (to version 0)
        run: |
          set -euo pipefail
          for _ in $(seq 1 100); do
            if ! go run ./cmd/migrate down 2>&1 | tee /tmp/migrate-down.log; then
              if grep -qi "no migration" /tmp/migrate-down.log; then
                echo "down complete"
                break
              fi
              echo "migrate down failed"
              exit 1
            fi
          done

      - name: migrate up (again — ensures a clean re-apply)
        run: go run ./cmd/migrate up
```

- [ ] **Step 2: Verify the YAML lints**

Run: `cat .github/workflows/ci.yml | tail -40` and confirm the new job reads cleanly.

Expected: same shape as `migrations-sanity` but no `services` block and `DATABASE_URL` switched to the SQLite scheme.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add SQLite migrations-sanity lane"
```

---

## Task 4 — Make e2e globalSetup self-bootstrapping

**Files:**
- Modify: `e2e/global-setup.ts`

- [ ] **Step 1: Replace the body of `globalSetup`**

Current body issues a single `POST /api/v1/auth/login` and throws on failure. Rewrite to attempt signup when login returns 401 (fresh DB), then retry login. The PG path keeps working because the seeded admin's credentials match `ADMIN_EMAIL`/`ADMIN_PASSWORD`, so the first login succeeds and signup is never attempted.

Replace the file contents entirely with:

```typescript
import { request, type APIRequestContext } from '@playwright/test';
import { mkdir } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

import {
  ADMIN_EMAIL,
  ADMIN_PASSWORD,
  ADMIN_STATE_PATH,
  BASE_URL,
} from './fixtures/constants';

// Logs in once as the seeded admin and persists the session cookie to
// ADMIN_STATE_PATH. Authenticated specs reuse it via `test.use({ storageState })`
// so they don't re-do the login dance in every test.
//
// On a fresh database (e.g. the SQLite e2e lane that doesn't load
// scripts/seed.sql) the first login returns 401. We fall back to the
// public /auth/signup endpoint to bootstrap the admin, then retry login.
// The seeded PG path takes the happy first-login branch.
export default async function globalSetup() {
  // The Go backend's CSRFGuard rejects state-changing requests without a
  // matching Origin/Referer header. Browser-driven requests set it
  // automatically; APIRequestContext doesn't, so we pass it explicitly.
  const ctx = await request.newContext({
    baseURL: BASE_URL,
    extraHTTPHeaders: { Origin: BASE_URL },
  });

  let res = await login(ctx);
  if (res.status === 401) {
    await signup(ctx);
    res = await login(ctx);
  }
  if (!res.ok) {
    throw new Error(
      `globalSetup: login failed (${res.status}) against ${BASE_URL}.\n` +
        `Make sure the Go binary is running (\`make build && ./tmp/embookshelf\`).\n` +
        `${res.body}`,
    );
  }

  const statePath = resolve(ADMIN_STATE_PATH);
  await mkdir(dirname(statePath), { recursive: true });
  await ctx.storageState({ path: statePath });
  await ctx.dispose();
}

async function login(ctx: APIRequestContext) {
  const r = await ctx.post('/api/v1/auth/login', {
    data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
  });
  return { ok: r.ok(), status: r.status(), body: await r.text() };
}

async function signup(ctx: APIRequestContext) {
  const r = await ctx.post('/api/v1/auth/signup', {
    data: { email: ADMIN_EMAIL, name: 'Admin', password: ADMIN_PASSWORD },
  });
  if (!r.ok()) {
    throw new Error(
      `globalSetup: signup failed (${r.status()}) against ${BASE_URL}.\n` +
        `${await r.text()}`,
    );
  }
}
```

- [ ] **Step 2: Verify TypeScript compiles**

Run: `cd e2e && npx tsc --noEmit`

Expected: no errors. (If `tsc` isn't on the e2e dependency tree directly, `npm install --no-save typescript` for the check or run from `ui/` since both projects share the same TS major.)

- [ ] **Step 3: Smoke-test against the local PG dev stack**

Pre-req: `make up` running in another terminal, `make seed` already loaded.

Run:

```bash
cd e2e && npm test -- --reporter=list
```

Expected: tests pass exactly as today. The signup branch is never reached because the seeded admin lets the first login succeed.

- [ ] **Step 4: Commit**

```bash
git add e2e/global-setup.ts
git commit -m "test(e2e): self-bootstrap admin via signup when login fails"
```

---

## Task 5 — Add SQLite Playwright lane to e2e.yml

**Files:**
- Modify: `.github/workflows/e2e.yml`

- [ ] **Step 1: Add the `playwright-sqlite` job**

Append the new job after the existing `playwright` job. The diff vs the PG job:

- Drop `services.postgres`.
- Drop the `Install psql client` and `Load seed` steps.
- Switch `DATABASE_URL` to the SQLite scheme.
- Set `MIGRATE_ON_START: "true"` so the binary creates the schema on its first boot (PG path applies migrations explicitly via `go run ./cmd/migrate up` because the seed step needs the schema present).

Append:

```yaml
  playwright-sqlite:
    name: playwright-sqlite
    runs-on: ubuntu-latest
    timeout-minutes: 20
    env:
      DATABASE_URL: sqlite://./data/embookshelf.db
      EMBOOKSHELF_PORT: "6060"
      MIGRATE_ON_START: "true"
      BOOKDROP_PATH: ./bookdrop
      DATA_PATH: ./data
      LOG_LEVEL: warn
      OTEL_ENABLED: "false"
      BASE_URL: http://localhost:6060
      ADMIN_EMAIL: admin@local
      ADMIN_PASSWORD: changeme

    steps:
      - uses: actions/checkout@v6

      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: true

      - uses: oven-sh/setup-bun@v2
        with:
          bun-version: "1.x"

      - uses: actions/cache@v5
        with:
          path: ~/.bun/install/cache
          key: bun-${{ runner.os }}-${{ hashFiles('ui/bun.lock') }}
          restore-keys: bun-${{ runner.os }}-

      - name: Cache Playwright browsers
        uses: actions/cache@v5
        with:
          path: ~/.cache/ms-playwright
          key: playwright-${{ runner.os }}-${{ hashFiles('e2e/package-lock.json') }}
          restore-keys: playwright-${{ runner.os }}-

      - name: Install UI deps
        working-directory: ui
        run: bun install --frozen-lockfile

      - name: Build UI
        working-directory: ui
        run: |
          mkdir -p ../internal/staticfs/dist
          bun run build

      - name: Build binary
        run: go build -o ./tmp/embookshelf ./cmd/embookshelf

      - name: Install e2e deps
        working-directory: e2e
        run: npm ci

      - name: Install Playwright browsers
        working-directory: e2e
        run: npx playwright install --with-deps chromium

      - name: Start binary
        run: |
          mkdir -p bookdrop data
          ./tmp/embookshelf > server.log 2>&1 &
          echo "$!" > server.pid
          for _ in $(seq 1 60); do
            if curl -fsS http://localhost:6060/api/v1/healthcheck >/dev/null 2>&1; then
              echo "server ready"
              exit 0
            fi
            sleep 0.5
          done
          echo "server did not become ready in 30s"
          cat server.log
          exit 1

      - name: Run Playwright
        working-directory: e2e
        run: npm test

      - name: Stop binary
        if: always()
        run: |
          if [ -f server.pid ]; then
            kill "$(cat server.pid)" 2>/dev/null || true
          fi

      - name: Upload Playwright report
        if: failure()
        uses: actions/upload-artifact@v7
        with:
          name: playwright-report-sqlite
          path: |
            e2e/playwright-report
            e2e/test-results
          if-no-files-found: ignore
          retention-days: 14

      - name: Upload server log
        if: failure()
        uses: actions/upload-artifact@v7
        with:
          name: server-log-sqlite
          path: server.log
          if-no-files-found: ignore
          retention-days: 14
```

Note the artifact names (`playwright-report-sqlite`, `server-log-sqlite`) differ from the PG job to avoid collisions when both fail.

- [ ] **Step 2: Verify the YAML lints**

Run: `cat .github/workflows/e2e.yml | grep -c '^  [a-z].*:$'`

Expected: 2 (two top-level jobs: `playwright`, `playwright-sqlite`).

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/e2e.yml
git commit -m "ci(e2e): add SQLite Playwright lane alongside the Postgres one"
```

---

## Task 6 — Local smoke

- [ ] **Step 1: Run the full Go test suite against SQLite**

```bash
REPOTEST_DIALECT=sqlite go test -race ./...
```

Expected: PASS. (This is what `go-test-sqlite` will do in CI.)

- [ ] **Step 2: Run the full Go test suite against Postgres**

Pre-req: `make db-up` running.

```bash
REPOTEST_DIALECT=postgres TEST_DATABASE_URL=postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable go test -race ./...
```

Expected: PASS.

- [ ] **Step 3: Smoke the SQLite migrations CLI loop**

```bash
rm -rf /tmp/sqlite-mig-smoke && mkdir -p /tmp/sqlite-mig-smoke
DATABASE_URL=sqlite:///tmp/sqlite-mig-smoke/embookshelf.db go run ./cmd/migrate up
DATABASE_URL=sqlite:///tmp/sqlite-mig-smoke/embookshelf.db go run ./cmd/migrate version
# Run down to 0
for _ in $(seq 1 100); do
  if ! DATABASE_URL=sqlite:///tmp/sqlite-mig-smoke/embookshelf.db go run ./cmd/migrate down 2>&1 | tee /tmp/down.log; then
    grep -qi "no migration" /tmp/down.log && break || exit 1
  fi
done
DATABASE_URL=sqlite:///tmp/sqlite-mig-smoke/embookshelf.db go run ./cmd/migrate up
```

Expected: every command exits 0; the loop breaks cleanly at version 0; the final `up` re-applies the schema without errors.

- [ ] **Step 4: Smoke the SQLite e2e flow end-to-end**

```bash
# 1. Build a binary against SQLite
make ui-build
go build -o ./tmp/embookshelf ./cmd/embookshelf

# 2. Run it pointed at a fresh SQLite DB
rm -rf /tmp/embookshelf-sqlite-e2e
mkdir -p /tmp/embookshelf-sqlite-e2e/data /tmp/embookshelf-sqlite-e2e/bookdrop
DATABASE_URL=sqlite:///tmp/embookshelf-sqlite-e2e/data/embookshelf.db \
  DATA_PATH=/tmp/embookshelf-sqlite-e2e/data \
  BOOKDROP_PATH=/tmp/embookshelf-sqlite-e2e/bookdrop \
  EMBOOKSHELF_PORT=6060 \
  MIGRATE_ON_START=true \
  BASE_URL=http://localhost:6060 \
  LOG_LEVEL=warn \
  ADMIN_EMAIL=admin@local \
  ADMIN_PASSWORD=changeme \
  ./tmp/embookshelf > /tmp/embookshelf-sqlite-e2e/server.log 2>&1 &
SERVER_PID=$!

# 3. Wait for ready
for _ in $(seq 1 60); do
  if curl -fsS http://localhost:6060/api/v1/healthcheck >/dev/null 2>&1; then echo ready; break; fi
  sleep 0.5
done

# 4. Run Playwright
(cd e2e && BASE_URL=http://localhost:6060 ADMIN_EMAIL=admin@local ADMIN_PASSWORD=changeme npm test)

# 5. Tear down
kill "$SERVER_PID" 2>/dev/null || true
```

Expected: Playwright reports all tests passing. The signup branch in `globalSetup` exercises (since the SQLite DB starts empty), then login succeeds, then the suite runs.

- [ ] **Step 5: If anything failed**

Fix in the relevant earlier task's commit (rebase / fixup) before moving on. Don't paper over a SQLite test failure with a workflow tweak — the test is the source of truth.

---

## Task 7 — Push branch + open PR

- [ ] **Step 1: Push the branch**

```bash
git push -u origin feat/sqlite-4-ci-release
```

- [ ] **Step 2: Open the PR**

```bash
gh pr create --title "feat: SQLite CI lanes + e2e + final docs (Plan 4 of 4)" --body "$(cat <<'EOF'
## Summary
- Adds `go-test-sqlite` and `migrations-sanity-sqlite` jobs to ci.yml; pins the existing `go-test` job to `REPOTEST_DIALECT=postgres` and gives it a postgres service.
- Adds `playwright-sqlite` lane to e2e.yml; the existing PG lane is unchanged.
- Teaches `e2e/global-setup.ts` to bootstrap an admin via `/auth/signup` when login fails, so the same Playwright suite runs against either backend without a parallel seed file.
- Drops the stale "until SQLite queue worker lands (Plan 3…)" caveat from `docs/architecture.md` now that Plan 3 has merged.

## Test plan
- [x] `REPOTEST_DIALECT=sqlite go test -race ./...` passes locally.
- [x] `REPOTEST_DIALECT=postgres TEST_DATABASE_URL=… go test -race ./...` passes locally against `make db-up`.
- [x] SQLite migrations sanity loop (up → down-to-0 → up) passes locally.
- [x] Local Playwright run against a fresh SQLite-backed binary passes (signup branch exercises).
- [x] Local Playwright run against the existing PG dev stack passes (login-first branch unchanged).
- [ ] CI: `ci.yml` workflow_dispatch shows both `go-test` and `go-test-sqlite` green.
- [ ] CI: `e2e.yml` workflow_dispatch shows both `playwright` and `playwright-sqlite` green.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 3: Capture the PR URL and report back**

The `gh pr create` command prints the URL on stdout. Surface it in the final summary.

---

## Done criteria

1. `docs/architecture.md` no longer claims SQLite is missing the queue worker.
2. `.github/workflows/ci.yml` has matching PG and SQLite lanes for `go-test` and `migrations-sanity`.
3. `.github/workflows/e2e.yml` has matching PG and SQLite Playwright lanes.
4. `e2e/global-setup.ts` self-bootstraps via signup when login fails.
5. PR is open against `main`.

When this lands, the SQLite-support effort (4 plans) is complete. No follow-up plans are queued — the data-migration tool between PG and SQLite is the only remaining open item from the spec, deferred per §9.
