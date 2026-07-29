import type { APIRequestContext } from '@playwright/test';

import { test, expect } from '../fixtures/api';
import { ADMIN_STATE_PATH } from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

type LibBook = { id: string; title: string; author: string; format: string };

async function listBooks(api: APIRequestContext): Promise<LibBook[]> {
  const res = await api.get('/api/v1/books?limit=50');
  expect(res.ok()).toBeTruthy();
  const { books } = (await res.json()) as { books: LibBook[] };
  return books;
}

test.describe('library', () => {
  test('seeded books render on /library with a volume counter', async ({
    page,
    adminApi,
  }) => {
    const books = await listBooks(adminApi);
    test.skip(books.length === 0, 'library is empty — skipping grid render spec');

    await page.goto('/library');
    await expect(page.getByRole('heading', { name: 'All Books' })).toBeVisible();

    // Whichever title is actually in the seed must appear at least once
    // (TopBar, grid row, or filter rail count).
    const firstTitle = books[0].title;
    await expect(page.getByText(firstTitle).first()).toBeVisible();
    // Volume counter — rendered in the filter rail + footer status bar.
    await expect(page.getByText(/\d+ volumes/).first()).toBeVisible();
  });

  test('search narrows the grid to matching titles', async ({ page, adminApi }) => {
    const books = await listBooks(adminApi);
    test.skip(
      books.length < 2,
      'search narrowing needs at least two distinct titles',
    );
    const [a, b] = books;
    test.skip(a.title === b.title, 'duplicate titles — cannot differentiate');

    await page.goto('/library');
    await expect(page.getByText(a.title).first()).toBeVisible();

    // Search on the first title; the second must disappear from the grid.
    await page.getByPlaceholder('Search library…').fill(a.title);
    await expect(page.getByText(a.title).first()).toBeVisible();
    await expect(page.getByText(b.title, { exact: true })).toHaveCount(0);
  });

  test('format filter narrows the grid by format', async ({ page, adminApi }) => {
    const books = await listBooks(adminApi);
    const pdfs = books.filter((b) => b.format === 'PDF');
    const epubs = books.filter((b) => b.format === 'EPUB');
    test.skip(
      pdfs.length === 0 || epubs.length === 0,
      'need both a PDF and an EPUB in the library to exercise the filter',
    );

    await page.goto('/library');
    await expect(page.getByText(epubs[0].title).first()).toBeVisible();

    await page.getByRole('button', { name: 'PDF', exact: true }).click();

    await expect(page.getByText(pdfs[0].title).first()).toBeVisible();
    // EPUB-only title should vanish.
    await expect(page.getByText(epubs[0].title, { exact: true })).toHaveCount(0);
  });

  test('clicking a book cover navigates to its detail page', async ({
    page,
    adminApi,
  }) => {
    const books = await listBooks(adminApi);
    test.skip(books.length === 0, 'library is empty');

    await page.goto('/library');
    await page.getByText(books[0].title).first().click();
    await expect(page).toHaveURL(/\/book\/[^/]+/);
  });

  test('layout switcher toggles via the search param', async ({
    page,
    adminApi,
  }) => {
    const books = await listBooks(adminApi);
    test.skip(books.length === 0, 'library is empty');

    await page.goto('/library');
    await expect(page.getByText(books[0].title).first()).toBeVisible();

    await page.getByTitle('List', { exact: true }).click();
    await expect(page).toHaveURL(/layout=list/);

    await page.getByTitle('Shelf', { exact: true }).click();
    await expect(page).toHaveURL(/layout=shelf/);

    await page.getByTitle('Grid', { exact: true }).click();
    await expect(page).toHaveURL(/layout=grid/);
  });

  test('sidebar library filter scopes the view to a single library', async ({
    page,
    adminApi,
  }) => {
    // Pick the first library that has books; assert it narrows the grid.
    const libsRes = await adminApi.get('/api/v1/libraries');
    expect(libsRes.ok()).toBeTruthy();
    const { libraries } = (await libsRes.json()) as {
      libraries: { slug: string; name: string; bookCount: number }[];
    };
    const target = libraries.find((l) => l.bookCount > 0);
    test.skip(!target, 'no library has books — skipping filter spec');

    await page.goto('/');
    await page
      .locator('[data-sidebar="sidebar"]')
      .getByRole('link', { name: new RegExp(target!.name) })
      .first()
      .click();
    await expect(page).toHaveURL(
      new RegExp(`/library\\?.*library=${target!.slug}`),
    );
    await expect(page.getByRole('heading', { name: target!.name })).toBeVisible();
  });

  test('sort dropdown reorders via the API', async ({ page, adminApi }) => {
    const books = await listBooks(adminApi);
    test.skip(books.length === 0, 'library is empty');

    await page.goto('/library');
    await expect(page.getByText(books[0].title).first()).toBeVisible();

    // The Sort control is a shadcn Select (combobox) — open it, then
    // click the option. The chain fires a fresh /api/v1/books?sort=<key>
    // request per selection. Scope to the sort trigger so cmdk's search
    // input (also role="combobox") doesn't shadow it under strict mode.
    const trigger = page.locator('[data-slot="select-trigger"]');

    // Neither selection may be the route's default, which is `title`.
    // Choosing Title changed nothing, so react-query served the cache
    // and no request went out; the wait only ever succeeded by catching
    // the page's own initial load still in flight. That race is why this
    // spec was green locally and failed in CI (#216).
    const authorSorted = page.waitForResponse(
      (r) => /\/api\/v1\/books\?.*sort=author/.test(r.url()) && r.ok(),
    );
    await trigger.click();
    await page.getByRole('option', { name: 'Author' }).click();
    await authorSorted;

    // "Recently added" is the UI's name for the backend's `recent`.
    const recentSorted = page.waitForResponse(
      (r) => /\/api\/v1\/books\?.*sort=recent/.test(r.url()) && r.ok(),
    );
    await trigger.click();
    await page.getByRole('option', { name: 'Recently added' }).click();
    await recentSorted;
  });
});
