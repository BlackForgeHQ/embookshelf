// @vitest-environment jsdom
import type { ReactNode } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { act, renderHook, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import type { ApiMutation } from "@/api/mutation"
import { useDraft, useSettingsDraft } from "../useSettingsDraft"

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}))

// A settings row shaped like the real ones: a couple of plain fields and
// a write-only secret the server reports as a boolean.
type Settings = {
  enabled: boolean
  baseUrl: string
  model: string
  keySet: boolean
}

type Payload = Settings & { apiKey: string }

const initial: Settings = {
  enabled: false,
  baseUrl: "",
  model: "",
  keySet: false,
}

const stored: Settings = {
  enabled: true,
  baseUrl: "http://localhost:11434/v1",
  model: "llama3.1",
  keySet: true,
}

let served: Settings = stored
const fetched = vi.fn()
const submitted = vi.fn<(p: Payload) => void>()

function serve(next: Settings) {
  served = next
}

// The payload of the nth save. Indexed access is checked in this project,
// and a missing call is a test bug worth naming rather than narrowing past.
function submittedAt(n: number): Payload {
  const call = submitted.mock.calls[n]
  if (!call)
    throw new Error(`expected a save #${n}, saw ${submitted.mock.calls.length}`)
  return call[0]
}

const saveSettings: ApiMutation<Payload, Settings> = {
  fn: (payload) => {
    submitted(payload)
    const { apiKey, ...rest } = payload
    return Promise.resolve({ ...rest, keySet: apiKey !== "" || rest.keySet })
  },
  invalidates: [["settings"]],
}

let client: QueryClient

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

// Counted so `refetch` can wait for the panel to have *seen* the new
// payload. Without that, "the edit survived" would pass on a render that
// never happened, which is exactly how these invariants held by accident
// before the module existed.
let renders = 0

function setup() {
  return renderHook(
    () => {
      renders++
      return useSettingsDraft({
        query: {
          key: ["settings"],
          fn: () => {
            fetched()
            return Promise.resolve(served)
          },
        },
        initial,
        save: saveSettings,
        toPayload: (value, secrets) => ({
          ...value,
          // The panel-side idiom this module exists to make uniform.
          apiKey: secrets.value("apiKey"),
          keySet: secrets.stillSet("apiKey", value.keySet),
        }),
      })
    },
    { wrapper }
  )
}

async function hydrated() {
  const view = setup()
  await waitFor(() => expect(view.result.current.loading).toBe(false))
  return view
}

// A payload landing in the cache behind the panel's back: another admin
// saved, or a mutation invalidated the key. Returns only once the cache
// holds it *and* the panel has rendered since — every test below turns on
// the payload having genuinely reached the hook, so proving it here keeps
// the assertions honest.
async function refetch(next: Settings) {
  const before = renders
  serve(next)
  await act(async () => {
    await client.invalidateQueries({ queryKey: ["settings"] })
  })
  await waitFor(() => expect(client.getQueryData(["settings"])).toEqual(next))
  await waitFor(() => expect(renders).toBeGreaterThan(before))
}

beforeEach(() => {
  served = stored
  renders = 0
  fetched.mockClear()
  submitted.mockClear()
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
})
afterEach(() => client.clear())

// ---------------------------------------------------------------------------

describe("hydration", () => {
  it("renders the initial shape until the first payload lands", () => {
    const { result } = setup()
    expect(result.current.loading).toBe(true)
    expect(result.current.value).toEqual(initial)
  })

  it("copies the first payload in", async () => {
    const { result } = await hydrated()
    expect(result.current.value).toEqual(stored)
    expect(result.current.dirty).toBe(false)
  })

  // The other half of invariant 1: gating on *dirtiness* rather than on
  // "have we hydrated once" is what keeps an untouched panel current.
  it("follows the server while the draft is pristine", async () => {
    const { result } = await hydrated()
    await refetch({ ...stored, model: "mistral-small" })
    await waitFor(() =>
      expect(result.current.value.model).toBe("mistral-small")
    )
  })
})

describe("invariant 1 — a refetch must not clobber in-flight edits", () => {
  it("keeps an edit when a newer payload arrives", async () => {
    const { result } = await hydrated()

    act(() => result.current.patch("model", "my-local-model"))
    await refetch({
      ...stored,
      model: "someone-elses-model",
      baseUrl: "http://elsewhere/v1",
    })

    expect(result.current.value.model).toBe("my-local-model")
    // Not just the edited field: the whole draft is the admin's, so an
    // untouched field must not silently take a new value either.
    expect(result.current.value.baseUrl).toBe(stored.baseUrl)
    expect(result.current.dirty).toBe(true)
  })

  it("keeps a typed secret when a newer payload arrives", async () => {
    const { result } = await hydrated()

    act(() => result.current.secret("apiKey").set("sk-the-new-key"))
    await refetch({ ...stored, model: "someone-elses-model" })

    expect(result.current.secret("apiKey").value).toBe("sk-the-new-key")
  })

  it("takes the newest payload once the draft is reverted", async () => {
    const { result } = await hydrated()

    act(() => result.current.patch("model", "my-local-model"))
    await refetch({ ...stored, model: "someone-elses-model" })
    act(() => result.current.revert())

    await waitFor(() =>
      expect(result.current.value.model).toBe("someone-elses-model")
    )
    expect(result.current.dirty).toBe(false)
  })

  // Editing back to what the server already had is not an edit worth
  // defending — the panel is pristine again and free to track the server.
  it("stops treating the draft as in-flight once it matches the server again", async () => {
    const { result } = await hydrated()

    act(() => result.current.patch("model", "typed-then-undone"))
    expect(result.current.dirty).toBe(true)
    act(() => result.current.patch("model", stored.model))
    expect(result.current.dirty).toBe(false)
  })

  // Gating on dirtiness would deadlock if a save left the draft dirty: the
  // panel would never accept a payload again. The save settles the draft,
  // so the refetch it triggers lands — including the server's own
  // normalisation of what was just submitted.
  it("takes the payload the save produced rather than staying dirty forever", async () => {
    const { result } = await hydrated()

    act(() => result.current.patch("model", " my-local-model "))
    serve({ ...stored, model: "my-local-model" }) // server trimmed it
    act(() => result.current.save())

    await waitFor(() => expect(submitted).toHaveBeenCalledOnce())
    await waitFor(() =>
      expect(result.current.value.model).toBe("my-local-model")
    )
    expect(result.current.dirty).toBe(false)
  })
})

describe("invariant 2 — an untouched secret submits empty, meaning keep", () => {
  it("submits an empty secret when the field was never touched", async () => {
    const { result } = await hydrated()

    act(() => result.current.patch("model", "llama3.2"))
    act(() => result.current.save())

    await waitFor(() => expect(submitted).toHaveBeenCalledOnce())
    expect(submittedAt(0)).toMatchObject({
      apiKey: "",
      // Empty *and* still set: keep the stored key. The pair is the whole
      // contract; either half alone is ambiguous.
      keySet: true,
    })
  })

  it("submits what was typed when the field was touched", async () => {
    const { result } = await hydrated()

    act(() => result.current.secret("apiKey").set("sk-the-new-key"))
    act(() => result.current.save())

    await waitFor(() => expect(submitted).toHaveBeenCalledOnce())
    expect(submittedAt(0)).toMatchObject({
      apiKey: "sk-the-new-key",
      keySet: true,
    })
  })

  // The dangerous case: a background refetch used to wipe the secret
  // draft, so the save that followed said "keep" and the operator's new
  // key vanished behind a success toast.
  it("submits the typed secret even after a refetch landed mid-edit", async () => {
    const { result } = await hydrated()

    act(() => result.current.secret("apiKey").set("sk-the-new-key"))
    await refetch({ ...stored, model: "someone-elses-model" })
    act(() => result.current.save())

    await waitFor(() => expect(submitted).toHaveBeenCalledOnce())
    expect(submittedAt(0).apiKey).toBe("sk-the-new-key")
  })

  // Typing and then erasing is not an instruction to clear. Only the
  // explicit act is.
  it("still means keep when the admin erases their own typing", async () => {
    const { result } = await hydrated()

    act(() => result.current.secret("apiKey").set("half-a-ke"))
    act(() => result.current.secret("apiKey").set(""))
    act(() => result.current.save())

    await waitFor(() => expect(submitted).toHaveBeenCalledOnce())
    expect(submittedAt(0)).toMatchObject({ apiKey: "", keySet: true })
  })

  it("clears only when the admin asks for it", async () => {
    const { result } = await hydrated()

    act(() => result.current.secret("apiKey").clear())
    expect(result.current.secret("apiKey").cleared).toBe(true)
    act(() => result.current.save())

    await waitFor(() => expect(submitted).toHaveBeenCalledOnce())
    // Empty and no longer set: the one encoding the server reads as clear.
    expect(submittedAt(0)).toMatchObject({ apiKey: "", keySet: false })
  })

  it("never shows a stored secret, because it never has one", async () => {
    const { result } = await hydrated()
    expect(result.current.value.keySet).toBe(true)
    expect(result.current.secret("apiKey").value).toBe("")
    expect(result.current.secret("apiKey").touched).toBe(false)
  })

  it("forgets the draft after a successful save so the next one means keep", async () => {
    const { result } = await hydrated()

    act(() => result.current.secret("apiKey").set("sk-the-new-key"))
    act(() => result.current.save())
    await waitFor(() => expect(submitted).toHaveBeenCalledOnce())
    await waitFor(() => expect(result.current.secret("apiKey").value).toBe(""))

    act(() => result.current.save())
    await waitFor(() => expect(submitted).toHaveBeenCalledTimes(2))
    expect(submittedAt(1).apiKey).toBe("")
  })

  it("keeps secrets apart by name", async () => {
    const { result } = await hydrated()

    act(() => result.current.secret("openai").set("sk-openai"))
    act(() => result.current.secret("elevenlabs").set("xi-eleven"))

    expect(result.current.secret("openai").value).toBe("sk-openai")
    expect(result.current.secret("elevenlabs").value).toBe("xi-eleven")
    expect(result.current.secret("azure").value).toBe("")
  })
})

describe("dirtiness", () => {
  it("counts a touched secret even when no field changed", async () => {
    const { result } = await hydrated()
    expect(result.current.dirty).toBe(false)
    act(() => result.current.secret("apiKey").set("sk-x"))
    expect(result.current.dirty).toBe(true)
  })

  it("resets after a successful save", async () => {
    const { result } = await hydrated()
    act(() => result.current.patch("model", "llama3.2"))
    act(() => result.current.save())
    await waitFor(() => expect(result.current.dirty).toBe(false))
  })
})

// ---------------------------------------------------------------------------
// The core on its own, for panels whose payload is a prop
// ---------------------------------------------------------------------------

describe("useDraft", () => {
  it("waits for a source and then hydrates", () => {
    const { result, rerender } = renderHook(
      ({ source }: { source: Settings | undefined }) =>
        useDraft(source, initial),
      { initialProps: { source: undefined as Settings | undefined } }
    )
    expect(result.current.loading).toBe(true)

    rerender({ source: stored })
    expect(result.current.loading).toBe(false)
    expect(result.current.value).toEqual(stored)
  })

  it("holds an edit against a changing source", () => {
    const { result, rerender } = renderHook(
      ({ source }: { source: Settings }) => useDraft(source, initial),
      { initialProps: { source: stored } }
    )
    act(() => result.current.patch("model", "mine"))
    rerender({ source: { ...stored, model: "theirs" } })
    expect(result.current.value.model).toBe("mine")
  })

  it("settles onto the current draft so the next source can land", () => {
    const { result, rerender } = renderHook(
      ({ source }: { source: Settings }) => useDraft(source, initial),
      { initialProps: { source: stored } }
    )
    act(() => result.current.patch("model", "mine"))
    act(() => result.current.settle())
    expect(result.current.dirty).toBe(false)

    rerender({ source: { ...stored, model: "theirs" } })
    expect(result.current.value.model).toBe("theirs")
  })
})

// The interface change #353 exists for: the hook consumes the spec via
// apiQueryOptions, so policy the spec declares — staleTime here —
// actually reaches the underlying query. The old key/fn-apart interface
// rebuilt the options by hand and dropped everything else on the floor.
it("carries the spec's declared policy through to the query", async () => {
  let fetches = 0
  const spec = {
    key: ["settings-policy"],
    fn: () => {
      fetches++
      return Promise.resolve(served)
    },
    staleTime: 60_000,
  }
  const first = renderHook(
    () =>
      useSettingsDraft({
        query: spec,
        initial,
        save: saveSettings,
        toPayload: (value, secrets) => ({
          ...value,
          apiKey: secrets.value("apiKey"),
          keySet: secrets.stillSet("apiKey", value.keySet),
        }),
      }),
    { wrapper }
  )
  await waitFor(() => expect(fetches).toBe(1))
  first.unmount()

  // A remount inside staleTime must serve the cached row, not refetch —
  // which only happens if the spec's staleTime survived the hook.
  const second = renderHook(
    () =>
      useSettingsDraft({
        query: spec,
        initial,
        save: saveSettings,
        toPayload: (value, secrets) => ({
          ...value,
          apiKey: secrets.value("apiKey"),
          keySet: secrets.stillSet("apiKey", value.keySet),
        }),
      }),
    { wrapper }
  )
  await waitFor(() => expect(second.result.current.loading).toBe(false))
  expect(fetches).toBe(1)
})
