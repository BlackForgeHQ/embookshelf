import { test, expect } from '@playwright/test';

import { ADMIN_STATE_PATH } from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

test.describe('library · a11y', () => {
  test('layout toggle buttons expose aria-pressed matching the active layout', async ({
    page,
  }) => {
    await page.goto('/library?layout=grid');
    // Wait for the library route to settle — the heading is always there.
    await expect(page.getByRole('heading', { name: 'All Books' })).toBeVisible();

    const gridBtn = page.getByRole('button', { name: 'Grid layout' });
    const listBtn = page.getByRole('button', { name: 'List layout' });
    const shelfBtn = page.getByRole('button', { name: 'Shelf layout' });

    await expect(gridBtn).toHaveAttribute('aria-pressed', 'true');
    await expect(listBtn).toHaveAttribute('aria-pressed', 'false');
    await expect(shelfBtn).toHaveAttribute('aria-pressed', 'false');

    await listBtn.click();
    await expect(page).toHaveURL(/layout=list/);
    await expect(listBtn).toHaveAttribute('aria-pressed', 'true');
    await expect(gridBtn).toHaveAttribute('aria-pressed', 'false');
  });

  test('book tiles render as <button> with an Open-<title> aria-label', async ({
    page,
  }) => {
    await page.goto('/library?layout=grid');
    await expect(page.getByRole('heading', { name: 'All Books' })).toBeVisible();

    // `aria-label="Open <title>"` — match any. If the library is empty,
    // skip so this spec doesn't depend on a specific seed.
    const tiles = page.getByRole('button', { name: /^Open / });
    const count = await tiles.count();
    test.skip(count === 0, 'library has no books — nothing to assert on');

    await expect(tiles.first()).toBeVisible();
    await tiles.first().click();
    await expect(page).toHaveURL(/\/book\/[^/]+$/);
  });
});
