import { describe, expect, it } from "vitest"

import type { ApiErrorCode } from "@/api/client"
import { ALL_ERROR_CODES, affordanceFor } from "@/lib/affordance"

const admin = { isAdmin: true }
const reader = { isAdmin: false }

describe("affordanceFor", () => {
  // The rule the whole table follows: what the UI does about an
  // obstacle is decided by who can clear it and where, not by how
  // severe it is.
  describe("obstacles about this book, which nobody can clear", () => {
    it("explains rather than hiding, so the feature is still discoverable", () => {
      for (const code of [
        "FORMAT_NOT_SUPPORTED",
        "FORMAT_NOT_NARRATABLE",
      ] as const) {
        const got = affordanceFor(code, reader)

        expect(got.kind).toBe("explain")
        if (got.kind !== "explain") throw new Error("unreachable")
        // Naming the formats is the whole value of explaining: "this
        // book cannot" is useless without "these can".
        expect(got.reason).toMatch(/EPUB/)
      }
    })

    it("says the same thing to an admin, because rank does not change a format", () => {
      expect(affordanceFor("FORMAT_NOT_NARRATABLE", admin)).toEqual(
        affordanceFor("FORMAT_NOT_NARRATABLE", reader),
      )
    })
  })

  describe("obstacles this user can clear themselves", () => {
    it("points at the fix instead of refusing", () => {
      const got = affordanceFor("KINDLE_EMAIL_UNSET", reader)

      expect(got.kind).toBe("navigate")
      if (got.kind !== "navigate") throw new Error("unreachable")
      expect(got.fix.where).toBe("account")
      expect(got.label).toBeTruthy()
    })
  })

  describe("instance-wide obstacles", () => {
    // An admin can fix these, so they get the way there.
    it("sends an admin to the panel that fixes it", () => {
      for (const [code, panel] of [
        ["EMAIL_DISABLED", "email"],
        ["GUIDES_DISABLED", "readingGuides"],
        ["AUDIOBOOKS_DISABLED", "audiobooks"],
      ] as const) {
        const got = affordanceFor(code, admin)

        expect(got.kind).toBe("navigate")
        if (got.kind !== "navigate") throw new Error("unreachable")
        expect(got.fix.where).toBe("settings")
        if (got.fix.where !== "settings") throw new Error("unreachable")
        expect(got.fix.panel).toBe(panel)
      }
    })

    // A reader cannot enable SMTP, so a permanently dead control is a
    // tease. This is the one place the rule chooses hiding.
    it("hides them from someone who cannot act on them", () => {
      for (const code of [
        "EMAIL_DISABLED",
        "GUIDES_DISABLED",
        "AUDIOBOOKS_DISABLED",
      ] as const) {
        expect(affordanceFor(code, reader).kind).toBe("hidden")
      }
    })
  })

  describe("outcomes rather than preconditions", () => {
    // These two describe something that already went wrong during an
    // action the admin took. There is no control to gate — the report
    // belongs where the action happened, which is why they are their
    // own kind rather than being forced into the other three.
    it("reports inline and gates nothing", () => {
      for (const code of ["EMAIL_RELOAD_FAILED", "SMTP_ERROR"] as const) {
        expect(affordanceFor(code, admin).kind).toBe("report")
        expect(affordanceFor(code, reader).kind).toBe("report")
      }
    })
  })

  // The acceptance criterion: every declared code has an affordance, or
  // it should not be declared. ALL_ERROR_CODES is held equal to the Go
  // AllErrorCodes by the parity test in internal/handler.
  it("covers every declared code, for both viewers", () => {
    for (const code of ALL_ERROR_CODES) {
      for (const viewer of [admin, reader]) {
        const got = affordanceFor(code, viewer)

        expect(got.kind).toBeTruthy()
        if (got.kind === "hidden") continue
        expect(got.reason.length).toBeGreaterThan(0)
      }
    }
  })

  // A code the client does not know is a server that has moved ahead of
  // it. Reporting the server's own sentence is the only honest answer;
  // guessing an affordance would hide or disable a control on the
  // strength of a string nobody has read.
  it("falls back to reporting for a code it does not know", () => {
    const got = affordanceFor("SOMETHING_NEW" as ApiErrorCode, reader)

    expect(got.kind).toBe("report")
  })
})
