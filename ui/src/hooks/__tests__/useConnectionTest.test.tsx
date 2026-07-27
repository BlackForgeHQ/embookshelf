// @vitest-environment jsdom
import type { ReactNode } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { act, renderHook, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it } from "vitest"

import type { ApiError } from "@/api/client"
import type { ApiMutation } from "@/api/mutation"
import { useConnectionTest } from "../useConnectionTest"

// The shape every test endpoint in this codebase answers with: 200, plus
// its own verdict. A refused credential is a successful request.
type Reply = { ok: boolean; reply?: string; error?: string }

let respond: () => Promise<Reply> = () =>
  Promise.resolve({ ok: true, reply: "hi" })

const testEndpoint: ApiMutation<void, Reply> = {
  fn: () => respond(),
  invalidates: [],
}

let client: QueryClient
function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

function setup() {
  return renderHook(
    () =>
      useConnectionTest({
        test: testEndpoint,
        read: (r) => ({
          ok: r.ok,
          message: r.ok
            ? `Endpoint replied: "${r.reply}"`
            : `Endpoint refused: ${r.error}`,
        }),
      }),
    { wrapper }
  )
}

beforeEach(() => {
  client = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  respond = () => Promise.resolve({ ok: true, reply: "hi" })
})

describe("useConnectionTest", () => {
  it("shows nothing until the test is run", () => {
    const { result } = setup()
    expect(result.current.outcome).toBeNull()
    expect(result.current.running).toBe(false)
  })

  it("reports what a working endpoint said", async () => {
    const { result } = setup()
    act(() => result.current.run())
    await waitFor(() => expect(result.current.outcome).not.toBeNull())
    expect(result.current.outcome).toMatchObject({
      ok: true,
      message: 'Endpoint replied: "hi"',
    })
  })

  // The whole point of the module: an endpoint that says no is a result,
  // not an exception. Two of the four hand-rolled versions threw this
  // away and left the admin with a toast that had already faded.
  it("reports a refusal as an outcome, not an error", async () => {
    respond = () => Promise.resolve({ ok: false, error: "invalid api key" })
    const { result } = setup()

    act(() => result.current.run())
    await waitFor(() => expect(result.current.outcome).not.toBeNull())

    expect(result.current.outcome).toMatchObject({
      ok: false,
      message: "Endpoint refused: invalid api key",
    })
    expect(result.current.outcome?.data).toEqual({
      ok: false,
      error: "invalid api key",
    })
  })

  // A request that never completed is the same event to the admin: the
  // test ran, here is what it found.
  it("turns a thrown ApiError into the same outcome shape", async () => {
    const err: ApiError = { status: 502, message: "upstream unreachable" }
    respond = () => Promise.reject(err)
    const { result } = setup()

    act(() => result.current.run())
    await waitFor(() => expect(result.current.outcome).not.toBeNull())

    expect(result.current.outcome).toMatchObject({
      ok: false,
      message: "upstream unreachable",
    })
    // No payload came back, so there is none to show.
    expect(result.current.outcome?.data).toBeUndefined()
  })

  it("falls back to a sentence when the failure carries no message", async () => {
    respond = () =>
      Promise.reject({ status: 0, message: "" } satisfies ApiError)
    const { result } = setup()

    act(() => result.current.run())
    await waitFor(() => expect(result.current.outcome).not.toBeNull())
    expect(result.current.outcome?.message).toBe(
      "The request never reached the server."
    )
  })

  it("replaces the previous outcome rather than stacking", async () => {
    const { result } = setup()
    act(() => result.current.run())
    await waitFor(() => expect(result.current.outcome?.ok).toBe(true))

    respond = () => Promise.resolve({ ok: false, error: "revoked" })
    act(() => result.current.run())
    await waitFor(() => expect(result.current.outcome?.ok).toBe(false))
    expect(result.current.outcome?.message).toBe("Endpoint refused: revoked")
  })

  it("clears on request", async () => {
    const { result } = setup()
    act(() => result.current.run())
    await waitFor(() => expect(result.current.outcome).not.toBeNull())
    act(() => result.current.clear())
    expect(result.current.outcome).toBeNull()
  })
})
