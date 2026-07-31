# Settings › Instance — status board

Replace the Settings **About** panel with an **Instance** panel: a status board that answers one question — *is this instance healthy right now, and if not, which subsystem is wrong?*

Today's About (`ui/src/components/settings/AboutPanel.tsx`) is nine static rows of version and path facts. It fetches `enrichmentProviders` and `allowedOrigins` and throws both away, it is the only settings panel that skips `PanelHeader`/`PanelLoading`, and it spells its layout in inline styles.

## Scope

**In:** the panel rewrite, five status rows, a small backend addition covering what the app currently cannot observe at all, a real database ping on `/healthcheck`.

**Out:** cross-panel navigation (rows report; they do not link — `SettingsShell` holds `active` in the route's `useState` and `render()` takes no arguments, and widening that seam is not worth it here). Job-queue depth counts (needs widening `queue.Client` at `internal/queue/queue.go:51`). Storage-backend reachability (needs a `Probe()` on `storage.Storage`). Instance-wide audiobook rollup. SSE client count. Each of those is real backend design and belongs to its own change.

## Rows

Five rows, chosen by one rule: **a row earns its place only if it can be wrong in a way nothing else surfaces.** Config echoes are excluded — Email, OIDC, Forward auth, Audiobooks and Reading guides each show their own state inside their own panel, and "configured: yes" is not news.

| Row | Data | Source |
|---|---|---|
| Database | pool in-use / max, ping latency, schema version, dirty flag | `instanceInfoQuery` (new fields) |
| Job queue | attached or not | `instanceInfoQuery` (new field) |
| Metadata providers | count failing, worst offender's error and age | `instanceInfoQuery` (**existing** `enrichmentProviders`) |
| Libraries | scanned / total, which never scanned | `settingsLibrariesQuery` |
| BookDrop | failed count, awaiting-review count | `bookDropQuery` (`ui/src/api/bookdrop.ts:168`) — states counted client-side |

Three network requests total, on mount.

Header carries version, commit, Go version, uptime, and the three counts.

### Why the database row is not "reachable: yes"

If Postgres is down, `/api/v1/settings/instance` never answers — session lookup dies first and the panel fails to load. A green "reachable" row is therefore tautological. The row reports what can vary while the app still serves: **pool pressure** and **ping latency**. Pool exhaustion is a real, currently-invisible failure mode.

### Tone rules

| Row | `warn` when | `idle` when |
|---|---|---|
| Database | `inUse / maxConns ≥ 0.8`, ping > 250 ms, or `schema.dirty` | — |
| Job queue | `!queueAttached` | — |
| Providers | any **enabled** provider with `lastErrorAt > lastSuccessAt` | — |
| Libraries | — | any library never scanned |
| BookDrop | any item in `failed` | — |

No stale-scan threshold. There is no scan schedule to be late against; "stale after 7 days" would be a lie with a number on it.

## Backend

No new endpoint. `GET /api/v1/settings/instance` (`internal/handler/instance.go:178`) is already admin-only and already the panel's query.

New fields on `instanceInfoDTO` (`instance.go:26`):

| Field | Source |
|---|---|
| `commit` | `h.commit` — already on the struct (`handler.go:25`), never serialized |
| `startedAt` (RFC3339) | new `Handler.startedAt`, set to `time.Now()` in `New`. RFC3339 so the client relativizes it, matching the convention `providerInfoDTO` already uses |
| `database` `{pingMs, inUse, idle, maxConns}` | `pgxpool.Stat()` plus a ping |
| `schema` `{version, dirty}` | `SELECT version, dirty FROM schema_migrations` |
| `queueAttached` | `h.queue != nil` |

### Seam

`Handler` holds no `*db.DB`, and the tier's style is narrow interfaces (`bookStore`, `appSettingsStore`), not the whole handle. Keep it:

- `service.PlatformService` holds `*db.DB` and exposes one method, `Probe(ctx) (PlatformStatus, error)`.
- `Handler` holds it behind a local `platformProbe` interface — nil-safe, fakeable.
- Schema reading goes in `migrator.Current(ctx, *sql.DB) (version uint, dirty bool, err error)`, since `schema_migrations` is that package's table.

**Read the schema version on demand, not at boot.** `internal/app/app.go:826` already reads it and discards it, but capturing it there is wrong when `MigrateOnStart` is false or someone migrates out of band.

A probe error degrades the payload — the affected fields go absent and the panel shows an `unknown` row. It must not 500 the whole endpoint.

### Healthcheck

`internal/handler/health.go:11` returns a static `{"status":"ok"}` — it lies to any orchestrator. It gains a real database ping via the same probe and returns non-200 on failure. Independent of the panel; same change because it is the same probe.

## Frontend

### Status derivation is pure and lives outside React

```ts
// ui/src/components/settings/instanceStatus.ts
export type StatusTone = "ok" | "warn" | "idle"
export type StatusRow = {
  key: string
  label: string
  state: string        // "1 of 4 failing"
  tone: StatusTone
  evidence?: string    // italic second line
}
```

Five derivers — `databaseRow`, `queueRow`, `providersRow`, `librariesRow`, `bookdropRow` — each a plain function from API type to `StatusRow`. This is where the logic is, and it is testable without rendering anything.

Each deriver takes `data | undefined` plus its query's error:

- pending → `state: "checking…"`, tone `idle`
- error → `state: "unknown"`, tone `warn`, evidence = the error message

**A row whose query failed must never vanish.** A board that hides what it could not check reads as all-clear.

### Presentation — the ledger

Label left, state right, italic evidence beneath, dashed hairline between. It extends the `DefRow` idiom (`ui/src/components/DefRow.tsx`) rather than replacing it — `DefRow` has no slot for the second line, so a small `StatusLedger` component owns the two-line row. No colour beyond the existing accent ink for a warn row; no status-dot vocabulary, which the app does not otherwise use.

`InstancePanel.tsx` stays thin: three `useApiQuery` calls, map to rows, render. It adopts `PanelHeader` and `PanelLoading` — it is currently the only panel that does not, and the inline styles that caused that drift are deleted.

`PanelLoading` shows only until `instanceInfoQuery` settles, since that query owns the header. The libraries and BookDrop rows resolve independently underneath it.

### Staleness

`as of {relativeTime(…)}` — the oldest `dataUpdatedAt` across the three queries — beside a Refresh button that refetches all three. Nothing polls. A health board that is quietly ten minutes old is worse than none: it says "ok" and you believe it.

`relativeTime` is currently private inside `ProvidersPanel.tsx:560`. Extract it to `ui/src/lib/format.ts`; `ProvidersPanel` imports it from there. The new panel needs it three times.

### Rename

Label `About` → `Instance`; section key `about` → `instance`; file `AboutPanel.tsx` → `InstancePanel.tsx` (`ui/src/components/settings/sections.tsx:97`). Nothing deep-links to the key, so the rename is free. **Position stays last** in the nav.

The AGPL footer line stays.

## Tests

**Go**

- `InstanceInfo` with a fake `platformProbe`: new fields serialize; a probe error degrades the payload rather than failing the request.
- `migrator.Current` against a migrated test database.
- `Healthcheck` returns non-200 when the ping fails.

**UI**

- `instanceStatus.test.ts` — the real test surface. Each row across ok / warn / idle / pending / error, driven by fixture objects. No rendering, no MSW.
- One panel render test: five rows present, the warn row carries its evidence.
- `SettingsShell.test.tsx` updated for the new key and label.

## Decisions taken

| Decision | Choice | Rejected |
|---|---|---|
| What About becomes | status board | cosmetic tidy; whole-settings redesign |
| Backend appetite | cheap additions only | frontend-only (queue/DB stay blind); full queue + storage probes |
| Row navigation | none | search-param routing; `render(nav)` callback |
| Row set | only what can be wrong | every subsystem with data; one aggregate endpoint |
| Layout | ledger | chip grid; marginalia |
| Freshness | mount + age + manual refresh | silent staleness; 30 s polling |
