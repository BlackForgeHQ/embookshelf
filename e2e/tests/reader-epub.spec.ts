import { test, expect } from '../fixtures/api';
import { ADMIN_STATE_PATH } from '../fixtures/constants';
import { ensureFixtureBook } from '../fixtures/reader';

test.use({ storageState: ADMIN_STATE_PATH });

const FIXTURE_TITLE = 'E2E Sample Book';

test.describe('reader-epub', () => {
  test('reader chrome renders the book title and exit button', async ({
    page,
    adminApi,
  }) => {
    test.setTimeout(45_000);

    const book = await ensureFixtureBook(adminApi, {
      format: 'epub',
      title: FIXTURE_TITLE,
      dropLabel: 'reader',
    });

    await page.goto(`/read/${book.id}`);

    await expect(page.getByText(FIXTURE_TITLE, { exact: false }).first()).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.getByText('E2E Author', { exact: false }).first()).toBeVisible();
    await expect(page.getByRole('button', { name: /Library/ })).toBeVisible();
  });

  test('Library button exits the reader back to the book detail page', async ({
    page,
    adminApi,
  }) => {
    test.setTimeout(45_000);

    const book = await ensureFixtureBook(adminApi, {
      format: 'epub',
      title: FIXTURE_TITLE,
      dropLabel: 'reader',
    });
    await page.goto(`/read/${book.id}`);

    // Despite the label, exit() navigates to /book/$id (see
    // read.$id.tsx:198) — the button goes back to detail, not library.
    const libraryBtn = page.getByRole('button', { name: /Library/ });
    await expect(libraryBtn).toBeVisible({ timeout: 15_000 });
    await libraryBtn.click();

    await expect(page).toHaveURL(new RegExp(`/book/${book.id}$`));
  });
});
