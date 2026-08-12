import type { ReactNode } from "react"

/**
 * The centered paper card every auth page renders into — login, pending
 * approval, accept invite, forgot/reset password.
 *
 * It existed five times as hand-copied inline style objects, identical
 * down to the shadow literal, and two of the five (reset and
 * forgot-password) had silently lost the logo lockup along the way. One
 * component, one card, the lockup always present.
 */
export function AuthShell({
  children,
  footer,
}: {
  children: ReactNode
  /** Rendered under the card, still inside the 420px column (login's
   *  signup toggle). */
  footer?: ReactNode
}) {
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
          {/* alt="" — the wordmark right under it is the accessible name */}
          <img
            src="/logo.png"
            alt=""
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
          {children}
        </div>
        {footer}
      </div>
    </main>
  )
}
