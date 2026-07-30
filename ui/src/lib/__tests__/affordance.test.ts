// @vitest-environment jsdom
import { createElement } from "react"
import type { ReactNode } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { cleanup, render } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import type { AuthUser } from "@/api/auth"
import { meQueryKey } from "@/api/auth"
import type { ApiErrorCode } from "@/api/client"
import type { Viewer } from "@/lib/affordance"
import { ALL_ERROR_CODES, affordanceFor, messageForCode, useViewer } from "@/lib/affordance"

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

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

// ---------------------------------------------------------------------------
// What the sentence says
// ---------------------------------------------------------------------------
//
// One code can stand for more than one cause. `AUDIOBOOKS_DISABLED` is
// both "the admin turned generation off" and "this instance has no
// engine wired": one obstacle with one fix, so one branch — but two
// different things to say, and only the server knows which happened.

describe("messageForCode", () => {
  it("tells an admin who turned narration off that it is off", () => {
    const said = messageForCode(
      "AUDIOBOOKS_DISABLED",
      "audiobook generation is not enabled",
      admin,
    )

    expect(said).toMatch(/not enabled/i)
    // The bug (#271): the client's own sentence claimed no engine was
    // configured, which on an instance with a working engine that an
    // admin had merely switched off was false, and sent the reader to
    // the wrong fix.
    expect(said).not.toMatch(/engine/i)
  })

  it("still tells an instance with nothing set up that it is not configured", () => {
    const said = messageForCode(
      "AUDIOBOOKS_DISABLED",
      "audiobook generation is not configured",
      admin,
    )

    expect(said).toMatch(/not configured/i)
  })

  // Both sentences have to carry the fix as well as the cause, which is
  // the half a toast can hold: `errorToast` is `(err) => string`, so the
  // label and the route of a navigate affordance never render.
  it("points the admin at the panel whichever cause the server named", () => {
    for (const cause of [
      "audiobook generation is not enabled",
      "audiobook generation is not configured",
    ]) {
      expect(messageForCode("AUDIOBOOKS_DISABLED", cause, admin)).toMatch(
        /narration settings/i,
      )
    }
  })

  // A reader can clear none of this, so they are told what happened and
  // not sent anywhere. The affordance is hidden, and the server's own
  // sentence is what is left.
  it("gives a reader the server's account and no fix to chase", () => {
    const said = messageForCode(
      "AUDIOBOOKS_DISABLED",
      "audiobook generation is not enabled",
      reader,
    )

    expect(said).toBe("audiobook generation is not enabled")
  })

  // Built from the code alone — a control gated before any request has
  // failed — there is no cause to quote, so the sentence has to be one
  // that holds however narration came to be unavailable.
  it("claims no particular cause when the server has not named one", () => {
    const got = affordanceFor("AUDIOBOOKS_DISABLED", admin)

    expect(got.kind).toBe("navigate")
    if (got.kind !== "navigate") throw new Error("unreachable")
    expect(got.reason).not.toMatch(/engine|enabled/i)
    expect(got.reason.length).toBeGreaterThan(0)
  })
})

// ---------------------------------------------------------------------------
// Who is looking
// ---------------------------------------------------------------------------
//
// `affordanceFor` takes a viewer, so every screen that shows a refusal had
// to build one, and each built it by comparing the role string itself —
// ten spellings of "admin" across seven components, three of which then
// wrapped the result in a viewer literal on the spot. The comparison
// belongs to the module that declares what a viewer is.

function userWithRole(role: AuthUser["role"]): AuthUser {
  return {
    id: "u1",
    email: "reader@example.com",
    name: "Reader",
    role,
    status: "active",
    display: "Reader",
    initials: "R",
    kindleEmail: "",
    createdAt: "2026-01-01T00:00:00Z",
  }
}

// Seeded rather than fetched: the hook's subject is what it makes of the
// current user, not how the user is loaded — that policy is `meQuery`'s
// and is tested with it. `undefined` is the exception, and the point of
// the third case: nothing seeded is /me still in flight, so the request
// is left hanging rather than answered.
function viewerSeenBy(user: AuthUser | null | undefined): Viewer {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } },
  })
  if (user === undefined) {
    vi.stubGlobal("fetch", () => new Promise<Response>(() => {}))
  } else {
    client.setQueryData(meQueryKey, user)
  }

  let seen: Viewer | undefined
  function Probe() {
    seen = useViewer()
    return null
  }
  render(
    createElement(QueryClientProvider, { client }, createElement(Probe) as ReactNode),
  )

  if (!seen) throw new Error("the hook did not run")
  return seen
}

describe("useViewer", () => {
  it("is an admin when the signed-in user's role says so", () => {
    expect(viewerSeenBy(userWithRole("admin"))).toEqual({ isAdmin: true })
  })

  it("is not an admin for an ordinary reader", () => {
    expect(viewerSeenBy(userWithRole("user"))).toEqual({ isAdmin: false })
  })

  it("is not an admin when nobody is signed in", () => {
    expect(viewerSeenBy(null)).toEqual({ isAdmin: false })
  })

  // The behaviour every call site already had, kept deliberately:
  // an unanswered /me reads as not-an-admin, so an admin-only control
  // appears a beat late rather than flashing into view and vanishing.
  it("is not an admin while the current user is still loading", () => {
    expect(viewerSeenBy(undefined)).toEqual({ isAdmin: false })
  })
})
