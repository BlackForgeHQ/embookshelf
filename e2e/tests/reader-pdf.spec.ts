import { test, expect } from '../fixtures/api';
import { ADMIN_STATE_PATH } from '../fixtures/constants';
import { ensureFixtureBook } from '../fixtures/reader';

test.use({ storageState: ADMIN_STATE_PATH });

const FIXTURE_TITLE = 'E2E PDF Sample';

test.describe('reader-pdf', () => {
  test('reader chrome renders the PDF title, author, and page counter', async ({
    page,
    adminApi,
  }) => {
    test.setTimeout(45_000);

    const book = await ensureFixtureBook(adminApi, {
      format: 'pdf',
      title: FIXTURE_TITLE,
      dropLabel: 'reader-pdf',
    });
    expect(book.format).toBe('PDF');

    await page.goto(`/read/${book.id}`);

    await expect(page.getByText(FIXTURE_TITLE, { exact: false }).first()).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.getByText('E2E Author', { exact: false }).first()).toBeVisible();
    // Footer subline is "<author> · p.N / p.M" for PDFs. The fixture is a
    // single-page document, so once pdfjs has loaded we expect p.1 / p.1.
    await expect(page.getByText(/p\.1\s*\/\s*p\.1/)).toBeVisible({
      timeout: 15_000,
    });
  });

  test('pdfjs renders the first page into a canvas', async ({
    page,
    adminApi,
  }) => {
    test.setTimeout(45_000);

    const book = await ensureFixtureBook(adminApi, {
      format: 'pdf',
      title: FIXTURE_TITLE,
      dropLabel: 'reader-pdf',
    });

    await page.goto(`/read/${book.id}`);

    // Wait for the chrome so we know the reader shell has booted.
    await expect(page.getByRole('button', { name: /Library/ })).toBeVisible({
      timeout: 15_000,
    });

    // "Loading PDF…" is the pre-pdfjs placeholder (PdfReader.tsx:150).
    // Once pdfjs resolves the document and renders page one, the message
    // goes away and a canvas is mounted with a data-page attribute.
    await expect(page.getByText('Loading PDF…')).toBeHidden({ timeout: 15_000 });
    const canvas = page.locator('canvas[data-page="1"], [data-page="1"] canvas');
    await expect(canvas.first()).toBeVisible({ timeout: 15_000 });

    // Non-zero paint dimensions confirm pdfjs painted a page, not an
    // empty canvas placeholder.
    const painted = await canvas.first().evaluate((el: HTMLCanvasElement | HTMLElement) => {
      const c =
        el instanceof HTMLCanvasElement
          ? el
          : (el.querySelector('canvas') as HTMLCanvasElement | null);
      return c ? { w: c.width, h: c.height } : { w: 0, h: 0 };
    });
    expect(painted.w).toBeGreaterThan(0);
    expect(painted.h).toBeGreaterThan(0);
  });

  test('Library button exits the PDF reader back to the book detail page', async ({
    page,
    adminApi,
  }) => {
    test.setTimeout(45_000);

    const book = await ensureFixtureBook(adminApi, {
      format: 'pdf',
      title: FIXTURE_TITLE,
      dropLabel: 'reader-pdf',
    });
    await page.goto(`/read/${book.id}`);

    const libraryBtn = page.getByRole('button', { name: /Library/ });
    await expect(libraryBtn).toBeVisible({ timeout: 15_000 });
    await libraryBtn.click();

    await expect(page).toHaveURL(new RegExp(`/book/${book.id}$`));
  });
});
