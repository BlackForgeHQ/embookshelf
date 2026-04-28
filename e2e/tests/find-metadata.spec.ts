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
    const book = await anyBook(adminApi);

    // Match any response so 500/404 surfaces clearly instead of timing out.
    const detailResp = page.waitForResponse((r) =>
      r.url().endsWith(`/api/v1/books/${book.id}`),
    );
    await page.goto(`/book/${book.id}`);
    const resp = await detailResp;
    if (!resp.ok()) {
      throw new Error(
        `GET /api/v1/books/${book.id} failed (${resp.status()}): ${await resp.text()}`,
      );
    }

    // Anchor on the always-rendered "Back to library" button — it sits
    // in the same header bar as "Find metadata online", so its presence
    // proves the header has rendered. If we time out here, dump the
    // body HTML so the actual page state surfaces.
    try {
      await expect(
        page.getByRole('button', { name: /back to library/i }),
      ).toBeVisible({ timeout: 10_000 });
    } catch (err) {
      const html = await page.content();
      throw new Error(
        `Book detail header didn't render. URL=${page.url()}\n` +
          `--- body ---\n${html.slice(0, 4000)}\n--- end ---\n` +
          (err instanceof Error ? err.message : String(err)),
      );
    }

    // Click the new header action. When it can't be clicked, dump the
    // visible body text + the labels of every button on the page so we
    // can see exactly what rendered.
    try {
      await page
        .getByRole('button', { name: /find metadata online/i })
        .click({ timeout: 10_000 });
    } catch (err) {
      const text = await page.locator('body').innerText();
      const buttons = await page
        .locator('button, a[role="button"], [role="link"]')
        .evaluateAll((els) =>
          els.map((el) => (el as HTMLElement).innerText.trim()),
        );
      throw new Error(
        `Find metadata online click failed. URL=${page.url()}\n` +
          `--- body text ---\n${text.slice(0, 4000)}\n` +
          `--- buttons/links ---\n${JSON.stringify(buttons, null, 2)}\n` +
          `--- end ---\n` +
          (err instanceof Error ? err.message : String(err)),
      );
    }

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
