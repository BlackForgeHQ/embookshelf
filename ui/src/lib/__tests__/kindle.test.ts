import { describe, expect, it } from "vitest"

import { kindleAction } from "@/lib/kindle"

// The gate mirrors three distinct server outcomes: no SMTP wired up at
// all, a format Amazon will not take (415), and a user with no Kindle
// address (412). The order matters — an ineligible book on an instance
// with no email should say nothing rather than explain a format rule for
// a feature that cannot run — and it used to live as a chain of early
// returns inside a route component, where nothing could reach it.
describe("kindleAction", () => {
  const ready = { emailEnabled: true, format: "EPUB", kindleEmail: "x@kindle.com" }

  it("offers the send when every precondition holds", () => {
    expect(kindleAction(ready).kind).toBe("send")
  })

  it("hides the action entirely when the instance has no email", () => {
    // Not "disabled": teasing a feature the admin cannot enable in this
    // session is worse than not showing it.
    expect(kindleAction({ ...ready, emailEnabled: false }).kind).toBe("hidden")
  })

  it("stays hidden when email is off even if the format is also ineligible", () => {
    expect(kindleAction({ ...ready, emailEnabled: false, format: "CBZ" }).kind).toBe(
      "hidden",
    )
  })

  it("disables the send for a format Amazon will not take", () => {
    const action = kindleAction({ ...ready, format: "CBZ" })

    expect(action.kind).toBe("ineligible")
    // The reason names the formats from the shared table, so it cannot
    // disagree with the handler's 415 message.
    if (action.kind !== "ineligible") throw new Error("unreachable")
    expect(action.reason).toContain("EPUB and PDF")
  })

  it("accepts an eligible format whatever its case", () => {
    expect(kindleAction({ ...ready, format: "pdf" }).kind).toBe("send")
  })

  it("routes to account settings when the user has no Kindle address", () => {
    expect(kindleAction({ ...ready, kindleEmail: "  " }).kind).toBe("needs-address")
  })

  it("checks the format before the address, as the server does", () => {
    // The handler returns 415 for the format before it looks at the
    // user's Kindle email, so a user with neither gets told about the
    // format — matching what would happen if they clicked through.
    expect(kindleAction({ ...ready, format: "CBZ", kindleEmail: "" }).kind).toBe(
      "ineligible",
    )
  })
})
