import { bookdropQuery } from "@/api/bookdrop"
import { useApiQuery } from "@/api/query"
import { instanceInfoQuery, settingsLibrariesQuery } from "@/api/settings"
import { DefRow } from "@/components/DefRow"
import { Card, PanelHeader, PanelLoading } from "@/components/SettingsShared"
import { StatusLedger } from "@/components/settings/StatusLedger"
import {
  bookdropRow,
  databaseRow,
  librariesRow,
  providersRow,
  queueRow,
  toQueryState,
} from "@/components/settings/instanceStatus"
import { relativeTime } from "@/lib/format"

/**
 * What this instance is, and whether anything about it is wrong.
 *
 * The rows are the subsystems that can fail in a way no other panel
 * surfaces. Email, OIDC, audiobooks and reading guides are absent on
 * purpose: each shows its own state inside its own panel, and
 * "configured: yes" is not news.
 *
 * Nothing polls. A health board that is quietly ten minutes old is worse
 * than none — it says ok and you believe it — so it says how old it is
 * and offers a button instead.
 */
export function InstancePanel() {
  const info = useApiQuery(instanceInfoQuery)
  const libraries = useApiQuery(settingsLibrariesQuery)
  const bookdrop = useApiQuery(bookdropQuery)

  const infoState = toQueryState(info)
  const rows = [
    databaseRow(infoState),
    queueRow(infoState),
    providersRow(infoState),
    librariesRow(toQueryState(libraries)),
    bookdropRow(toQueryState(bookdrop)),
  ]

  const fetchedAt = [info, libraries, bookdrop]
    .map((q) => q.dataUpdatedAt)
    .filter((t) => t > 0)
  const oldest = fetchedAt.length > 0 ? Math.min(...fetchedAt) : 0

  function refreshAll() {
    void Promise.all([info.refetch(), libraries.refetch(), bookdrop.refetch()])
  }

  return (
    <>
      <PanelHeader title="Instance">
        What this server is running, and whether anything about it needs
        attention.
      </PanelHeader>

      {info.error ? (
        <div
          role="status"
          className="mb-4 rounded-lg border border-border bg-card p-4 shadow-sm"
        >
          <p className="t-small text-(--color-accent-ink)">
            This instance's details could not be read: {info.error.message}
          </p>
        </div>
      ) : !info.data ? (
        <PanelLoading />
      ) : (
        <Card>
          <DefRow label="Version" value={<span className="mono">{info.data?.version ?? "—"}</span>} />
          {info.data?.commit && (
            <DefRow label="Build" value={<span className="mono">{info.data.commit}</span>} />
          )}
          <DefRow label="Runtime" value={<span className="mono">{info.data?.goVersion ?? "—"}</span>} />
          <DefRow
            label="Uptime"
            value={<span className="mono">{uptimeText(info.data)}</span>}
          />
          <DefRow label="Data path" value={<span className="mono">{info.data?.dataPath ?? "—"}</span>} />
          <DefRow label="BookDrop path" value={<span className="mono">{info.data?.bookDropPath ?? "—"}</span>} />
          <DefRow
            label="Catalog"
            value={
              info.data
                ? `${info.data.counts.books.toLocaleString()} books · ${info.data.counts.libraries} libraries · ${info.data.counts.users} users`
                : "—"
            }
          />
        </Card>
      )}

      <div role="status">
        <div className="mt-6 mb-2.5 flex items-baseline justify-between gap-3">
          <div className="t-label">Status</div>
          <div className="flex items-baseline gap-3">
            <span className="t-small text-muted-foreground italic">
              {oldest > 0 ? `as of ${relativeTime(oldest)}` : "not yet read"}
            </span>
            <button
              type="button"
              onClick={refreshAll}
              className="t-micro border border-(--color-rule-soft) px-2 py-1 text-(--color-ink-2) transition-colors hover:bg-(--color-paper-2) hover:text-(--color-ink-1)"
            >
              Refresh
            </button>
          </div>
        </div>

        <StatusLedger rows={rows} />
      </div>

      <p className="t-small mt-6 italic">
        embookshelf — self-hosted ebook library. AGPL-3.0.
      </p>
    </>
  )
}

/**
 * The uptime row's fallback for the two shapes `relativeTime` returns
 * that are not a duration: "—" for a startedAt that failed to parse, and
 * "in the future" for a server clock ahead of the browser's. Neither
 * reads as an uptime, so both collapse to "unknown" — a genuine duration
 * is the only case that gets " ago" stripped.
 */
function uptimeText(info: { startedAt: string } | undefined): string {
  if (!info) return "—"
  const t = relativeTime(Date.parse(info.startedAt))
  if (t === "—" || t === "in the future") return "unknown"
  return t.replace(" ago", "")
}
