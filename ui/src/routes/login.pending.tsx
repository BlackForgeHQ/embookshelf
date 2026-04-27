import { Link, createFileRoute } from "@tanstack/react-router"

export const Route = createFileRoute("/login/pending")({
  component: PendingApproval,
})

function PendingApproval() {
  return (
    <main
      style={{
        minHeight: "100vh",
        background: "var(--color-paper-1)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: 32,
      }}
    >
      <div style={{ width: "100%", maxWidth: 420 }}>
        <div
          style={{
            textAlign: "center",
            marginBottom: 24,
            display: "flex",
            flexDirection: "column",
            alignItems: "center",
            gap: 10,
          }}
        >
          <img
            src="/logo.png"
            alt="embookshelf"
            style={{
              width: 40,
              height: 40,
              objectFit: "contain",
              borderRadius: 2,
            }}
          />
          <div
            style={{
              fontFamily: "var(--font-serif)",
              fontSize: 22,
              fontWeight: 500,
              letterSpacing: "-0.01em",
            }}
          >
            embookshelf
          </div>
        </div>

        <div
          style={{
            background: "var(--color-paper-0)",
            border: "1px solid var(--color-rule-soft)",
            padding: 32,
            borderRadius: 3,
            boxShadow: "0 12px 32px -8px oklch(0.2 0.02 60 / 0.14)",
          }}
        >
          <h1 className="t-h2" style={{ marginBottom: 4 }}>
            Pending approval
          </h1>
          <p
            className="t-small"
            style={{ marginBottom: 16, fontStyle: "italic" }}
          >
            Your account has been created and is awaiting review.
          </p>
          <p className="t-body" style={{ marginBottom: 12 }}>
            An administrator will approve or deny new sign-ins from your SSO
            provider before you can sign in. You will be able to sign in once
            your account is approved.
          </p>
          <p
            className="t-small"
            style={{ marginBottom: 20, fontStyle: "italic" }}
          >
            You can close this tab — there is nothing else to do here.
          </p>
          <Link
            to="/login"
            className="t-link"
            style={{ fontSize: 13 }}
          >
            Back to login
          </Link>
        </div>
      </div>
    </main>
  )
}
