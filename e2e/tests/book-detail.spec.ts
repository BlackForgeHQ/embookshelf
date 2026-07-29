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

      // Anchored on the trigger's own aria-label rather than by walking
      // up from the "Shelves" text. That walk reached the card's header
      // row — label plus counts — once the header became its own flex
      // container, so the buttons underneath were out of scope and the
      // picker never opened (#216). "Shelves" also appears in the
      // sidebar, which the `main` scope was there to dodge.
      await page.getByRole('button', { name: 'Add to shelf' }).click();

      // The picker is a Radix popover holding a cmdk list, so its rows
      // are options rather than buttons.
      const picker = page.getByRole('dialog');
      await picker.getByRole('option', { name: shelf.name }).click();

      // Dismiss the picker before touching the chip behind it.
      await page.keyboard.press('Escape');

      // Exact, because the sidebar renders a "Share <shelf> with all
      // users" button for the same shelf and a substring match claims
      // both. Whether the sidebar had refetched by this point decided
      // whether the spec passed, which is what made it flaky in a full
      // run and green on its own (#216).
      const chip = page
        .locator('main')
        .getByRole('button', { name: shelf.name, exact: true });
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
    // Default 30s isn't enough headroom for cold-start SQLite lanes; the
    // restoreTitle finally also needs budget to log in fresh.
    test.setTimeout(60_000);

    const book = await firstBookBy(adminApi, 'Blindsight');
    const originalTitle = book.title;
    const mutatedTitle = `${originalTitle}-e2e-${Date.now()}`;

    let mutated = false;
    try {
      await test.step('open book detail', async () => {
        const detailResp = page.waitForResponse(
          (r) => r.url().endsWith(`/api/v1/books/${book.id}`),
          { timeout: 15_000 },
        );
        await page.goto(`/book/${book.id}`);
        const resp = await detailResp;
        if (!resp.ok()) {
          throw new Error(
            `GET /api/v1/books/${book.id} failed (${resp.status()}): ${await resp.text()}`,
          );
        }

        // Anchor on "Back to library" — sits in the same header as
        // "Edit metadata" and proves the page is past Loading/Error.
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
      });

      await test.step('navigate into edit page', async () => {
        await page.getByRole('button', { name: 'Edit metadata' }).click();
        await page.waitForURL(new RegExp(`/book/${book.id}/edit$`), {
          timeout: 10_000,
        });
      });

      await test.step('mutate title', async () => {
        const titleInput = page.getByRole('textbox').first();
        await expect(titleInput).toHaveValue(originalTitle, { timeout: 10_000 });
        await titleInput.fill(mutatedTitle);
        mutated = true;
      });

      await test.step('save and assert redirect', async () => {
        const saveResp = page.waitForResponse(
          (r) =>
            r.url().endsWith(`/api/v1/books/${book.id}`) &&
            r.request().method() === 'PATCH',
          { timeout: 15_000 },
        );
        // The editor renders Save changes in both the sticky header and
        // the sticky footer save bar. Both share onSave, so picking the
        // first one is fine — and avoids ARIA-role footer scoping which
        // breaks once the footer is nested inside a <main>.
        await page
          .getByRole('button', { name: 'Save changes' })
          .first()
          .click();
        const patch = await saveResp;
        if (!patch.ok()) {
          throw new Error(
            `PATCH /api/v1/books/${book.id} failed (${patch.status()}): ${await patch.text()}`,
          );
        }
        await page.waitForURL(new RegExp(`/book/${book.id}$`), {
          timeout: 10_000,
        });
        await expect(
          page.getByRole('heading', { name: mutatedTitle }),
        ).toBeVisible({ timeout: 10_000 });
      });
    } finally {
      if (mutated) await restoreTitle(book.id, originalTitle);
    }
  });
});
