import type { BookDropItem } from "@/api/bookdrop"
import type { InstanceInfo, ProviderInfo, SettingsLibrary } from "@/api/settings"
import { relativeTime } from "@/lib/format"

/**
 * The Instance panel as pure functions.
 *
 * Every row on the board is derived here, from an API payload to a
 * StatusRow, with no React and no fetching in sight. The panel that
 * renders these is thirty lines of layout; this is where the judgements
 * live — what counts as pool pressure, when a provider is failing rather
 * than merely having once failed, what a never-scanned library means.
 * Those are the things worth testing, and a test for them needs no DOM.
 */

export type StatusTone = "ok" | "warn" | "idle"

export type StatusRow = {
  key: string
  label: string
  /** The verdict, one short phrase. */
  state: string
  tone: StatusTone
  /** The evidence for the verdict, rendered as a second italic line. */
  evidence?: string
}

/**
 * What a deriver needs to know about its query. Narrower than
 * react-query's result on purpose: a deriver that could see `isFetching`
 * would start making the board flicker.
 *
 * `data === undefined` with no error means still in flight — there is no
 * separate pending flag, because those are the same state to a reader.
 */
export type QueryState<T> = {
  data: T | undefined
  error: { message: string } | null
}

export function toQueryState<T>(q: {
  data: T | undefined
  error: { message: string } | null
}): QueryState<T> {
  return { data: q.data, error: q.error }
}

/** Pool utilisation at or above this reads as pressure. */
export const POOL_PRESSURE_RATIO = 0.8

/** A ping slower than this is worth saying out loud. */
export const SLOW_PING_MS = 250

/**
 * The one place a row handles not-yet and could-not-tell.
 *
 * A failed query becomes a warn row, never a missing one: a board that
 * drops what it could not check reads as all-clear, which is the exact
 * lie it exists to prevent.
 */
function row<T>(
  key: string,
  label: string,
  q: QueryState<T>,
  resolve: (data: T) => Omit<StatusRow, "key" | "label">
): StatusRow {
  if (q.error) {
    return { key, label, state: "unknown", tone: "warn", evidence: q.error.message }
  }
  if (!q.data) {
    return { key, label, state: "checking…", tone: "idle" }
  }
  return { key, label, ...resolve(q.data) }
}

export function databaseRow(q: QueryState<InstanceInfo>): StatusRow {
  return row("database", "Database", q, (info) => {
    const db = info.database
    if (!db) {
      return {
        state: "unknown",
        tone: "warn",
        evidence: "the server could not probe its database",
      }
    }

    const schema = info.schema
    const evidence = [
      schema ? `schema ${schema.version}, ${schema.dirty ? "dirty" : "clean"}` : "schema unknown",
      `pool ${db.inUse} of ${db.maxConns} in use`,
      `ping ${Math.round(db.pingMs)}ms`,
    ].join(" · ")

    if (schema?.dirty) return { state: "Schema dirty", tone: "warn", evidence }
    if (db.maxConns > 0 && db.inUse / db.maxConns >= POOL_PRESSURE_RATIO) {
      return { state: "Pool under pressure", tone: "warn", evidence }
    }
    if (db.pingMs > SLOW_PING_MS) return { state: "Slow", tone: "warn", evidence }
    return { state: "Healthy", tone: "ok", evidence }
  })
}

export function queueRow(q: QueryState<InstanceInfo>): StatusRow {
  return row("queue", "Job queue", q, (info) =>
    info.queueAttached
      ? { state: "Attached", tone: "ok" }
      : {
          state: "Not attached",
          tone: "warn",
          evidence:
            "no worker pool is wired — scans, enrichment and narration will not run",
        }
  )
}

export function providersRow(q: QueryState<InstanceInfo>): StatusRow {
  return row("providers", "Metadata providers", q, (info) => {
    const enabled = info.enrichmentProviders.filter((p) => p.enabled)
    if (enabled.length === 0) {
      return {
        state: "None enabled",
        tone: "idle",
        evidence: "no provider will be consulted when a book is enriched",
      }
    }

    const failing = enabled.filter(isFailing)
    if (failing.length === 0) {
      return { state: `${enabled.length} enabled`, tone: "ok" }
    }

    const worst = failing.reduce((a, b) => (errorAt(b) > errorAt(a) ? b : a))
    return {
      state: `${failing.length} of ${enabled.length} failing`,
      tone: "warn",
      evidence: `${worst.name} — ${worst.lastError ?? "no detail recorded"}, ${relativeTime(errorAt(worst))}`,
    }
  })
}

/**
 * A provider is failing when its last error is newer than its last
 * success — not merely when it has ever errored, which is true of any
 * provider that has run long enough.
 */
function isFailing(p: ProviderInfo): boolean {
  return errorAt(p) > successAt(p)
}

function errorAt(p: ProviderInfo): number {
  return p.lastErrorAt ? Date.parse(p.lastErrorAt) : 0
}

function successAt(p: ProviderInfo): number {
  return p.lastSuccessAt ? Date.parse(p.lastSuccessAt) : 0
}

export function librariesRow(q: QueryState<Array<SettingsLibrary>>): StatusRow {
  return row("libraries", "Libraries", q, (libs) => {
    if (libs.length === 0) {
      return { state: "None", tone: "idle", evidence: "no library has been added yet" }
    }

    const never = libs.filter((l) => !l.lastScannedAt)
    if (never.length === 0) {
      return { state: `${libs.length} scanned`, tone: "ok" }
    }

    // Never-scanned is idle, not a warning: nothing is broken, the work
    // simply has not been asked for. There is deliberately no
    // stale-after-N-days rule — there is no scan schedule to be late
    // against, so a threshold would be a lie with a number on it.
    return {
      state: `${libs.length - never.length} of ${libs.length} scanned`,
      tone: "idle",
      evidence: `${never.map((l) => l.name).join(", ")} — never scanned`,
    }
  })
}

export function bookdropRow(q: QueryState<Array<BookDropItem>>): StatusRow {
  return row("bookdrop", "BookDrop", q, (items) => {
    if (items.length === 0) return { state: "Empty", tone: "ok" }

    const failed = items.filter((i) => i.state === "failed")
    const waiting = items.filter((i) => i.state === "ready")
    const waitingNote = waiting.length > 0 ? `${waiting.length} awaiting review` : undefined

    if (failed.length === 0) {
      return {
        state: `${items.length} ${items.length === 1 ? "item" : "items"}`,
        tone: "ok",
        evidence: waitingNote,
      }
    }
    return {
      state: `${failed.length} failed`,
      tone: "warn",
      evidence: [`of ${items.length} items`, waitingNote].filter(Boolean).join(" · "),
    }
  })
}
