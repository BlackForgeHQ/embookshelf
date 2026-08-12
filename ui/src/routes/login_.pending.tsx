import { Link, createFileRoute } from "@tanstack/react-router"
import { AuthShell } from "@/components/AuthShell"

export const Route = createFileRoute("/login_/pending")({
  component: PendingApproval,
})

function PendingApproval() {
  return (
    <AuthShell>
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
            You can close this tab. There is nothing else to do here.
          </p>
          <Link
            to="/login"
            className="t-link"
            style={{ fontSize: 13 }}
          >
            Back to login
          </Link>
    </AuthShell>
  )
}
