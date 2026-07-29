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

// psql shells out into the dev compose stack — same path `make seed` uses.
// Service name is `postgres` (see compose.dev.yml). Working directory must
// be the project root so docker compose resolves the file.
function psql(sql: string) {
  execSync(
    'docker compose -f compose.dev.yml exec -T postgres psql -U embookshelf -d embookshelf -v ON_ERROR_STOP=1',
    {
      input: sql,
      stdio: ['pipe', 'pipe', 'inherit'],
      cwd: PROJECT_ROOT,
    }
  )
}

function loadFixture(path: string) {
  psql(readFileSync(path, 'utf8'))
}

// These tests poke the dev Postgres directly to seed pending OIDC users
// — they're meaningless on the SQLite e2e lane where no postgres
// container is running. Skip the whole describe block if the container
// isn't reachable instead of erroring out per-test in beforeEach.
const postgresReachable = (() => {
  try {
    execSync(
      'docker compose -f compose.dev.yml exec -T postgres pg_isready -U embookshelf -d embookshelf',
      { cwd: PROJECT_ROOT, stdio: 'ignore' }
    )
    return true
  } catch {
    return false
  }
})()

test.beforeEach(() => {
  test.skip(!postgresReachable, 'dev Postgres container not running')
  psql(SQL_DELETE_PENDING)
  loadFixture(FIXTURE)
})

test.afterEach(() => {
  if (!postgresReachable) return
  psql(SQL_DELETE_PENDING)
})

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
