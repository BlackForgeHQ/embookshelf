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

export type UseApiMutationOpts<TVars, TResult> = {
  successToast?: string | ((result: TResult, vars: TVars) => string)
  errorToast?: string | ((err: ApiError, vars: TVars) => string)
  onSuccess?: (result: TResult, vars: TVars) => void
}

// useApiMutation wraps useMutation with the project's standard onSuccess
// (invalidate registered keys + optional toast + caller hook) and
// onError (toast the ApiError message). Optimistic updates are not
// supported — there are zero callsites today; add an `onMutate` escape
// hatch when one materialises.
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
      const msg =
        typeof opts.errorToast === "function"
          ? opts.errorToast(err, vars)
          : (opts.errorToast ?? err.message)
      toast.error(msg)
    },
  })
}
