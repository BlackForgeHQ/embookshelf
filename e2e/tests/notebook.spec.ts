import { test, expect } from '../fixtures/api';
import { ADMIN_STATE_PATH } from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

test.describe('notebook', () => {
  test('renders page chrome', async ({ page }) => {
    await page.goto('/notebook');
    await expect(page.getByRole('heading', { name: 'Notebook' })).toBeVisible();
    await expect(
      page.getByText('Every highlight and marginalia, across every book.'),
    ).toBeVisible();
  });

  test('fetches annotations and the books list on load', async ({ page }) => {
    // The route fans out two GETs via useQueries — make sure both land
    // successfully so an empty-state render is authentic (not masking an
    // error that happened to produce the same UI).
    await page.goto('/notebook');
    await page.waitForResponse(
      (r) => r.url().endsWith('/api/v1/annotations') && r.ok(),
    );
    await page.waitForResponse(
      (r) => /\/api\/v1\/books(\?|$)/.test(r.url()) && r.ok(),
    );
  });

  test('a freshly-created annotation surfaces on the notebook page', async ({
    page,
    adminApi,
  }) => {
    // Pick any seeded book — the annotation just needs a bookId.
    const booksRes = await adminApi.get('/api/v1/books?q=Piranesi');
    expect(booksRes.ok()).toBeTruthy();
    const { books } = (await booksRes.json()) as {
      books: { id: string; title: string }[];
    };
    expect(books.length).toBeGreaterThan(0);
    const book = books[0];

    const marker = `e2e-note-${Date.now()}`;
    const createRes = await adminApi.post(
      `/api/v1/books/${book.id}/annotations`,
      {
        data: {
          selectedText: 'A fragment worth remembering',
          note: marker,
          color: 'yellow',
        },
      },
    );
    expect(createRes.ok()).toBeTruthy();
    const { annotation } = (await createRes.json()) as { annotation: { id: string } };

    try {
      await page.goto('/notebook');
      // The marker text appears on the notebook row.
      await expect(page.getByText(marker).first()).toBeVisible();
      // Book title is rendered next to the annotation row.
      await expect(page.getByText(book.title).first()).toBeVisible();
    } finally {
      const del = await adminApi.delete(`/api/v1/annotations/${annotation.id}`);
      expect([204, 404]).toContain(del.status());
    }
  });
});
