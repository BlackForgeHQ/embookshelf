import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { test, expect, request, type APIRequestContext } from '@playwright/test'

import { ADMIN_EMAIL, ADMIN_PASSWORD, BASE_URL } from '../fixtures/constants'
import { loadSqlFixture, postgresReachable, psql } from '../fixtures/psql'

// Pins OPDS paging across a page boundary (#241): 55 seeded books at
// 50/page must yield a 50-entry page 1 with a next link and a 5-entry
// page 2 with a previous link, with no entry repeated or skipped at the
// boundary. Before the catalog owned paging, the feed sliced an
// in-memory list whose per-library ordering made the boundary
// unstable.

const __filename = fileURLToPath(import.meta.url)
const __dirname = resolve(__filename, '..')
const FIXTURE = resolve(__dirname, '../fixtures/sql/opds-paging-books.sql')

const SQL_CLEANUP = `
DELETE FROM books
  WHERE library_id IN (SELECT id FROM libraries WHERE slug = 'e2e-opds-paging');
DELETE FROM libraries WHERE slug = 'e2e-opds-paging';
`

const FEED_PATH = '/opds/library/e2e-opds-paging'

function entryTitles(feedXml: string): string[] {
  // The <title> of the feed itself comes before any <entry>; only titles
  // inside entries count.
  return [...feedXml.matchAll(/<entry>[\s\S]*?<title>([^<]*)<\/title>/g)].map(
    (m) => m[1]
  )
}

test.describe('opds paging', () => {
  let ctx: APIRequestContext

  test.beforeEach(async () => {
    test.skip(!postgresReachable, 'dev Postgres container not running')
    psql(SQL_CLEANUP)
    loadSqlFixture(FIXTURE)
    ctx = await request.newContext({
      baseURL: BASE_URL,
      httpCredentials: { username: ADMIN_EMAIL, password: ADMIN_PASSWORD },
    })
  })

  test.afterEach(async () => {
    if (postgresReachable) psql(SQL_CLEANUP)
    await ctx?.dispose()
  })

  test('a 55-book library pages 50 + 5 with links across the boundary', async () => {
    const page1 = await ctx.get(FEED_PATH)
    expect(page1.ok()).toBeTruthy()
    const body1 = await page1.text()
    const titles1 = entryTitles(body1)

    expect(titles1).toHaveLength(50)
    expect(titles1[0]).toBe('e2e-opds-page-000')
    expect(titles1[49]).toBe('e2e-opds-page-049')
    expect(body1).toContain('rel="next"')
    expect(body1).not.toContain('rel="previous"')

    const page2 = await ctx.get(`${FEED_PATH}?page=2`)
    expect(page2.ok()).toBeTruthy()
    const body2 = await page2.text()
    const titles2 = entryTitles(body2)

    // The boundary neither repeats 049 nor skips 050.
    expect(titles2).toHaveLength(5)
    expect(titles2[0]).toBe('e2e-opds-page-050')
    expect(titles2[4]).toBe('e2e-opds-page-054')
    expect(body2).toContain('rel="previous"')
    expect(body2).not.toContain('rel="next"')
  })
})
