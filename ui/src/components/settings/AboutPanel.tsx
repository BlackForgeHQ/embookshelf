import { instanceInfoQuery } from "@/api/settings"
import { useApiQuery } from "@/api/query"
import { Card, DefRow } from "@/components/SettingsShared"

export function AboutPanel() {
  const info = useApiQuery(instanceInfoQuery)

  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 24 }}>
        About
      </h2>

      <Card>
        <DefRow label="Product" value="embookshelf" />
        <DefRow
          label="Version"
          value={<span className="mono">{info.data?.version ?? "—"}</span>}
        />
        <DefRow
          label="Runtime"
          value={<span className="mono">{info.data?.goVersion ?? "—"}</span>}
        />
        <DefRow
          label="BookDrop path"
          value={<span className="mono">{info.data?.bookDropPath ?? "—"}</span>}
        />
        <DefRow
          label="Data path"
          value={<span className="mono">{info.data?.dataPath ?? "—"}</span>}
        />
        <DefRow
          label="Migrate on start"
          value={info.data ? (info.data.migrateOnStart ? "yes" : "no") : "—"}
        />
      </Card>

      {info.data && (
        <>
          <div className="t-label" style={{ marginTop: 24, marginBottom: 10 }}>
            Instance totals
          </div>
          <Card>
            <DefRow label="Users" value={info.data.counts.users} />
            <DefRow label="Libraries" value={info.data.counts.libraries} />
            <DefRow
              label="Books"
              value={info.data.counts.books.toLocaleString()}
            />
          </Card>
        </>
      )}

      <p className="t-small" style={{ marginTop: 24, fontStyle: "italic" }}>
        embookshelf — self-hosted ebook library. AGPL-3.0.
      </p>
    </>
  )
}
