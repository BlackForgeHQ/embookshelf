import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import type { QueryKey } from "@tanstack/react-query"

import type { ApiMutation } from "@/api/mutation"
import { useApiMutation } from "@/api/mutation"

// The settings-draft module.
//
// Every admin settings panel repeats the same five moves: fetch the row,
// copy it into local form state, re-copy it when a new payload lands,
// submit it, and hold whatever a connection test returned. Written out
// per panel, each move drifts — eight panels grew four different shapes
// for write-only secrets and four different loading states, and the same
// four-line comment justifying the hydrate effect was pasted five times.
// A comment that appears five times is a module that was never written.
//
// This module owns the whole lifecycle. Panels supply a shape, a query,
// a save mutation, and the form body; they never write a hydrate effect,
// never decide what an empty secret means, and never track dirtiness.
//
// Two invariants live here, and nowhere else:
//
//   1. A refetch must not clobber in-flight edits. A settings query
//      refetches on invalidation, on remount, and after any mutation
//      that lists it in `invalidates`. The payload it brings back is
//      authoritative only while the admin has not started typing —
//      afterwards the draft wins until it is saved or reverted.
//
//   2. An untouched secret field submits empty, and empty means *keep*.
//      The server never sends a stored secret back, so a field the admin
//      did not touch has nothing to submit. Sending anything other than
//      "" would either invent a value or clear a working credential.
//      Clearing is a separate, explicit act — see `clear()`.
//
// Both were prose comments before this module existed, and neither was
// tested. `useSettingsDraft.test.tsx` tests them now.

// ---------------------------------------------------------------------------
// Write-only secrets
// ---------------------------------------------------------------------------

// SecretField is what a panel binds a password input to. `value` is only
// ever what the admin typed this session — there is nothing else to show,
// because the stored secret is unreadable by design (ADR-0010).
export type SecretField = {
  value: string
  // True once the admin typed into the field or asked to clear it. The
  // difference between "" and untouched is the difference between an
  // admin who erased their own typing and one who never looked.
  touched: boolean
  // True when the admin explicitly asked to drop the stored secret.
  cleared: boolean
  set: (v: string) => void
  clear: () => void
}

// SecretSubmission is the read side, handed to `toPayload`. It is the only
// place that encodes the keep/clear contract the settings handlers share
// (`resolveSecret` in internal/handler/settings.go): a non-empty value
// wins; an empty value with the set-flag still true keeps the stored
// secret; an empty value with the flag false clears it.
export type SecretSubmission = {
  // The string to submit for `name`. Empty means keep — never clear.
  value: (name: string) => string
  touched: (name: string) => boolean
  cleared: (name: string) => boolean
  // The set-flag to submit alongside `value`, given whether the server
  // currently holds one. Answers "will a secret be stored after this
  // save?", which is the question the flag actually encodes.
  stillSet: (name: string, storedNow: boolean) => boolean
}

type SecretCell = { value: string; touched: boolean; cleared: boolean }

const UNTOUCHED: SecretCell = { value: "", touched: false, cleared: false }

// ---------------------------------------------------------------------------
// The draft core
// ---------------------------------------------------------------------------

export type Draft<TData> = {
  // True until the first payload has been copied in. A panel renders its
  // header and `PanelLoading` while this holds.
  loading: boolean
  value: TData
  // What the server last told us, for panels that need to compare (e.g.
  // "stored" badges read from here, not from the draft).
  server: TData | undefined
  set: (next: TData | ((prev: TData) => TData)) => void
  patch: <TKey extends keyof TData>(key: TKey, value: TData[TKey]) => void
  dirty: boolean
  revert: () => void
  secret: (name: string) => SecretField
  secrets: SecretSubmission
  // settle accepts the current draft as the new server truth: dirtiness
  // resets and the next payload is free to hydrate again. Called after a
  // successful save.
  settle: () => void
}

// sameShape compares two payloads by their JSON encoding. These are plain
// settings DTOs built by spreading the server's own object, so key order
// is stable and a structural compare is exactly the right notion of
// "the admin changed nothing".
function sameShape(a: unknown, b: unknown): boolean {
  return a === b || JSON.stringify(a) === JSON.stringify(b)
}

// useDraft is the source-agnostic core: hydrate-while-pristine, dirtiness,
// and secret drafts over any `source` that arrives asynchronously. Used
// directly by panels whose payload is a prop rather than their own query
// (a provider row inside the providers list), and by `useSettingsDraft`
// for everything else.
//
// `initial` is read on the first render and by `revert` before any payload
// has landed; keep it a module-level constant so it does not change
// identity between renders.
export function useDraft<TData>(
  source: TData | undefined,
  initial: TData
): Draft<TData> {
  const [value, setValue] = useState<TData>(initial)
  // baseline is the payload the draft was last hydrated from — undefined
  // until the first one lands, which is also what `loading` means.
  const [baseline, setBaseline] = useState<TData | undefined>(undefined)
  const [cells, setCells] = useState<Record<string, SecretCell>>({})

  // settle runs from a mutation callback, outside any render, so it reads
  // the live draft through a ref rather than a captured copy.
  const valueRef = useRef(value)
  valueRef.current = value

  const secretsTouched = useMemo(
    () => Object.values(cells).some((c) => c.touched),
    [cells]
  )
  const dirty =
    (baseline !== undefined && !sameShape(value, baseline)) || secretsTouched

  // Invariant 1 is enforced here and only here. A pristine draft tracks
  // the server, so a panel left open still sees another admin's save; a
  // dirty one ignores every payload until it is saved or reverted. The
  // effect re-runs when `dirty` falls, so the newest payload lands the
  // moment the draft stops being in flight rather than being lost.
  useEffect(() => {
    if (source === undefined) return
    if (source === baseline) return
    if (baseline !== undefined && dirty) return
    setValue(source)
    setBaseline(source)
  }, [source, baseline, dirty])

  const set = useCallback((next: TData | ((prev: TData) => TData)) => {
    setValue((prev) =>
      typeof next === "function" ? (next as (p: TData) => TData)(prev) : next
    )
  }, [])

  const patch = useCallback(
    <TKey extends keyof TData>(key: TKey, v: TData[TKey]) => {
      setValue((prev) => ({ ...prev, [key]: v }))
    },
    []
  )

  const revert = useCallback(() => {
    setValue((prev) => baseline ?? prev)
    setCells({})
  }, [baseline])

  const settle = useCallback(() => {
    setBaseline(valueRef.current)
    setCells({})
  }, [])

  const secret = useCallback(
    (name: string): SecretField => {
      const cell = cells[name] ?? UNTOUCHED
      return {
        value: cell.value,
        touched: cell.touched,
        cleared: cell.cleared,
        set: (v: string) =>
          setCells((prev) => ({
            ...prev,
            [name]: { value: v, touched: true, cleared: false },
          })),
        clear: () =>
          setCells((prev) => ({
            ...prev,
            [name]: { value: "", touched: true, cleared: true },
          })),
      }
    },
    [cells]
  )

  const secrets = useMemo<SecretSubmission>(() => {
    const cell = (name: string) => cells[name] ?? UNTOUCHED
    return {
      // Invariant 2. An untouched field has no value of its own, so it
      // submits "" — which the server reads as keep. There is deliberately
      // no branch that turns "untouched" into anything else.
      value: (name) => cell(name).value,
      touched: (name) => cell(name).touched,
      cleared: (name) => cell(name).cleared,
      stillSet: (name, storedNow) => {
        const c = cell(name)
        if (c.cleared) return false
        return c.value !== "" || storedNow
      },
    }
  }, [cells])

  return {
    loading: baseline === undefined,
    value,
    server: baseline,
    set,
    patch,
    dirty,
    revert,
    secret,
    secrets,
    settle,
  }
}

// ---------------------------------------------------------------------------
// The query-backed panel entry point
// ---------------------------------------------------------------------------

export type SettingsDraft<TData> = Draft<TData> & {
  save: () => void
  saving: boolean
}

export type SettingsDraftOptions<TData, TPayload, TResult> = {
  queryKey: QueryKey
  queryFn: () => Promise<TData>
  // The shape rendered before the first payload lands. Keep it a module
  // constant; it doubles as the "not configured yet" baseline.
  initial: TData
  save: ApiMutation<TPayload, TResult>
  // toPayload is where a panel's own submit shape lives — folding secret
  // drafts back in, re-serialising a textarea into a list, and so on. It
  // is called with the current draft, never with stale state.
  toPayload: (value: TData, secrets: SecretSubmission) => TPayload
  successToast?: string
  onSaved?: (result: TResult) => void
}

// useSettingsDraft wires a settings query and a save mutation onto the
// draft core. Note that reading `data` here is what makes the panel
// re-render on a refetch at all — which is the point: the invariant is
// defended deliberately rather than by whichever property the panel
// happened to touch during render.
export function useSettingsDraft<TData, TPayload, TResult>(
  opts: SettingsDraftOptions<TData, TPayload, TResult>
): SettingsDraft<TData> {
  const query = useQuery({ queryKey: opts.queryKey, queryFn: opts.queryFn })
  const draft = useDraft(query.data, opts.initial)

  // The mutation's onSuccess runs long after the render that created it,
  // so it reaches the draft through a ref rather than a captured copy.
  const draftRef = useRef(draft)
  draftRef.current = draft

  const saveMut = useApiMutation(opts.save, {
    successToast: opts.successToast,
    onSuccess: (result) => {
      // Settle before the invalidated query comes back: the refetch is
      // then free to hydrate, and the panel converges on what the server
      // actually stored rather than on what was typed.
      draftRef.current.settle()
      opts.onSaved?.(result)
    },
  })

  return {
    ...draft,
    // A failed GET is not a permanent "Loading…": the panel falls back to
    // `initial` and lets the admin write a configuration from scratch,
    // which is what an instance with no row stored looks like anyway.
    loading: draft.loading && !query.isError,
    save: () => saveMut.mutate(opts.toPayload(draft.value, draft.secrets)),
    saving: saveMut.isPending,
  }
}
