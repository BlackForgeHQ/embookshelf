import { request, type APIRequestContext } from '@playwright/test';

import { test, expect } from '../fixtures/api';
import {
  ADMIN_EMAIL,
  ADMIN_PASSWORD,
  ADMIN_STATE_PATH,
  BASE_URL,
} from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

type BookLite = { id: string; title: string; author: string };

async function firstBookBy(
  adminApi: APIRequestContext,
  title: string,
): Promise<BookLite> {
  // Search API first — if the title is present we use that exact row.
  const queried = await adminApi.get(
    `/api/v1/books?q=${encodeURIComponent(title)}`,
  );
  if (queried.ok()) {
    const { books } = (await queried.json()) as { books: BookLite[] };
    if (books.length > 0) return books[0];
  }
  // Fall back to the first available book so the spec still runs when
  // the dev DB hasn't been seeded with the legacy fixture titles.
  const res = await adminApi.get('/api/v1/books?limit=1');
  expect(res.ok()).toBeTruthy();
  const { books } = (await res.json()) as { books: BookLite[] };
  expect(books.length).toBeGreaterThan(0);
  return books[0];
}

// restoreTitle uses a fresh request context so cleanup survives a test
// that hits its timeout — the per-test adminApi fixture is torn down on
// timeout, which silently breaks a try/finally cleanup.
async function restoreTitle(bookId: string, title: string): Promise<void> {
  const ctx = await request.newContext({
    baseURL: BASE_URL,
    extraHTTPHeaders: { Origin: BASE_URL },
  });
  try {
    await ctx.post('/api/v1/auth/login', {
      data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
    });
    const res = await ctx.patch(`/api/v1/books/${bookId}`, { data: { title } });
    expect(res.ok()).toBeTruthy();
  } finally {
    await ctx.dispose();
  }
}

test.describe('book detail', () => {
  test('renders seeded book with title, author, and action buttons', async ({
    page,
    adminApi,
  }) => {
    const book = await firstBookBy(adminApi, 'Piranesi');

    await page.goto(`/book/${book.id}`);

    await expect(page.getByRole('heading', { name: book.title })).toBeVisible();
    await expect(page.getByText(`by ${book.author}`)).toBeVisible();
    await expect(page.getByRole('button', { name: 'Edit metadata' })).toBeVisible();
    await expect(page.getByRole('button', { name: /Open book|Continue reading/ })).toBeVisible();
  });

  test('overview meta rows mirror the book fields', async ({ page, adminApi }) => {
    const book = await firstBookBy(adminApi, 'Piranesi');
    await page.goto(`/book/${book.id}`);

    // The overview tab is the default — Meta rows render label/value pairs.
    const overviewPanel = page.locator('main');
    await expect(overviewPanel.getByText('Title', { exact: true })).toBeVisible();
    await expect(overviewPanel.getByText('Author', { exact: true })).toBeVisible();
    await expect(overviewPanel.getByText('Format', { exact: true })).toBeVisible();
    // And the actual value for Author shows up in the right column too.
    await expect(overviewPanel.getByText(book.author, { exact: true }).first()).toBeVisible();
  });

  test('back-to-library button navigates to /library', async ({ page, adminApi }) => {
    const book = await firstBookBy(adminApi, 'Piranesi');
    await page.goto(`/book/${book.id}`);

    await page.getByRole('button', { name: 'Back to library' }).click();
    await expect(page).toHaveURL(/\/library$/);
  });

  test('shelf picker adds a book to a shelf and removes it via the chip', async ({
    page,
    adminApi,
  }) => {
    test.setTimeout(30_000);

    // Pick a book that we know is NOT already on the currently-reading
    // shelf in the seed (to-read has "The Tombs of Atuan" etc.). Using
    // a dedicated test shelf keeps this spec isolated from the seed's
    // shelf memberships — plus the sidebar would otherwise mutate and
    // break sibling specs' strict-mode locators.
    const shelfName = `e2e-toggle-${Date.now()}`;
    const shelfRes = await adminApi.post('/api/v1/shelves', {
      data: { name: shelfName },
    });
    expect(shelfRes.ok()).toBeTruthy();
    const { shelf } = (await shelfRes.json()) as { shelf: { slug: string; name: string } };

    const book = await firstBookBy(adminApi, 'Piranesi');

    try {
      await page.goto(`/book/${book.id}`);
      const shelfCard = page.locator('main').getByText('Shelves', { exact: true }).locator('..');

      // Open the picker.
      await shelfCard.getByRole('button', { name: /Add/ }).click();
      // The test shelf appears in the picker since the book isn't on it.
      await shelfCard.getByRole('button', { name: shelf.name }).click();

      // After add, the chip for the shelf shows up with a close icon.
      const chip = page.getByRole('button', { name: new RegExp(shelf.name) });
      await expect(chip).toBeVisible();

      // API should reflect membership now.
      const after = await adminApi.get(`/api/v1/books/${book.id}`);
      const { book: enriched } = (await after.json()) as {
        book: { shelves: string[] };
      };
      expect(enriched.shelves).toContain(shelf.slug);

      // Click the chip to remove from shelf.
      await chip.click();
      await expect(chip).toHaveCount(0);
    } finally {
      await adminApi.delete(`/api/v1/shelves/${encodeURIComponent(shelf.slug)}`);
    }
  });

  test('edit metadata round-trip: change title, save, then restore', async ({
    page,
    adminApi,
  }) => {
    const book = await firstBookBy(adminApi, 'Blindsight');
    const originalTitle = book.title;
    const mutatedTitle = `${originalTitle}-e2e-${Date.now()}`;

    let mutated = false;
    try {
      await page.goto(`/book/${book.id}`);
      await page.getByRole('button', { name: 'Edit metadata' }).click();
      await page.waitForURL(new RegExp(`/book/${book.id}/edit$`));

      // Editor rows use a plain <div> for the label, so getByLabel doesn't
      // resolve. The Title input is the first textbox on the form — assert
      // its current value before mutating so we fail fast if the structure
      // changes.
      const titleInput = page.getByRole('textbox').first();
      await expect(titleInput).toHaveValue(originalTitle);

      await titleInput.fill(mutatedTitle);
      mutated = true;

      await page.getByRole('button', { name: 'Save changes' }).click();
      await page.waitForURL(new RegExp(`/book/${book.id}$`));
      await expect(
        page.getByRole('heading', { name: mutatedTitle }),
      ).toBeVisible({ timeout: 10_000 });
    } finally {
      if (mutated) await restoreTitle(book.id, originalTitle);
    }
  });
});
