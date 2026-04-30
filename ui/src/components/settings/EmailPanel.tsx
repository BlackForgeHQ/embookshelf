import { AdminGate, Card, DefRow } from "@/components/SettingsShared"

export function EmailPanel({ isAdmin }: { isAdmin: boolean }) {
  if (!isAdmin) return <AdminGate label="Email delivery" />
  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 8 }}>
        Email delivery
      </h2>
      <p className="t-small" style={{ marginBottom: 24, fontStyle: "italic" }}>
        SMTP is not yet wired. Send-to-Kindle and share-by-email will surface
        here once the transport is configured.
      </p>

      <Card>
        <DefRow label="Transport" value="—" />
        <DefRow label="From address" value="—" />
        <DefRow label="Send-to-Kindle" value="disabled" />
      </Card>

      <p className="t-small" style={{ marginTop: 16, fontStyle: "italic" }}>
        Configure via <span className="mono">SMTP_HOST</span>,{" "}
        <span className="mono">SMTP_USERNAME</span>, and related env vars on the
        server.
      </p>
    </>
  )
}

