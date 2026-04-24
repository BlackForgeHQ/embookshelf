import { test, expect } from '../fixtures/api';
import { ADMIN_STATE_PATH } from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

test.describe('dashboard', () => {
  test('renders greeting heading and key sections', async ({ page }) => {
    await page.goto('/');

    // Greeting: "Morning, Admin." / "Afternoon, Admin." / etc. The exact
    // time-of-day word depends on CI clock — so match on the trailing name.
    await expect(page.getByRole('heading', { name: /, Admin\.$/ })).toBeVisible();

    // Core sections on the dashboard.
    await expect(page.getByRole('heading', { name: 'Currently reading' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Reading activity' })).toBeVisible();
  });

  test('reading activity stat cards render labels regardless of session data', async ({
    page,
  }) => {
    await page.goto('/');

    // These labels are always in the DOM; values depend on recorded sessions.
    await expect(page.getByText('This week', { exact: true })).toBeVisible();
    await expect(page.getByText('Current streak', { exact: true })).toBeVisible();
    await expect(page.getByText('This quarter', { exact: true })).toBeVisible();
    await expect(page.getByText('All time', { exact: true })).toBeVisible();
  });

  test('clicking All Books in the sidebar navigates to /library', async ({ page }) => {
    await page.goto('/');

    // Scoped to the shadcn Sidebar primitive — it renders as a <div>
    // with data-sidebar="sidebar", not <aside>.
    await page
      .locator('[data-sidebar="sidebar"]')
      .getByRole('link', { name: /All Books/ })
      .first()
      .click();
    await expect(page).toHaveURL(/\/library$/);
    await expect(page.getByRole('heading', { name: 'All Books' })).toBeVisible();
  });
});
