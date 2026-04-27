import { test, expect } from '../fixtures/api';
import { ADMIN_STATE_PATH } from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

test.describe('command palette', () => {
  test('⌘K opens the palette and Settings nav item routes to /settings', async ({ page }) => {
    await page.goto('/');
    await page.keyboard.press('ControlOrMeta+K');

    // The CommandInput placeholder identifies the palette.
    const input = page.getByPlaceholder(/search books, shelves/i);
    await expect(input).toBeVisible();

    await input.fill('settings');
    // cmdk renders items as role="option". Match the standalone Settings
    // navigation row (avoids the "Library scan (Settings → Libraries)"
    // quick action which also matches /settings/i).
    await page.getByRole('option', { name: /^settings$/i }).first().click();
    await expect(page).toHaveURL(/\/settings$/);
  });

  test('palette routes to /bookdrop via the Bookdrop nav item', async ({ page }) => {
    await page.goto('/');
    await page.keyboard.press('ControlOrMeta+K');
    const input = page.getByPlaceholder(/search books, shelves/i);
    await expect(input).toBeVisible();

    await input.fill('bookdrop');
    await page.getByRole('option', { name: /^bookdrop$/i }).first().click();
    await expect(page).toHaveURL(/\/bookdrop$/);
  });

  test('palette opens via the TopBar ⌘K button (custom event path)', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('button', { name: /open command palette/i }).click();
    await expect(page.getByPlaceholder(/search books, shelves/i)).toBeVisible();
  });
});
