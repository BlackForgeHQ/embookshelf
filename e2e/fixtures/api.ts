import { test as base, type APIRequestContext, request } from '@playwright/test';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

import { ADMIN_STATE_PATH, BASE_URL } from './constants';

// Playwright's APIRequestContext doesn't auto-set the Origin header, which
// the Go backend's CSRFGuard requires on state-changing requests. Wrap it
// once so specs don't have to remember that detail.
async function buildAdminApiContext(): Promise<APIRequestContext> {
  const stateRaw = await readFile(resolve(ADMIN_STATE_PATH), 'utf8');
  const state = JSON.parse(stateRaw) as {
    cookies?: { name: string; value: string }[];
  };
  const sessionCookie = state.cookies?.find((c) => c.name === 'embookshelf_session');
  if (!sessionCookie) {
    throw new Error(
      `No embookshelf_session cookie in ${ADMIN_STATE_PATH} — did global-setup run?`,
    );
  }

  return request.newContext({
    baseURL: BASE_URL,
    extraHTTPHeaders: {
      Origin: BASE_URL,
      Cookie: `embookshelf_session=${sessionCookie.value}`,
    },
  });
}

// Extends the base test with an `adminApi` fixture: an APIRequestContext
// authenticated as the seeded admin, with Origin pre-set so CSRFGuard is
// happy. Disposed automatically at end of test.
export const test = base.extend<{ adminApi: APIRequestContext }>({
  // eslint-disable-next-line no-empty-pattern
  adminApi: async ({}, use) => {
    const ctx = await buildAdminApiContext();
    await use(ctx);
    await ctx.dispose();
  },
});

export { expect } from '@playwright/test';
