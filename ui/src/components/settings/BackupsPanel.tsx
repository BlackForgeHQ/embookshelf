import { AdminGate, Card, DefRow } from "@/components/SettingsShared"


export function BackupsPanel({ isAdmin }: { isAdmin: boolean }) {
  if (!isAdmin) return <AdminGate label="Backups" />
  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 8 }}>
        Backups
      </h2>
      <p className="t-small" style={{ marginBottom: 24, fontStyle: "italic" }}>
        The on-disk data directory and the PostgreSQL volume hold every durable
        piece of state. Back them up together.
      </p>

      <Card>
        <DefRow
          label="Database"
          value={
            <>
              <span className="mono">pg_dump embookshelf</span> — ship to your
              usual blob store on a cron.
            </>
          }
        />
        <DefRow
          label="Book files"
          value={<span className="mono">library paths</span>}
        />
        <DefRow
          label="Covers + BookDrop queue"
          value={<span className="mono">$DATA_PATH</span>}
        />
      </Card>

      <p className="t-small" style={{ marginTop: 16, fontStyle: "italic" }}>
        A scheduled-backups surface will land here once the job runner gains an
        "export" task.
      </p>
    </>
  )
}

// ---------------------------------------------------------------------------
// About
// ---------------------------------------------------------------------------

