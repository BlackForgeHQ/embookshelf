import { test, expect } from '@playwright/test';

import { ADMIN_STATE_PATH } from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

test.describe('stats', () => {
  test('renders the Statistics heading with subtitle', async ({ page }) => {
    await page.goto('/stats');
    await expect(page.getByRole('heading', { name: 'Statistics' })).toBeVisible();
    await expect(
      page.getByText('A view of your collection: size, shape, and texture.'),
    ).toBeVisible();
  });

  test('headline tiles for the seeded library render', async ({ page }) => {
    await page.goto('/stats');

    // Tile labels are static regardless of data; values follow the seed.
    await expect(page.getByText('Books', { exact: true })).toBeVisible();
    await expect(page.getByText('Covers', { exact: true })).toBeVisible();
    await expect(page.getByText('Reading', { exact: true })).toBeVisible();
    await expect(page.getByText('Finished', { exact: true })).toBeVisible();
    await expect(page.getByText('Annotations', { exact: true })).toBeVisible();
  });
});
