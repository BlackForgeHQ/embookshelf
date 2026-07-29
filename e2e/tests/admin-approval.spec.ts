import { execSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { test, expect } from '@playwright/test'

import { ADMIN_STATE_PATH } from '../fixtures/constants'

// Use the cached admin storageState.
test.use({ storageState: ADMIN_STATE_PATH })

const __filename = fileURLToPath(import.meta.url)
const __dirname = resolve(__filename, '..')
const PROJECT_ROOT = resolve(__dirname, '../..')
const FIXTURE = resolve(__dirname, '../fixtures/sql/pending-user.sql')
const SQL_DELETE_PENDING = `DELETE FROM users WHERE email LIKE '%@e2e.local';`

// Two ways to reach Postgres, because the two environments differ.
//
// Locally it is the compose service and the host usually has no psql
// client, so the only route in is `docker compose exec` — the same path
// `make seed` uses, with the project root as cwd so the compose file
// resolves. In CI it is a service container with the port published and
// a psql client installed, and there is no compose stack at all.
//
// Trying compose first and calling it a day is what made these specs
// skip silently in CI: the probe failed, `postgresReachable` went false,
// and two specs reported success by not running.
const DIRECT_PSQL_URL =
  process.env.TEST_DATABASE_URL ??
  process.env.DATABASE_URL ??
  'postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable'

type PsqlRunner = (sql: string) => void

function composeRunner(): PsqlRunner {
  return (sql) =>
    execSync(
      'docker compose -f compose.dev.yml exec -T postgres psql -U embookshelf -d embookshelf -v ON_ERROR_STOP=1',
      { input: sql, stdio: ['pipe', 'pipe', 'inherit'], cwd: PROJECT_ROOT }
    )
}

function directRunner(): PsqlRunner {
  return (sql) =>
    execSync(`psql "${DIRECT_PSQL_URL}" -v ON_ERROR_STOP=1`, {
      input: sql,
      stdio: ['pipe', 'pipe', 'inherit'],
    })
}

// Picks whichever route actually answers, once.
const runner: PsqlRunner | null = (() => {
  for (const build of [directRunner, composeRunner]) {
    const candidate = build()
    try {
      candidate('SELECT 1;')
      return candidate
    } catch {
      // Try the other one.
    }
  }
  return null
})()

function psql(sql: string) {
  if (!runner) throw new Error('no psql route to the test database')
  runner(sql)
}

function loadFixture(path: string) {
  psql(readFileSync(path, 'utf8'))
}

// These tests poke the dev Postgres directly to seed pending OIDC users
// — they're meaningless on the SQLite e2e lane where no postgres
// container is running. Skip the whole describe block if the container
// isn't reachable instead of erroring out per-test in beforeEach.
const postgresReachable = runner !== null

test.beforeEach(() => {
  test.skip(!postgresReachable, 'dev Postgres container not running')
  psql(SQL_DELETE_PENDING)
  loadFixture(FIXTURE)
})

test.afterEach(() => {
  if (!postgresReachable) return
  psql(SQL_DELETE_PENDING)
})

// Serial, because these two tests share one piece of global state: the
// @e2e.local rows in the users table. Under fullyParallel each test's
// beforeEach deletes and re-seeds them while the other is mid-flight, so
// approving one user could leave the other test's badge reading zero.
// The suite runs single-worker in CI, which hid this locally-only race
// behind a config value rather than fixing it (#216).
test.describe.configure({ mode: 'serial' })

test.describe('OIDC admin approval', () => {
  test('badge surfaces pending users and approve flips them to active', async ({
    page,
  }) => {
    await page.goto('/settings')
    // Not exact: the pending badge renders inside this button, so
    // seeding the users these specs need changes the button's own
    // accessible name to "Users & roles 2". The exact match could
    // only ever pass when the fixture had not been applied (#216).
    await page.getByRole('button', { name: 'Users & roles' }).click()
    await expect(
      page.getByRole('main').getByRole('heading', { name: 'Users & roles' })
    ).toBeVisible()

    // Two pending users → badge reads "2".
    const badge = page.getByTestId('users-tab-badge')
    await expect(badge).toHaveText('2')

    const row = page
      .locator('[data-row="user"]')
      .filter({ hasText: 'pending-approve@e2e.local' })
    await expect(row.locator('[data-row-status="pending"]')).toBeVisible()
    await row.getByRole('button', { name: 'Approve' }).click()

    // Pill disappears for the approved row.
    await expect(row.locator('[data-row-status]')).toHaveCount(0)
    // Badge now reads "1" (one pending user remains).
    await expect(badge).toHaveText('1')
  })

  test('deny flips status to denied and keeps row durable', async ({ page }) => {
    await page.goto('/settings')
    // Not exact: the pending badge renders inside this button, so
    // seeding the users these specs need changes the button's own
    // accessible name to "Users & roles 2". The exact match could
    // only ever pass when the fixture had not been applied (#216).
    await page.getByRole('button', { name: 'Users & roles' }).click()
    await expect(
      page.getByRole('main').getByRole('heading', { name: 'Users & roles' })
    ).toBeVisible()

    const row = page
      .locator('[data-row="user"]')
      .filter({ hasText: 'pending-deny@e2e.local' })
    await row.getByRole('button', { name: 'Deny' }).click()

    await expect(row.locator('[data-row-status="denied"]')).toBeVisible()
    await expect(row.getByRole('button', { name: 'Approve' })).toBeVisible()
    await expect(row.getByRole('button', { name: 'Deny' })).toHaveCount(0)
  })
})
