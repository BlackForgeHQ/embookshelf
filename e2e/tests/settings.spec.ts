import { test, expect } from '@playwright/test';

import { ADMIN_EMAIL, ADMIN_STATE_PATH } from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

test.describe('settings', () => {
  test('account panel renders the seeded admin identity by default', async ({ page }) => {
    await page.goto('/settings');

    await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Account' })).toBeVisible();
    // Admin seed is admin@local with role "admin".
    await expect(page.getByText(ADMIN_EMAIL, { exact: false })).toBeVisible();
    await expect(page.getByText(/·\s*Admin\s*·/)).toBeVisible();
  });

  test('switching to Libraries shows the registered library roots', async ({ page }) => {
    await page.goto('/settings');

    await page.getByRole('button', { name: 'Libraries', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Libraries' })).toBeVisible();
    // Scope to the main content area — "Main" also appears as a sidebar
    // link, which would trip strict-mode with an unqualified text match.
    await expect(
      page.getByRole('main').getByText('Main', { exact: true }),
    ).toBeVisible();
  });
});
