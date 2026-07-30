// @vitest-environment jsdom
import type { ReactNode } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { act, renderHook, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import type { ApiError } from "../client"
import type { UseApiMutationOpts } from "../mutation"
import { defineMutation, useApiMutation } from "../mutation"

const toastError = vi.fn()
const toastSuccess = vi.fn()
vi.mock("sonner", () => ({
  toast: {
    error: (msg: string) => toastError(msg),
    success: (msg: string) => toastSuccess(msg),
  },
}))

let respond: () => Promise<string> = () => Promise.resolve("done")

const endpoint = defineMutation<void, string>({
  fn: () => respond(),
  invalidates: [],
})

let client: QueryClient
function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

const refused: ApiError = { status: 409, message: "Shelf name already taken." }

beforeEach(() => {
  client = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  respond = () => Promise.resolve("done")
  toastError.mockClear()
  toastSuccess.mockClear()
})

// ---------------------------------------------------------------------------
// Where a failure is reported
// ---------------------------------------------------------------------------
//
// A failure is reported once. The hook used to toast unconditionally, so
// the three call sites that also rendered `mut.error` next to the control
// showed the same sentence twice — once floating, once in place.

describe("failure reporting", () => {
  it("raises no toast when the caller reports the failure inline", async () => {
    respond = () => Promise.reject(refused)
    const { result } = renderHook(
      () => useApiMutation(endpoint, { reportErrors: "inline" }),
      { wrapper }
    )

    act(() => result.current.mutate())
    await waitFor(() => expect(result.current.error).not.toBeNull())

    // The caller renders this itself; a toast would be the second copy.
    expect(result.current.error?.message).toBe("Shelf name already taken.")
    expect(toastError).not.toHaveBeenCalled()
  })

  it("toasts by default, so untouched call sites are unaffected", async () => {
    respond = () => Promise.reject(refused)
    const { result } = renderHook(() => useApiMutation(endpoint), { wrapper })

    act(() => result.current.mutate())
    await waitFor(() => expect(result.current.error).not.toBeNull())

    expect(toastError).toHaveBeenCalledWith("Shelf name already taken.")
  })

  // Checked by `bun run typecheck`, not by running this file: an inline
  // reporter has no toast to word, so the two cannot be asked for at once.
  it("cannot be asked for a toast it will not raise", () => {
    // @ts-expect-error — errorToast belongs to the toast branch only.
    const opts: UseApiMutationOpts<void, string> = {
      reportErrors: "inline",
      errorToast: "never said",
    }
    expect(opts.reportErrors).toBe("inline")
  })

  it("still reports success where the caller asked for it", async () => {
    const { result } = renderHook(
      () => useApiMutation(endpoint, { reportErrors: "inline" }),
      { wrapper }
    )

    act(() => result.current.mutate())
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    // Inline is a choice about failures. A success toast is a separate
    // opt-in, and this call site did not take it.
    expect(toastSuccess).not.toHaveBeenCalled()
  })
})
