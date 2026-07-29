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
      // A library subsection, not a particular one. This named
      // `/opds/library/main` — the slug of a library some long-gone seed
      // fixture created — so it asserted the shape of somebody's dev
      // database rather than the shape of the feed (#216). What the feed
      // owes the reader is a subsection per library; which libraries
      // exist is the fixture's business, and global-setup guarantees at
      // least one.
      expect(body).toContain('/opds/library/');
    } finally {
      await ctx.dispose();
    }
  });
});
