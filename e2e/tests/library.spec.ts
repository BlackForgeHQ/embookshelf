import { test, expect } from '@playwright/test';

import { ADMIN_STATE_PATH } from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

test.describe('library', () => {
  test('seeded books render on /library with a volume counter', async ({ page }) => {
    await page.goto('/library');

    await expect(page.getByRole('heading', { name: 'All Books' })).toBeVisible();
    await expect(page.getByText('Piranesi').first()).toBeVisible();
    await expect(page.getByText('The Name of the Rose').first()).toBeVisible();
    // The filter rail counter — the footer status bar has a hardcoded
    // "3 libraries · 1,202 volumes" so we match the first occurrence.
    await expect(page.getByText(/\d+ volumes/).first()).toBeVisible();
  });

  test('search narrows the grid to matching titles', async ({ page }) => {
    await page.goto('/library');
    // Wait for initial results so the test isn't racing an empty state.
    await expect(page.getByText('The Name of the Rose').first()).toBeVisible();

    await page.getByPlaceholder('Search library…').fill('Piranesi');

    await expect(page.getByText('Piranesi').first()).toBeVisible();
    // Poll until the non-matching titles are gone. toBeHidden trips strict
    // mode when multiple matches exist; toHaveCount(0) retries.
    await expect(page.getByText('The Name of the Rose')).toHaveCount(0);
  });

  test('format filter narrows the grid by format', async ({ page }) => {
    await page.goto('/library');
    await expect(page.getByText('Piranesi').first()).toBeVisible();

    // Seed has two PDFs: "Gödel, Escher, Bach" and "House of Leaves".
    await page.getByRole('button', { name: 'PDF', exact: true }).click();

    await expect(page.getByText('Gödel, Escher, Bach').first()).toBeVisible();
    await expect(page.getByText('House of Leaves').first()).toBeVisible();
    // An EPUB title should no longer be visible.
    await expect(page.getByText('Piranesi')).toHaveCount(0);
  });

  test('clicking a book cover navigates to its detail page', async ({ page }) => {
    await page.goto('/library');
    await page.getByText('Piranesi').first().click();
    await expect(page).toHaveURL(/\/book\/[^/]+/);
  });

  test('layout switcher toggles via the search param', async ({ page }) => {
    await page.goto('/library');
    await expect(page.getByText('Piranesi').first()).toBeVisible();

    // Buttons use an icon + title attribute; getByTitle pulls them out
    // reliably even though they have no visible text.
    await page.getByTitle('List', { exact: true }).click();
    await expect(page).toHaveURL(/layout=list/);

    await page.getByTitle('Shelf', { exact: true }).click();
    await expect(page).toHaveURL(/layout=shelf/);

    await page.getByTitle('Grid', { exact: true }).click();
    await expect(page).toHaveURL(/layout=grid/);
  });

  test('sidebar library filter scopes the view to a single library', async ({
    page,
  }) => {
    await page.goto('/');
    // Seeded libraries are Main / Comics / Audiobooks; only Main has
    // books, so clicking it narrows the grid but keeps books visible.
    await page.locator('aside').getByRole('link', { name: /Main/ }).click();
    await expect(page).toHaveURL(/\/library\?.*library=main/);
    await expect(page.getByRole('heading', { name: 'Main' })).toBeVisible();
    await expect(page.getByText('Piranesi').first()).toBeVisible();
  });

  test('sort dropdown reorders via the API', async ({ page }) => {
    await page.goto('/library');
    await expect(page.getByText('Piranesi').first()).toBeVisible();

    // The Sort select fires a fresh /api/v1/books?sort=<api-key> request
    // on change — wait for the response to confirm the query took.
    const sorted = page.waitForResponse(
      (r) => /\/api\/v1\/books\?.*sort=title/.test(r.url()) && r.ok(),
    );
    await page.locator('select.input').selectOption('title');
    await sorted;

    const authorSorted = page.waitForResponse(
      (r) => /\/api\/v1\/books\?.*sort=author/.test(r.url()) && r.ok(),
    );
    await page.locator('select.input').selectOption('author');
    await authorSorted;
  });
});
