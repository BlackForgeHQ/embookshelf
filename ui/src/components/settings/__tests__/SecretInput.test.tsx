// @vitest-environment jsdom
import type { ReactNode } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { AudiobooksPanel } from "../AudiobooksPanel"
import { EmailPanel } from "../EmailPanel"
import { OidcPanel } from "../OidcPanel"
import { ReadingGuidesPanel } from "../ReadingGuidesPanel"

// The point of this file is the *lever*, not the plumbing.
// `useSettingsDraft` has modelled clear-versus-keep since it was written
// and its own spec proves the encoding; the server has honoured it since
// #218. What was missing was a control that pulls it — `clear()` had no
// callers. So every test here renders a real panel, clicks what an admin
// would click, and reads what reached the server.
const { apiMock } = vi.hoisted(() => ({ apiMock: vi.fn() }))
vi.mock("@/api/client", () => ({ api: apiMock }))
vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}))

// Radix's Switch measures itself; jsdom has no ResizeObserver.
global.ResizeObserver = class {
  observe() {}
  unobserve() {}
  disconnect() {}
}

// ---------------------------------------------------------------------------
// A server that keeps the secret
// ---------------------------------------------------------------------------

// resolveSecret mirrors internal/handler/settings.go. The fake below
// applies it on every write and answers GETs from what it kept, so "the
// row is empty afterwards" is a claim about stored state rather than
// about the payload the panel happened to send.
function resolveSecret(incoming: string, setFlag: boolean, existing: string) {
  if (incoming !== "") return incoming
  return setFlag ? existing : ""
}

type Body = Record<string, unknown>

// One stored secret per surface, by the name the wire uses.
let kept: Record<string, string>
const held = (name: string) => kept[name] ?? ""

const emailRow = () => ({
  enabled: true,
  smtp: {
    host: "smtp.example.com",
    port: 587,
    username: "postmaster",
    tls: "starttls" as const,
  },
  from: { address: "books@example.com", name: "embookshelf" },
  publicUrl: "https://books.example.com",
  passwordSet: held("smtp") !== "",
})

const guideRow = () => ({
  enabled: true,
  baseUrl: "https://api.openai.com/v1",
  model: "gpt-4o-mini",
  keySet: held("guide") !== "",
  authStyle: "bearer" as const,
  language: "en",
  textCap: 48_000,
  requestJsonMode: false,
})

const engine = (id: string, label: string) => ({
  id,
  label,
  enabled: true,
  baseUrl: "",
  keySet: held(id) !== "",
  model: "",
  defaultVoice: "",
  pricePerMillionChars: 0,
  maxRequestChars: 4000,
  needsModel: false,
  needsBaseUrl: false,
})

const audiobookRow = () => ({
  enabled: true,
  engine: "openai",
  engines: [engine("openai", "OpenAI"), engine("elevenlabs", "ElevenLabs")],
})

const oidcRow = () => ({
  forceOnly: false,
  autoProvision: {
    enableAutoProvisioning: false,
    allowLocalAccountLinking: false,
    defaultRole: "user" as const,
    requireAdminApproval: true,
  },
  google: {
    enabled: true,
    clientId: "g-id",
    clientSecretSet: held("google") !== "",
  },
  github: {
    enabled: false,
    clientId: "",
    clientSecretSet: held("github") !== "",
  },
  generic: {
    enabled: false,
    providerName: "Authentik",
    clientId: "",
    clientSecretSet: held("generic") !== "",
    issuerUri: "",
    scopes: "openid profile email",
    claimMapping: {
      username: "preferred_username",
      email: "email",
      name: "name",
    },
  },
  redirectUri: "https://books.example.com/auth/callback",
})

function put(path: string, body: Body) {
  switch (path) {
    case "/api/v1/settings/email": {
      const smtp = body.smtp as { password?: string }
      kept.smtp = resolveSecret(
        smtp.password ?? "",
        Boolean(body.passwordSet),
        held("smtp")
      )
      return emailRow()
    }
    case "/api/v1/settings/reading-guide":
      kept.guide = resolveSecret(
        String(body.apiKey ?? ""),
        Boolean(body.keySet),
        held("guide")
      )
      return guideRow()
    case "/api/v1/settings/audiobook": {
      for (const e of body.engines as Array<{
        id: string
        apiKey?: string
        keySet: boolean
      }>) {
        kept[e.id] = resolveSecret(e.apiKey ?? "", e.keySet, held(e.id))
      }
      return audiobookRow()
    }
    case "/api/v1/settings/oidc": {
      for (const slug of ["google", "github", "generic"] as const) {
        const p = body[slug] as {
          clientSecret?: string
          clientSecretSet: boolean
        }
        kept[slug] = resolveSecret(
          p.clientSecret ?? "",
          p.clientSecretSet,
          held(slug)
        )
      }
      return oidcRow()
    }
    default:
      throw new Error(`unexpected PUT ${path}`)
  }
}

function get(path: string) {
  switch (path) {
    case "/api/v1/settings/email":
      return emailRow()
    case "/api/v1/settings/reading-guide":
      return guideRow()
    case "/api/v1/settings/reading-guide/estimate":
      return {
        books: 0,
        fullTextBooks: 0,
        maxInputTokens: 0,
        totalBooks: 0,
        booksWithGuide: 0,
      }
    case "/api/v1/settings/audiobook":
      return audiobookRow()
    case "/api/v1/settings/oidc":
      return oidcRow()
    default:
      throw new Error(`unexpected GET ${path}`)
  }
}

// Every write the panels made, as [path, init] pairs.
function puts(): Array<[string, RequestInit]> {
  return apiMock.mock.calls.flatMap((call: Array<unknown>) => {
    const init = call[1] as RequestInit | undefined
    return init?.method === "PUT"
      ? [[String(call[0]), init] as [string, RequestInit]]
      : []
  })
}

// The PUT body of the nth write to `path`. A missing call is a test bug
// worth naming rather than narrowing past.
function sentTo(path: string, n = 0): Body {
  const calls = puts().filter(([p]) => p === path)
  const call = calls[n]
  if (!call)
    throw new Error(`expected a PUT #${n} to ${path}, saw ${calls.length}`)
  return JSON.parse(String(call[1].body)) as Body
}

function wrap(node: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>{node}</QueryClientProvider>
  )
}

beforeEach(() => {
  kept = {
    smtp: "hunter2",
    guide: "sk-guide",
    openai: "sk-openai",
    elevenlabs: "xi-eleven",
    google: "goog-secret",
    github: "",
    generic: "",
  }
  apiMock.mockReset()
  apiMock.mockImplementation((path: string, init?: RequestInit) =>
    Promise.resolve(
      init?.method === "PUT"
        ? put(path, JSON.parse(String(init.body)) as Body)
        : get(path)
    )
  )
})
afterEach(cleanup)

// ---------------------------------------------------------------------------

describe("clearing a stored secret", () => {
  it("submits the encoding the server reads as clear, and empties the row", async () => {
    wrap(<EmailPanel />)
    await screen.findByLabelText("Password")

    fireEvent.click(
      screen.getByRole("button", { name: "Remove stored password" })
    )
    fireEvent.click(screen.getByRole("button", { name: "Save email settings" }))

    await waitFor(() =>
      expect(sentTo("/api/v1/settings/email")).toMatchObject({
        passwordSet: false,
        smtp: expect.objectContaining({ password: "" }),
      })
    )
    // Against the server, not the draft: the fake applied resolveSecret.
    expect(held("smtp")).toBe("")
    await screen.findByText("Not set")
  })

  it("clears a reading-guide key", async () => {
    wrap(<ReadingGuidesPanel />)
    await screen.findByLabelText("API key")

    fireEvent.click(screen.getByRole("button", { name: "Remove stored key" }))
    fireEvent.click(screen.getByRole("button", { name: "Save" }))

    await waitFor(() =>
      expect(sentTo("/api/v1/settings/reading-guide")).toMatchObject({
        apiKey: "",
        keySet: false,
      })
    )
    expect(held("guide")).toBe("")
  })

  // One engine among several: the card that was cleared loses its key and
  // its neighbour keeps one it never mentioned.
  it("clears one audiobook engine key and leaves the others stored", async () => {
    wrap(<AudiobooksPanel />)
    await screen.findByText("ElevenLabs")

    fireEvent.click(
      screen.getByRole("button", { name: "Remove stored ElevenLabs key" })
    )
    fireEvent.click(screen.getByRole("button", { name: "Save" }))

    await waitFor(() => expect(held("elevenlabs")).toBe(""))
    expect(held("openai")).toBe("sk-openai")
  })

  it("clears one OIDC client secret", async () => {
    wrap(<OidcPanel />)
    await screen.findByText("Google")

    fireEvent.click(
      screen.getByRole("button", { name: "Remove stored Google client secret" })
    )
    fireEvent.click(screen.getByRole("button", { name: "Save all" }))

    await waitFor(() => expect(held("google")).toBe(""))
  })
})

describe("the pending state", () => {
  it("says the secret goes on save, and only then", async () => {
    wrap(<EmailPanel />)
    await screen.findByLabelText("Password")
    expect(screen.getByText("Stored")).toBeDefined()

    fireEvent.click(
      screen.getByRole("button", { name: "Remove stored password" })
    )

    // Legible rather than blank: the badge changes, the sentence names
    // the moment it takes effect, and nothing has been sent.
    expect(screen.getByText("Removing on save")).toBeDefined()
    expect(
      screen.getByText("The stored password is removed when you save.")
    ).toBeDefined()
    expect(puts()).toHaveLength(0)
    expect(held("smtp")).toBe("hunter2")
  })

  it("abandons the clear on undo, and saves as keep", async () => {
    wrap(<EmailPanel />)
    await screen.findByLabelText("Password")

    fireEvent.click(
      screen.getByRole("button", { name: "Remove stored password" })
    )
    fireEvent.click(screen.getByRole("button", { name: "Undo" }))
    fireEvent.click(screen.getByRole("button", { name: "Save email settings" }))

    await waitFor(() =>
      expect(sentTo("/api/v1/settings/email")).toMatchObject({
        passwordSet: true,
      })
    )
    expect(held("smtp")).toBe("hunter2")
  })

  // Typing is the other way out of a pending clear: a new secret replaces
  // the stored one, which is not the same instruction as removing it.
  it("takes a typed replacement as a replacement, not a clear", async () => {
    wrap(<EmailPanel />)
    const input = await screen.findByLabelText("Password")

    fireEvent.click(
      screen.getByRole("button", { name: "Remove stored password" })
    )
    fireEvent.change(input, { target: { value: "new-password" } })
    fireEvent.click(screen.getByRole("button", { name: "Save email settings" }))

    await waitFor(() => expect(held("smtp")).toBe("new-password"))
  })
})

describe("when nothing is stored", () => {
  it("offers no removal control", async () => {
    kept.smtp = ""
    wrap(<EmailPanel />)
    await screen.findByLabelText("Password")

    expect(screen.getByText("Not set")).toBeDefined()
    expect(
      screen.queryByRole("button", { name: "Remove stored password" })
    ).toBeNull()
  })

  // A provider whose secret was never set must not show the control just
  // because a sibling provider's is stored.
  it("offers it per provider rather than per panel", async () => {
    wrap(<OidcPanel />)
    await screen.findByText("Google")

    expect(
      screen.getByRole("button", { name: "Remove stored Google client secret" })
    ).toBeDefined()
    expect(
      screen.queryByRole("button", {
        name: "Remove stored GitHub client secret",
      })
    ).toBeNull()
  })
})

describe("a replacement already typed", () => {
  // Removing what is about to be overwritten is not a state worth
  // offering — the save replaces the stored secret either way.
  it("withdraws the removal control", async () => {
    wrap(<ReadingGuidesPanel />)
    const input = await screen.findByLabelText("API key")

    expect(
      screen.getByRole("button", { name: "Remove stored key" })
    ).toBeDefined()
    fireEvent.change(input, { target: { value: "sk-new" } })
    expect(
      screen.queryByRole("button", { name: "Remove stored key" })
    ).toBeNull()
  })
})
