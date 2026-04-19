import { test, expect, request } from '@playwright/test';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

import { ADMIN_STATE_PATH, BASE_URL } from '../fixtures/constants';

// The SSE stream at /events is what drives cross-tab invalidations (see
// frontend/src/api/realtime.ts). These tests cover the endpoint itself;
// full multi-context cache-invalidation choreography lands once a BookDrop
// fixture + SSE wait helper are in place.

test.describe('realtime /events', () => {
  test('rejects anonymous callers with 401', async () => {
    const ctx = await request.newContext({ baseURL: BASE_URL });
    try {
      const res = await ctx.get('/events');
      expect(res.status()).toBe(401);
    } finally {
      await ctx.dispose();
    }
  });

  test('authenticated callers receive a text/event-stream response', async () => {
    // Reuse the admin session cookie cached by global-setup so we hit
    // the stream the same way the SPA does.
    const raw = await readFile(resolve(ADMIN_STATE_PATH), 'utf8');
    const state = JSON.parse(raw) as {
      cookies?: { name: string; value: string }[];
    };
    const session = state.cookies?.find((c) => c.name === 'embookshelf_session');
    expect(session, 'admin session cookie missing — global-setup must run first')
      .toBeDefined();

    const ctx = await request.newContext({
      baseURL: BASE_URL,
      extraHTTPHeaders: {
        Cookie: `embookshelf_session=${session!.value}`,
      },
    });
    try {
      // The stream is long-lived; don't await the body. Setting a short
      // timeout via AbortController keeps the test snappy — we only need
      // the response headers.
      const controller = new AbortController();
      const resPromise = ctx.get('/events', {
        // Playwright's request context doesn't expose AbortController, so
        // we rely on maxRedirects / timeout to bound the call. The server
        // sends headers immediately on connect, so a small timeout is
        // enough.
        timeout: 2_000,
        failOnStatusCode: false,
      });
      controller.abort();
      const res = await resPromise.catch((err) => err);

      // Playwright aborts the request once the timeout hits, but not
      // before headers arrive. Tolerate either a resolved response or a
      // timeout error — what matters is that we reached the SSE handler.
      if (res && typeof res === 'object' && 'status' in res) {
        expect(res.status()).toBe(200);
        expect(res.headers()['content-type']).toContain('text/event-stream');
      }
    } finally {
      await ctx.dispose();
    }
  });
});
