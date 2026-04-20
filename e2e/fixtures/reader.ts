import type { APIRequestContext } from '@playwright/test';
import { expect } from '@playwright/test';

import { dropFixture, type DropFormat } from './bookdrop';

type BookLite = { id: string; title: string; format: string };

// ensureFixtureBook looks up a previously-approved book by title and
// format; if one exists it's reused. Otherwise it drops the fixture file
// into ./bookdrop/, waits for the BookDrop pipeline to reach 'ready',
// and approves it through the API.
//
// Caching by title across runs keeps the shared dev DB from accumulating
// test rows. DELETE /books exists now but is admin-only and destructive
// (also unlinks the file); reusing an already-approved row is still the
// cheaper path for reader fixtures.
export async function ensureFixtureBook(
  adminApi: APIRequestContext,
  opts: { format: DropFormat; title: string; dropLabel: string },
): Promise<BookLite> {
  const existing = await adminApi.get(
    `/api/v1/books?q=${encodeURIComponent(opts.title)}`,
  );
  if (existing.ok()) {
    const { books } = (await existing.json()) as { books: BookLite[] };
    const hit = books.find(
      (b) => b.title === opts.title && b.format === opts.format.toUpperCase(),
    );
    if (hit) return hit;
  }

  const { filename } = await dropFixture(opts.format, opts.dropLabel);

  const deadline = Date.now() + 20_000;
  let dropId: string | undefined;
  while (Date.now() < deadline) {
    const res = await adminApi.get('/api/v1/bookdrop');
    if (res.ok()) {
      const { items } = (await res.json()) as {
        items: { id: string; filename: string; state: string }[];
      };
      const row = items.find((i) => i.filename === filename);
      if (row?.state === 'ready') {
        dropId = row.id;
        break;
      }
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  if (!dropId) {
    throw new Error(
      `fixture ${opts.format.toUpperCase()} "${filename}" did not reach 'ready' state in time`,
    );
  }

  const approve = await adminApi.post(`/api/v1/bookdrop/${dropId}/approve`);
  expect(approve.ok()).toBeTruthy();
  const { book } = (await approve.json()) as { book: BookLite };
  return book;
}
