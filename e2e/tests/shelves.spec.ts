import type { APIRequestContext } from '@playwright/test';

import { test, expect } from '../fixtures/api';
import { ADMIN_STATE_PATH } from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

async function deleteShelfBySlug(api: APIRequestContext, slug: string): Promise<void> {
  const res = await api.delete(`/api/v1/shelves/${encodeURIComponent(slug)}`);
  // 204 on success, 404 if already gone.
  expect([204, 404]).toContain(res.status());
}

test.describe('shelves', () => {
  test('sidebar lists seeded shelves and they link to the filtered library', async ({
    page,
  }) => {
    await page.goto('/');

    const sidebar = page.locator('[data-sidebar="sidebar"]');
    // Seeded shelves from scripts/seed.sql.
    await expect(sidebar.getByRole('link', { name: /Reading Now/ })).toBeVisible();
    await expect(sidebar.getByRole('link', { name: /To read/ })).toBeVisible();
    await expect(sidebar.getByRole('link', { name: /Favorites/ })).toBeVisible();

    await sidebar.getByRole('link', { name: /Favorites/ }).click();
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
