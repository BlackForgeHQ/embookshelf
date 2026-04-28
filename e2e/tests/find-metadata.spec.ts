import type { APIRequestContext } from '@playwright/test';

import { test, expect } from '../fixtures/api';
import { ADMIN_STATE_PATH } from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

async function anyBook(
  adminApi: APIRequestContext,
): Promise<{ id: string; title: string; author: string }> {
  const res = await adminApi.get('/api/v1/books?limit=1');
  expect(res.ok()).toBeTruthy();
  const { books } = (await res.json()) as {
    books: { id: string; title: string; author: string }[];
  };
  expect(books.length).toBeGreaterThan(0);
  return books[0];
}

test.describe('book find · dedicated enrichment page', () => {
  test('opens the find page from book detail and renders search rail + provider chips', async ({
    page,
    adminApi,
  }) => {
    test.setTimeout(20_000);
    const book = await anyBook(adminApi);

    await page.goto(`/book/${book.id}`);

    // Click the new header action.
    await page.getByRole('button', { name: /find metadata online/i }).click();

    await expect(page).toHaveURL(new RegExp(`/book/${book.id}/find$`));

    // Heading and book subtitle line.
    await expect(
      page.getByRole('heading', { name: /find metadata online/i }),
    ).toBeVisible();

    // Search inputs are pre-filled (Title input is the first textbox in
    // the search rail).
    const titleInput = page.getByRole('textbox').first();
    await expect(titleInput).toHaveValue(book.title);

    // At least one provider chip renders (could be active/done/disabled).
    await expect(page.getByText('Google Books').first()).toBeVisible();

    // "Search again" control exists.
    await expect(
      page.getByRole('button', { name: /search again|searching/i }),
    ).toBeVisible();

    // Back-to-edit link returns to the edit route.
    await page.getByRole('link', { name: /back to edit/i }).click();
    await expect(page).toHaveURL(new RegExp(`/book/${book.id}/edit$`));
  });
});
