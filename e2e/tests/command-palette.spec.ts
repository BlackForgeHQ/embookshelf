import { test, expect } from '../fixtures/api';
import { ADMIN_STATE_PATH } from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

test.describe('command palette', () => {
  // The window-level keydown listener is attached inside AppLayout's
  // useEffect, so a Ctrl+K press fired before hydration completes is a
  // no-op. Wait for the TopBar's "open command palette" button — its
  // presence confirms the layout has rendered and the listener is live.
  async function openViaShortcut(page: import('@playwright/test').Page) {
    await page.goto('/');
    await expect(
      page.getByRole('button', { name: /open command palette/i }),
    ).toBeVisible();
    await page.keyboard.press('ControlOrMeta+K');
    await expect(page.getByPlaceholder(/search books, shelves/i)).toBeVisible();
  }

  test('⌘K opens the palette and Settings nav item routes to /settings', async ({ page }) => {
    await openViaShortcut(page);

    await page.getByPlaceholder(/search books, shelves/i).fill('settings');
    // cmdk renders items as role="option". Match the Settings nav row
    // by exact name so partial matches in search results don't shadow it.
    await page.getByRole('option', { name: /^settings$/i }).first().click();
    await expect(page).toHaveURL(/\/settings$/);
  });

  test('palette routes to /bookdrop via the Bookdrop nav item', async ({ page }) => {
    await openViaShortcut(page);

    await page.getByPlaceholder(/search books, shelves/i).fill('bookdrop');
    await page.getByRole('option', { name: /^bookdrop$/i }).first().click();
    await expect(page).toHaveURL(/\/bookdrop$/);
  });

  test('palette opens via the TopBar ⌘K button (custom event path)', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('button', { name: /open command palette/i }).click();
    await expect(page.getByPlaceholder(/search books, shelves/i)).toBeVisible();
  });
});
