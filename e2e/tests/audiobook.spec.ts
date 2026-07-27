import type { APIRequestContext } from '@playwright/test';

import { test, expect } from '../fixtures/api';
import { ADMIN_STATE_PATH } from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

// Narration (ADRs 0025–0028) is admin-only and costs real money, so
// these specs never start a run. They cover the surfaces an operator
// reaches before spending anything: that the panels render, that the
// catalog reaches the client intact, and that the format gate and the
// confirm step actually stand between a click and a bill.

type BookLite = { id: string; title: string; format: string };

async function books(adminApi: APIRequestContext): Promise<BookLite[]> {
  const res = await adminApi.get('/api/v1/books?limit=50');
  expect(res.ok()).toBeTruthy();
  const { books: rows } = (await res.json()) as { books: BookLite[] };
  return rows;
}

async function bookOfFormat(
  adminApi: APIRequestContext,
  predicate: (b: BookLite) => boolean,
): Promise<BookLite | null> {
  return (await books(adminApi)).find(predicate) ?? null;
}

async function openNarrationTab(page: import('@playwright/test').Page, id: string) {
  await page.goto(`/book/${id}`);
  await page.getByRole('tab', { name: 'Narration' }).click();
}

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

test('audiobook settings panel renders the whole engine catalog', async ({ page }) => {
  await page.goto('/settings');
  await page.getByRole('button', { name: 'Audiobooks', exact: true }).click();

  const main = page.getByRole('main');
  await expect(
    main.getByRole('heading', { name: 'Audiobook narration' }),
  ).toBeVisible();

  // All three engines reach the client. A missing card means the catalog
  // literal and the DTO have drifted apart.
  for (const label of ['OpenAI-compatible', 'ElevenLabs', 'Azure Speech']) {
    await expect(main.getByRole('heading', { name: new RegExp(label) })).toBeVisible();
  }

  // The panel has to say the thing an operator will otherwise discover
  // by hunting for a button that does not exist.
  await expect(main.getByText(/no bulk run/i)).toBeVisible();
});

test('a stored engine key is never sent back to the browser', async ({
  page,
  adminApi,
}) => {
  // Write a key through the API, then read the settings the panel reads.
  const put = await adminApi.put('/api/v1/settings/audiobook', {
    data: {
      enabled: false,
      engine: 'openai',
      engines: [
        {
          id: 'elevenlabs',
          enabled: false,
          baseUrl: 'https://api.elevenlabs.io/v1',
          apiKey: 'xi-e2e-secret-value',
          model: 'eleven_multilingual_v2',
          defaultVoice: 'v1',
          pricePerMillionChars: 180,
        },
      ],
    },
  });
  expect(put.ok()).toBeTruthy();

  const body = await (await adminApi.get('/api/v1/settings/audiobook')).text();
  expect(body).not.toContain('xi-e2e-secret-value');
  expect(body).toContain('"keySet":true');

  // And the rendered panel must not leak it into the DOM either.
  await page.goto('/settings');
  await page.getByRole('button', { name: 'Audiobooks', exact: true }).click();
  await expect(page.getByRole('main')).toBeVisible();
  expect(await page.content()).not.toContain('xi-e2e-secret-value');
});

// ---------------------------------------------------------------------------
// Book page
// ---------------------------------------------------------------------------

test('narration tab offers generation on an EPUB', async ({ page, adminApi }) => {
  const book = await bookOfFormat(adminApi, (b) => b.format === 'EPUB');
  test.skip(!book, 'no EPUB in the library to narrate');

  await openNarrationTab(page, book!.id);

  const main = page.getByRole('main');
  await expect(main.getByText(/No narration yet/i)).toBeVisible();
  await expect(
    main.getByRole('button', { name: /Generate narration/i }),
  ).toBeVisible();
});

test('narration is refused for a format with no extractable text', async ({
  page,
  adminApi,
}) => {
  const book = await bookOfFormat(
    adminApi,
    (b) => b.format !== 'EPUB' && b.format !== 'MP3' && b.format !== 'M4B',
  );
  test.skip(!book, 'no non-EPUB book in the library');

  await openNarrationTab(page, book!.id);

  const main = page.getByRole('main');
  await expect(main.getByText(/Only EPUB books can be narrated/i)).toBeVisible();
  // The gate is the point: no way to spend money from here.
  await expect(
    main.getByRole('button', { name: /Generate narration/i }),
  ).toHaveCount(0);
});

// The confirm step is the guardrail on an action that can cost real
// money, so the first click must never start anything.
test('generating asks for confirmation before it starts', async ({
  page,
  adminApi,
}) => {
  const book = await bookOfFormat(adminApi, (b) => b.format === 'EPUB');
  test.skip(!book, 'no EPUB in the library to narrate');

  // Fail the spec loudly if a run is ever enqueued by these clicks.
  let started = false;
  await page.route(`**/api/v1/books/${book!.id}/audiobook`, async (route) => {
    if (route.request().method() === 'POST') started = true;
    await route.continue();
  });

  await openNarrationTab(page, book!.id);
  await page.getByRole('button', { name: /Generate narration/i }).click();

  // The estimate panel replaces the button with a decision. Start is
  // present either way — disabled while the estimate is still being
  // measured, enabled once the number is in — so it is the stable
  // anchor; the "Measuring…" line is a transient beside it.
  const main = page.getByRole('main');
  await expect(main.getByRole('button', { name: /^Start$/ })).toBeVisible();

  // Backing out must leave nothing behind.
  await main.getByRole('button', { name: /^Cancel$/ }).click();
  await expect(
    main.getByRole('button', { name: /Generate narration/i }),
  ).toBeVisible();
  expect(started).toBe(false);
});
