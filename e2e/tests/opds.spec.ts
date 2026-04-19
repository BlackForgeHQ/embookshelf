import { test, expect, request } from '@playwright/test';

import {
  ADMIN_EMAIL,
  ADMIN_PASSWORD,
  BASE_URL,
} from '../fixtures/constants';

// OPDS uses Basic Auth, not session cookies, so these tests drive the
// APIRequestContext directly rather than going through a browser session.

test.describe('opds catalog', () => {
  test('anonymous access is rejected with 401', async () => {
    const ctx = await request.newContext({ baseURL: BASE_URL });
    try {
      const res = await ctx.get('/opds/');
      expect(res.status()).toBe(401);
    } finally {
      await ctx.dispose();
    }
  });

  test('bad credentials are rejected with 401', async () => {
    const ctx = await request.newContext({
      baseURL: BASE_URL,
      httpCredentials: { username: ADMIN_EMAIL, password: 'definitely-wrong' },
    });
    try {
      const res = await ctx.get('/opds/');
      expect(res.status()).toBe(401);
    } finally {
      await ctx.dispose();
    }
  });

  test('valid Basic Auth returns the navigation feed', async () => {
    const ctx = await request.newContext({
      baseURL: BASE_URL,
      httpCredentials: { username: ADMIN_EMAIL, password: ADMIN_PASSWORD },
    });
    try {
      const res = await ctx.get('/opds/');
      expect(res.ok()).toBeTruthy();
      expect(res.headers()['content-type']).toContain('application/atom+xml');

      const body = await res.text();
      expect(body).toContain('<feed');
      expect(body).toContain('embookshelf');
      // Standard OPDS rels that the root navigation feed advertises.
      expect(body).toContain('rel="self"');
      expect(body).toContain('rel="start"');
      expect(body).toContain('rel="search"');
      // Library subsections for the seeded libraries.
      expect(body).toContain('/opds/library/main');
    } finally {
      await ctx.dispose();
    }
  });
});
