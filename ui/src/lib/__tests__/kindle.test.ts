import { describe, expect, it } from "vitest"

import { kindleAction } from "@/lib/kindle"

// The gate predicts which of three server outcomes a click would get —
// no SMTP wired up at all (503), a user with no Kindle address (412), or
// a format Amazon will not take (415) — and then asks lib/affordance.ts
// what to do about it. Nothing here decides hide-versus-explain on its
// own; that rule is one rule for the whole app and is tested next door.
describe("kindleAction", () => {
  const reader = { isAdmin: false }
  const admin = { isAdmin: true }
  const ready = {
    emailEnabled: true,
    format: "EPUB",
    kindleEmail: "x@kindle.com",
    viewer: reader,
  }

  it("offers the send when every precondition holds", () => {
    expect(kindleAction(ready).kind).toBe("send")
  })

  it("hides the action from a reader when the instance has no email", () => {
    // Not "disabled": teasing a feature the reader cannot enable, and
    // cannot ask anyone in this session to enable, is worse than not
    // showing it.
    expect(kindleAction({ ...ready, emailEnabled: false }).kind).toBe("hidden")
  })

  it("points an admin at the panel that turns email on", () => {
    const action = kindleAction({ ...ready, emailEnabled: false, viewer: admin })

    expect(action.kind).toBe("navigate")
    if (action.kind !== "navigate") throw new Error("unreachable")
    expect(action.fix).toEqual({ where: "settings", panel: "email" })
  })

  it("stays hidden when email is off even if the format is also ineligible", () => {
    expect(
      kindleAction({ ...ready, emailEnabled: false, format: "CBZ" }).kind
    ).toBe("hidden")
  })

  it("disables the send for a format Amazon will not take", () => {
    const action = kindleAction({ ...ready, format: "CBZ" })

    expect(action.kind).toBe("explain")
    // The reason names the formats from the shared table, so it cannot
    // disagree with the handler's 415 message.
    if (action.kind !== "explain") throw new Error("unreachable")
    expect(action.reason).toContain("EPUB and PDF")
  })

  it("accepts an eligible format whatever its case", () => {
    expect(kindleAction({ ...ready, format: "pdf" }).kind).toBe("send")
  })

  it("routes to account settings when the user has no Kindle address", () => {
    const action = kindleAction({ ...ready, kindleEmail: "  " })

    expect(action.kind).toBe("navigate")
    if (action.kind !== "navigate") throw new Error("unreachable")
    expect(action.fix).toEqual({ where: "account" })
  })

  it("checks the address before the format, as the server does", () => {
    // internal/handler/kindle.go answers 412 KINDLE_EMAIL_UNSET before it
    // ever looks at the format, so a user with neither is sent to their
    // account page — which is what clicking through would have told them.
    // This used to assert the opposite on the strength of a comment that
    // did not match the handler.
    const action = kindleAction({ ...ready, format: "CBZ", kindleEmail: "" })

    expect(action.kind).toBe("navigate")
    if (action.kind !== "navigate") throw new Error("unreachable")
    expect(action.fix).toEqual({ where: "account" })
  })
})
