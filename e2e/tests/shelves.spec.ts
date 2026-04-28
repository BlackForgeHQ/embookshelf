import type { APIRequestContext } from '@playwright/test';

import { test, expect } from '../fixtures/api';
import { ADMIN_STATE_PATH } from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

async function deleteShelfBySlug(api: APIRequestContext, slug: string): Promise<void> {
  const res = await api.delete(`/api/v1/shelves/${encodeURIComponent(slug)}`);
  // 204 on success, 404 if already gone.
  expect([204, 404]).toContain(res.status());
}

// Idempotently ensure a shelf with the given name exists. The Postgres
// seed lane preloads these via scripts/seed.sql; the SQLite e2e lane
// runs against a fresh DB with only the bootstrap admin, so the test
// creates them on demand.
//
// We list first and skip when a shelf with the same name already exists.
// Posting blindly is not idempotent — the shelf-create repo loop
// auto-suffixes the slug on conflict (`to-read` → `to-read-2`) and
// returns 201, so a re-run would accumulate duplicate sidebar entries.
//
// Note: "Reading Now" is intentionally NOT created here — the sidebar
// hardcodes a pinned row for it (Sidebar.tsx) and a dynamic shelf with
// the same name would auto-slug to "reading-now" and produce a duplicate
// link. The pin renders regardless of whether the shelf exists.
async function ensureShelf(api: APIRequestContext, name: string): Promise<void> {
  const list = await api.get('/api/v1/shelves');
  expect(list.ok()).toBeTruthy();
  const { shelves } = (await list.json()) as {
    shelves: { name: string; slug: string }[];
  };
  const matches = shelves.filter((s) => s.name === name);
  // Earlier broken runs (before the repo Create-loop fix) left duplicate
  // shelves with auto-suffixed slugs (`to-read-2`, `to-read-3`, …).
  // Drop them so strict-mode locators don't trip on the duplicates.
  for (const dup of matches.slice(1)) {
    await api.delete(`/api/v1/shelves/${encodeURIComponent(dup.slug)}`);
  }
  if (matches.length > 0) return;
  const res = await api.post('/api/v1/shelves', { data: { name } });
  expect([200, 201]).toContain(res.status());
}

test.describe('shelves', () => {
  test('sidebar lists seeded shelves and they link to the filtered library', async ({
    page,
    adminApi,
  }) => {
    await ensureShelf(adminApi, 'To read');
    await ensureShelf(adminApi, 'Favorites');

    await page.goto('/');

    const sidebar = page.locator('[data-sidebar="sidebar"]');
    // Reading Now is the hardcoded pinned row in the sidebar — it
    // always renders, regardless of seed state.
    await expect(
      sidebar.getByRole('link', { name: /Reading Now/ }).first(),
    ).toBeVisible();
    // Tolerate duplicates that pre-date the repo Create-loop fix.
    // The test's intent is "the seeded shelves render", not "exactly
    // one row each".
    await expect(
      sidebar.getByRole('link', { name: /To read/ }).first(),
    ).toBeVisible();
    await expect(
      sidebar.getByRole('link', { name: /Favorites/ }).first(),
    ).toBeVisible();

    // Click the canonical Favorites row by exact href so we don't pick
    // up an auto-suffixed duplicate (`shelf=favorites-2`) if one exists.
    await sidebar.locator('a[href="/library?shelf=favorites"]').click();
    await expect(page).toHaveURL(/\/library\?.*shelf=favorites/);
    await expect(page.getByRole('heading', { name: 'Favorites' })).toBeVisible();
  });

  test('creating a shelf via the API surfaces it in the sidebar after a refetch', async ({
    page,
    adminApi,
  }) => {
    const shelfName = `e2e-${Date.now()}`;
    const createRes = await adminApi.post('/api/v1/shelves', { data: { name: shelfName } });
    expect(createRes.ok()).toBeTruthy();
    const { shelf } = (await createRes.json()) as { shelf: { slug: string } };

    try {
      await page.goto('/');

      const sidebar = page.locator('[data-sidebar="sidebar"]');
      await expect(sidebar.getByRole('link', { name: new RegExp(shelfName) })).toBeVisible();

      await sidebar.getByRole('link', { name: new RegExp(shelfName) }).click();
      await expect(page).toHaveURL(new RegExp(`/library\\?.*shelf=${shelf.slug}`));
      await expect(page.getByRole('heading', { name: shelfName })).toBeVisible();
    } finally {
      await deleteShelfBySlug(adminApi, shelf.slug);
    }
  });
});
