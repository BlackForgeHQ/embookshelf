import { request } from '@playwright/test';
import { mkdir } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

import {
  ADMIN_EMAIL,
  ADMIN_PASSWORD,
  ADMIN_STATE_PATH,
  BASE_URL,
} from './fixtures/constants';

// Logs in once as the seeded admin and persists the session cookie to
// ADMIN_STATE_PATH. Authenticated specs reuse it via `test.use({ storageState })`
// so they don't re-do the login dance in every test.
export default async function globalSetup() {
  // The Go backend's CSRFGuard rejects state-changing requests without a
  // matching Origin/Referer header. Browser-driven requests set it
  // automatically; APIRequestContext doesn't, so we pass it explicitly.
  const ctx = await request.newContext({
    baseURL: BASE_URL,
    extraHTTPHeaders: { Origin: BASE_URL },
  });
  const res = await ctx.post('/api/v1/auth/login', {
    data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
  });
  if (!res.ok()) {
    const body = await res.text();
    throw new Error(
      `globalSetup: login failed (${res.status()}) against ${BASE_URL}.\n` +
        `Make sure the Go binary is running (\`make build && ./tmp/embookshelf\`) ` +
        `and the DB has been seeded (\`make seed\`).\n${body}`,
    );
  }

  const statePath = resolve(ADMIN_STATE_PATH);
  await mkdir(dirname(statePath), { recursive: true });
  await ctx.storageState({ path: statePath });
  await ctx.dispose();
}
