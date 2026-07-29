import { request, type APIRequestContext } from '@playwright/test';
import { mkdir } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

import { dropFixture } from './fixtures/bookdrop';
import {
  ADMIN_EMAIL,
  ADMIN_PASSWORD,
  ADMIN_STATE_PATH,
  BASE_URL,
} from './fixtures/constants';

// Logs in once as the seeded admin and persists the session cookie to
// ADMIN_STATE_PATH. Authenticated specs reuse it via `test.use({ storageState })`
// so they don't re-do the login dance in every test.
//
// On a fresh database (e.g. the SQLite e2e lane that doesn't load
// scripts/seed.sql) the first login returns 401. We fall back to the
// public /auth/signup endpoint to bootstrap the admin, then retry login.
// The seeded PG path takes the happy first-login branch.
export default async function globalSetup() {
  // The Go backend's CSRFGuard rejects state-changing requests without a
  // matching Origin/Referer header. Browser-driven requests set it
  // automatically; APIRequestContext doesn't, so we pass it explicitly.
  const ctx = await request.newContext({
    baseURL: BASE_URL,
    extraHTTPHeaders: { Origin: BASE_URL },
  });

  let res = await login(ctx);
  if (res.status === 401) {
    await signup(ctx);
    res = await login(ctx);
  }
  if (!res.ok) {
    throw new Error(
      `globalSetup: login failed (${res.status}) against ${BASE_URL}.\n` +
        `Make sure the Go binary is running (\`make build && ./tmp/embookshelf\`).\n` +
        `${res.body}`,
    );
  }

  const statePath = resolve(ADMIN_STATE_PATH);
  await mkdir(dirname(statePath), { recursive: true });
  await ctx.storageState({ path: statePath });

  const authed = await request.newContext({
    baseURL: BASE_URL,
    extraHTTPHeaders: { Origin: BASE_URL },
    storageState: statePath,
  });
  try {
    await clearE2EArtifacts(authed);
    await ensureLibrary(authed);
    await ensureBook(authed);
  } finally {
    await authed.dispose();
  }
  await ctx.dispose();
}

// The suite's unstated precondition. Most read specs assert
// `books.length > 0` and none of them creates a book: the bookdrop specs
// drop a file and then *reject* it, so they leave the queue as they
// found it and the library empty. On a fresh database — or on one whose
// last run left bookdrop rows and nothing to approve them into — every
// one of those specs failed on an empty list, and the failure looked
// like a broken feature rather than a missing fixture.
//
// Built through the real endpoints and the real watcher rather than by
// inserting rows: seed rows drift from what ingestion actually produces,
// and a fixture that lies is worse than none.
const LIBRARY_NAME = 'E2E Library';

// Each run used to inherit the last one's mess. Specs reject bookdrop
// rows rather than removing them, and a spec that times out never
// reaches the cleanup in its finally — so processed rows and
// `e2e-toggle-*` shelves piled up run after run, and a sidebar full of
// them is what a later spec's strict-mode locator trips over (#216).
//
// Scoped to what the suite itself creates. A developer's own books,
// shelves and queue are none of this function's business.
async function clearE2EArtifacts(ctx: APIRequestContext): Promise<void> {
  // Imported and discarded rows, through the same endpoint the settings
  // panel uses. In-flight rows are left alone by the server.
  await ctx.delete('/api/v1/settings/bookdrop/processed');

  const res = await ctx.get('/api/v1/shelves');
  if (!res.ok()) return;
  const { shelves } = (await res.json()) as {
    shelves?: { slug: string; name: string }[];
  };
  for (const shelf of shelves ?? []) {
    if (!shelf.name.startsWith('e2e-')) continue;
    await ctx.delete(`/api/v1/shelves/${encodeURIComponent(shelf.slug)}`);
  }
}

async function ensureLibrary(ctx: APIRequestContext): Promise<string> {
  const existing = await ctx.get('/api/v1/libraries');
  if (existing.ok()) {
    const body = (await existing.json()) as { libraries?: { id: string; name: string }[] };
    const found = body.libraries?.find((l) => l.name === LIBRARY_NAME) ?? body.libraries?.[0];
    if (found) return found.id;
  }

  const created = await ctx.post('/api/v1/settings/libraries', {
    data: { name: LIBRARY_NAME, kind: 'local', scan: false },
  });
  if (!created.ok()) {
    throw new Error(
      `globalSetup: could not create a library (${created.status()}).\n` +
        `Local libraries need DATA_PATH set on the server.\n${await created.text()}`,
    );
  }
  const { library } = (await created.json()) as { library: { id: string } };
  return library.id;
}

async function ensureBook(ctx: APIRequestContext): Promise<void> {
  const have = await ctx.get('/api/v1/books?limit=1');
  if (have.ok()) {
    const { books } = (await have.json()) as { books: unknown[] };
    if (books.length > 0) return;
  }

  const libraryID = await ensureLibrary(ctx);
  const { filename, cleanup } = await dropFixture('epub', 'seed');
  try {
    // The watcher ticks at 5 s and extraction adds a second or two, so
    // this is the same wait the bookdrop specs budget for.
    const item = await waitForDrop(ctx, filename, 45_000);
    const approved = await ctx.post(`/api/v1/bookdrop/${item.id}/approve`, {
      data: { libraryId: libraryID },
    });
    if (!approved.ok()) {
      throw new Error(
        `globalSetup: could not approve the seed book (${approved.status()}).\n` +
          `${await approved.text()}`,
      );
    }
  } finally {
    // Approve consumes the staged file; the unlink is for the path that
    // threw before it got there.
    await cleanup();
  }
}

async function waitForDrop(
  ctx: APIRequestContext,
  filename: string,
  timeoutMs: number,
): Promise<{ id: string; state: string }> {
  const deadline = Date.now() + timeoutMs;
  let last = 'never appeared';
  while (Date.now() < deadline) {
    const res = await ctx.get('/api/v1/bookdrop');
    if (res.ok()) {
      const { items } = (await res.json()) as {
        items: { id: string; filename: string; state: string }[];
      };
      const mine = items.find((i) => i.filename === filename);
      if (mine) {
        last = mine.state;
        // `discovered` still has extraction ahead of it; approving then
        // imports a book with no metadata.
        if (mine.state === 'ready' || mine.state === 'failed') return mine;
      }
    }
    await new Promise((r) => setTimeout(r, 1_000));
  }
  throw new Error(
    `globalSetup: ${filename} did not reach a reviewable state within ` +
      `${timeoutMs}ms (last seen: ${last}). Is the bookdrop watcher running?`,
  );
}

async function login(ctx: APIRequestContext) {
  const r = await ctx.post('/api/v1/auth/login', {
    data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
  });
  return { ok: r.ok(), status: r.status(), body: await r.text() };
}

async function signup(ctx: APIRequestContext) {
  const r = await ctx.post('/api/v1/auth/signup', {
    data: { email: ADMIN_EMAIL, name: 'Admin', password: ADMIN_PASSWORD },
  });
  if (!r.ok()) {
    throw new Error(
      `globalSetup: signup failed (${r.status()}) against ${BASE_URL}.\n` +
        `${await r.text()}`,
    );
  }
}
