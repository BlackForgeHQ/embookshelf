import { useCallback, useRef, useState } from "react"

import type { ApiError } from "@/api/client"
import { useApiMutation } from "@/api/mutation"

// A connection test produces a result to display, not an error to throw.
//
// Four sites invented this independently: two inside the OIDC panel, one
// in reading guides, one in audiobooks. Each held its own result state,
// each decided separately whether a refused endpoint was a toast or an
// inline block, and one cleared its result on failure so a rejected
// credential showed nothing at all.
//
// The asymmetry they were all working around is that an endpoint can fail
// two ways. It can answer, saying no — a wrong key, a bad issuer — which
// arrives as a 200 with `ok: false`. Or the request can fail to complete
// at all — DNS, a 500, a dropped connection — which arrives as a thrown
// ApiError. To the admin those are the same event: the test ran, here is
// what it found. This module collapses them into one outcome, so a panel
// renders one thing and never has to write a catch.
//
// That collapse is why the module still exists on top of `useApiMutation`
// rather than being replaced by it: half of what it reports — the refusal
// that arrives as a 200 — never passes through a mutation's error path at
// all, so no reporting choice made there could reach it. What is no
// longer this module's business is *where* a failure is said. That is one
// decision the shared hook owns for every mutation in the app, and this
// one takes it explicitly, below, as `reportErrors: "inline"`.

export type ConnectionTestOutcome<TResult> = {
  ok: boolean
  // One sentence for the admin, always present — including for a request
  // that never reached the server.
  message: string
  // The server's own payload, absent when the request itself failed. Panels
  // with more to show (the OIDC check list) read it from here.
  data?: TResult
}

export type ConnectionTest<TVars, TResult> = {
  // null until the first run. Rendered by `ConnectionTestReport`.
  outcome: ConnectionTestOutcome<TResult> | null
  running: boolean
  run: (vars: TVars) => void
  clear: () => void
}

export type ConnectionTestOptions<TVars, TResult> = {
  // Just the call. An `ApiMutation` satisfies this structurally, which is
  // how the api modules' test endpoints are passed; a test has nothing to
  // invalidate, so the rest of that type would be noise here.
  test: { fn: (vars: TVars) => Promise<TResult> }
  // read turns a completed response into the verdict and the sentence.
  // Every test endpoint answers 200 with its own success flag, so this is
  // where a panel says what "worked" means for its engine. `vars` is
  // passed too: a probe often reads better as "sent to <address>" than as
  // anything the empty 200 body could say.
  read: (result: TResult, vars: TVars) => { ok: boolean; message: string }
  // unreachable turns a request that never completed into the same shape.
  // Defaults to the ApiError's own message.
  unreachable?: (err: ApiError, vars: TVars) => string
}

const NOTHING = [] as const

export function useConnectionTest<TVars, TResult>(
  opts: ConnectionTestOptions<TVars, TResult>
): ConnectionTest<TVars, TResult> {
  const [outcome, setOutcome] = useState<ConnectionTestOutcome<TResult> | null>(
    null
  )

  // The interpreters are closures rebuilt every render; the mutation
  // callbacks fire much later, so they read the newest ones through refs.
  const readRef = useRef(opts.read)
  readRef.current = opts.read
  const unreachableRef = useRef(opts.unreachable)
  unreachableRef.current = opts.unreachable

  const mut = useApiMutation<TVars, TResult>(
    // A test has nothing to invalidate: it reads an endpoint, it does not
    // change one.
    { fn: opts.test.fn, invalidates: NOTHING },
    {
      // A toast is gone in four seconds and the admin is mid-form; the
      // point of pressing Test is to read the answer next to the field
      // that caused it. The outcome below is that answer.
      reportErrors: "inline",
      onSuccess: (result, vars) => {
        setOutcome({ ...readRef.current(result, vars), data: result })
      },
      onError: (err, vars) => {
        setOutcome({
          ok: false,
          message:
            unreachableRef.current?.(err, vars) ||
            err.message ||
            "The request never reached the server.",
        })
      },
    }
  )

  const run = useCallback(
    (vars: TVars) => {
      mut.mutate(vars)
    },
    [mut]
  )

  const clear = useCallback(() => setOutcome(null), [])

  return { outcome, running: mut.isPending, run, clear }
}
