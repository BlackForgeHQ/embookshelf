import { useMutation, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import type { QueryKey, UseMutationResult } from "@tanstack/react-query"

import type { ApiError } from "./client"

// ApiMutation pairs a mutation function with the query keys whose data it
// invalidates. Co-locating the two means callers don't have to remember
// which caches go stale when X mutates — the api module is the single
// source of truth. `invalidates` may be a static list or a closure when
// the keys depend on input or response (e.g. shelf rename invalidating
// books-on-that-shelf). See ADR-0017 for the realtime gap this fills.
export type Invalidates<TVars, TResult> =
  | ReadonlyArray<QueryKey>
  | ((vars: TVars, result: TResult) => ReadonlyArray<QueryKey>)

export type ApiMutation<TVars, TResult> = {
  fn: (vars: TVars) => Promise<TResult>
  invalidates: Invalidates<TVars, TResult>
}

export function defineMutation<TVars, TResult>(
  spec: ApiMutation<TVars, TResult>
): ApiMutation<TVars, TResult> {
  return spec
}

// Where a failure gets reported. A failure is reported once, and this is
// where the call site says where.
//
// "toast" is the default, and right for a mutation fired from a control
// that is about to go away — a row action, a menu item, a dialog that
// closes on success. "inline" is for a call site that renders `mut.error`
// itself, next to the control that failed: a form the user is still
// standing in, a detail pane whose error belongs to the thing on screen.
// Choosing "inline" means the hook keeps quiet and the error reaches the
// caller only through the returned `error`.
//
// The two are exclusive by construction: an inline reporter has no toast
// to word, so `errorToast` is not offered on that branch.
export type ErrorReporting<TVars> =
  | {
      reportErrors?: "toast"
      errorToast?: string | ((err: ApiError, vars: TVars) => string)
    }
  | { reportErrors: "inline"; errorToast?: never }

export type UseApiMutationOpts<TVars, TResult> = {
  successToast?: string | ((result: TResult, vars: TVars) => string)
  onSuccess?: (result: TResult, vars: TVars) => void
  // Runs after the failure has been reported (or deliberately not). For a
  // caller that has somewhere to put the error beyond rendering `error`
  // — `useConnectionTest` folds it into the outcome it displays.
  onError?: (err: ApiError, vars: TVars) => void
} & ErrorReporting<TVars>

// useApiMutation wraps useMutation with the project's standard onSuccess
// (invalidate registered keys + optional toast + caller hook) and
// onError (report the ApiError message where the caller asked for it,
// a toast unless told otherwise). Optimistic updates are not supported —
// there are zero callsites today; add an `onMutate` escape hatch when one
// materialises.
export function useApiMutation<TVars, TResult>(
  mutation: ApiMutation<TVars, TResult>,
  opts: UseApiMutationOpts<TVars, TResult> = {}
): UseMutationResult<TResult, ApiError, TVars> {
  const queryClient = useQueryClient()
  return useMutation<TResult, ApiError, TVars>({
    mutationFn: mutation.fn,
    onSuccess: (result, vars) => {
      const keys =
        typeof mutation.invalidates === "function"
          ? mutation.invalidates(vars, result)
          : mutation.invalidates
      for (const key of keys) {
        queryClient.invalidateQueries({ queryKey: key })
      }
      if (opts.successToast !== undefined) {
        const msg =
          typeof opts.successToast === "function"
            ? opts.successToast(result, vars)
            : opts.successToast
        toast.success(msg)
      }
      opts.onSuccess?.(result, vars)
    },
    onError: (err, vars) => {
      // Inline callers read `error` off the returned mutation; saying it
      // again here is the same sentence twice.
      if (opts.reportErrors !== "inline") {
        const msg =
          typeof opts.errorToast === "function"
            ? opts.errorToast(err, vars)
            : (opts.errorToast ?? err.message)
        toast.error(msg)
      }
      opts.onError?.(err, vars)
    },
  })
}
