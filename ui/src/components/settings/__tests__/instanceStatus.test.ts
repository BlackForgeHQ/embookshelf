import { describe, expect, it } from "vitest"

import type { InstanceInfo, SettingsLibrary } from "@/api/settings"
import type { BookDropItem } from "@/api/bookdrop"
import {
  bookdropRow,
  databaseRow,
  librariesRow,
  providersRow,
  queueRow,
} from "@/components/settings/instanceStatus"

function info(patch: Partial<InstanceInfo> = {}): InstanceInfo {
  return {
    version: "1.4.2",
    goVersion: "go1.23.4",
    startedAt: "2026-07-31T09:00:00Z",
    allowedOrigins: [],
    bookDropPath: "/data/bookdrop",
    dataPath: "/data",
    migrateOnStart: true,
    enrichmentProviders: [],
    counts: { users: 2, libraries: 1, books: 40 },
    queueAttached: true,
    database: { pingMs: 1.4, inUse: 3, idle: 5, maxConns: 25 },
    schema: { version: 38, dirty: false },
    ...patch,
  }
}

function library(patch: Partial<SettingsLibrary> = {}): SettingsLibrary {
  return {
    id: "lib-1",
    name: "Fiction",
    slug: "fiction",
    bookCount: 40,
    createdAt: "2026-01-01T00:00:00Z",
    path: "/data/fiction",
    lastScannedAt: "2026-07-31T08:00:00Z",
    fileCount: 40,
    discoveredCount: 40,
    ...patch,
  }
}

function dropItem(patch: Partial<BookDropItem> = {}): BookDropItem {
  return {
    id: "bd-1",
    filename: "book.epub",
    path: "/data/bookdrop/book.epub",
    fileSize: 1024,
    format: "epub",
    state: "ready",
    progress: 100,
    hasCover: false,
    discoveredAt: "2026-07-31T09:00:00Z",
    updatedAt: "2026-07-31T09:00:00Z",
    ...patch,
  }
}

const pending = { data: undefined, error: null }
const failed = { data: undefined, error: { message: "network down" } }

describe("unresolved queries", () => {
  it("shows checking while pending", () => {
    expect(databaseRow(pending)).toMatchObject({ state: "checking…", tone: "idle" })
    expect(librariesRow(pending)).toMatchObject({ state: "checking…", tone: "idle" })
  })

  // A board that hides what it could not check reads as all-clear.
  it("warns — never hides — when the query failed", () => {
    expect(bookdropRow(failed)).toMatchObject({
      state: "unknown",
      tone: "warn",
      evidence: "network down",
    })
  })
})

describe("databaseRow", () => {
  it("is healthy on a quiet pool and a clean schema", () => {
    const row = databaseRow({ data: info(), error: null })
    expect(row.tone).toBe("ok")
    expect(row.evidence).toContain("schema 38, clean")
    expect(row.evidence).toContain("pool 3 of 25")
  })

  it("warns on a dirty schema even when the pool is quiet", () => {
    const row = databaseRow({ data: info({ schema: { version: 38, dirty: true } }), error: null })
    expect(row.tone).toBe("warn")
    expect(row.state).toBe("Schema dirty")
  })

  it("warns when the pool is near capacity", () => {
    const row = databaseRow({
      data: info({ database: { pingMs: 1, inUse: 20, idle: 0, maxConns: 25 } }),
      error: null,
    })
    expect(row.tone).toBe("warn")
    expect(row.state).toBe("Pool under pressure")
  })

  it("warns on a slow ping", () => {
    const row = databaseRow({
      data: info({ database: { pingMs: 800, inUse: 1, idle: 9, maxConns: 25 } }),
      error: null,
    })
    expect(row.tone).toBe("warn")
    expect(row.state).toBe("Slow")
  })

  it("warns when the server could not probe at all", () => {
    const row = databaseRow({
      data: info({ database: undefined, schema: undefined }),
      error: null,
    })
    expect(row).toMatchObject({ state: "unknown", tone: "warn" })
  })
})

describe("queueRow", () => {
  it("is ok when attached", () => {
    expect(queueRow({ data: info(), error: null })).toMatchObject({
      state: "Attached",
      tone: "ok",
    })
  })

  it("warns and says what stops when detached", () => {
    const row = queueRow({ data: info({ queueAttached: false }), error: null })
    expect(row.tone).toBe("warn")
    expect(row.evidence).toContain("narration")
  })
})

describe("providersRow", () => {
  it("is idle when none are enabled", () => {
    expect(providersRow({ data: info(), error: null })).toMatchObject({
      state: "None enabled",
      tone: "idle",
    })
  })

  it("ignores a disabled provider that is failing", () => {
    const row = providersRow({
      data: info({
        enrichmentProviders: [
          { id: "a", name: "A", enabled: true, external: true, lastSuccessAt: "2026-07-31T09:00:00Z" },
          { id: "b", name: "B", enabled: false, external: true, lastErrorAt: "2026-07-31T09:30:00Z" },
        ],
      }),
      error: null,
    })
    expect(row).toMatchObject({ state: "1 enabled", tone: "ok" })
  })

  // A provider is failing when its last error is newer than its last
  // success — not merely when it has ever errored.
  it("counts a provider whose error is newer than its success", () => {
    const row = providersRow({
      data: info({
        enrichmentProviders: [
          { id: "a", name: "Open Library", enabled: true, external: true, lastSuccessAt: "2026-07-31T09:30:00Z", lastErrorAt: "2026-07-31T09:00:00Z" },
          { id: "b", name: "Google Books", enabled: true, external: true, lastSuccessAt: "2026-07-31T08:00:00Z", lastErrorAt: "2026-07-31T09:40:00Z", lastError: "429 rate limited" },
        ],
      }),
      error: null,
    })
    expect(row.tone).toBe("warn")
    expect(row.state).toBe("1 of 2 failing")
    expect(row.evidence).toContain("Google Books")
    expect(row.evidence).toContain("429 rate limited")
  })
})

describe("librariesRow", () => {
  it("is idle when a library has never been scanned", () => {
    const row = librariesRow({
      data: [library(), library({ id: "lib-2", name: "Comics", lastScannedAt: null })],
      error: null,
    })
    expect(row).toMatchObject({ state: "1 of 2 scanned", tone: "idle" })
    expect(row.evidence).toContain("Comics")
  })

  it("is ok when every library has been scanned", () => {
    expect(librariesRow({ data: [library()], error: null })).toMatchObject({
      state: "1 scanned",
      tone: "ok",
    })
  })

  it("is idle with no libraries at all", () => {
    expect(librariesRow({ data: [], error: null })).toMatchObject({ tone: "idle" })
  })
})

describe("bookdropRow", () => {
  it("is ok when empty", () => {
    expect(bookdropRow({ data: [], error: null })).toMatchObject({
      state: "Empty",
      tone: "ok",
    })
  })

  it("warns on failed items and counts what is waiting", () => {
    const row = bookdropRow({
      data: [dropItem(), dropItem({ id: "bd-2", state: "failed" })],
      error: null,
    })
    expect(row).toMatchObject({ state: "1 failed", tone: "warn" })
    expect(row.evidence).toContain("1 awaiting review")
  })

  it("is ok when items are only waiting", () => {
    const row = bookdropRow({ data: [dropItem()], error: null })
    expect(row.tone).toBe("ok")
    expect(row.evidence).toContain("1 awaiting review")
  })
})
