import { useQuery } from "@tanstack/react-query"
import type {
  QueryKey,
  UseQueryOptions,
  UseQueryResult,
} from "@tanstack/react-query"

import type { ApiError } from "./client"

// ApiQuery is the read-side twin of ApiMutation.
//
// A mutation already arrives as a spec: the call and the caches it
// invalidates, declared together next to the endpoint, so sixty-plus call
// sites never have to remember what goes stale. Reads had no such thing.
// Every call site re-paired a key with a fetcher and, worse, decided the
// caching policy on the spot — so the policy drifted. Ten sites read the
// current user; eight declared a 60s stale time and two declared nothing,
// which meant those two refetched `/api/v1/me` on any mount more than
// thirty seconds into a session. Nothing was wrong at either site: the
// question "how long is this endpoint's answer good for?" simply had no
// home, so it was answered ten times.
//
// This module gives it one. A spec is key + fetcher + policy, written
// where the endpoint is written; a call site names the resource and
// nothing else. What a call site may still say is what only it knows —
// whether the query is ready to run, whether to poll, what to show while
// the arguments change. What it may not say is how long the answer keeps,
// because that is a property of the endpoint, not of the screen.

export type ApiQuery<TData> = {
  key: QueryKey
  fn: () => Promise<TData>
  /**
   * How long a fetched payload counts as fresh. Omitted means the
   * client-wide default in `router.tsx` — right for anything that a
   * mutation invalidates promptly, wrong for a read that half the app
   * performs on mount.
   */
  staleTime?: number
  /** How long an unused payload survives in cache before eviction. */
  gcTime?: number
  /**
   * Retry policy, when this endpoint's failures are not worth retrying —
   * a token check that answers "invalid" is an answer, not an outage.
   */
  retry?: UseQueryOptions<TData, ApiError>["retry"]
}

export function defineQuery<TData>(spec: ApiQuery<TData>): ApiQuery<TData> {
  return spec
}

/**
 * What a call site may still decide. Deliberately not a passthrough of
 * `UseQueryOptions`: `staleTime` and `gcTime` are missing on purpose, so
 * "every current-user read shares one policy" is a property of the code
 * rather than a convention that holds until the next call site.
 */
export type ApiQueryOpts<TData> = {
  /** False while the arguments the query needs are not there yet. */
  enabled?: boolean
  /** Polling, for a call site watching a job it just started. */
  refetchInterval?: UseQueryOptions<TData, ApiError>["refetchInterval"]
  /** Usually `(prev) => prev`, to hold the last page while a filter changes. */
  placeholderData?: UseQueryOptions<TData, ApiError>["placeholderData"]
}

/**
 * The spec as plain react-query options, for the callers that do not use
 * the hook: a route's `beforeLoad` / loader reaching for
 * `ensureQueryData`, and `useQueries`.
 */
export function apiQueryOptions<TData>(
  spec: ApiQuery<TData>,
  opts: ApiQueryOpts<TData> = {}
): UseQueryOptions<TData, ApiError> {
  return {
    queryKey: spec.key,
    queryFn: spec.fn,
    staleTime: spec.staleTime,
    gcTime: spec.gcTime,
    retry: spec.retry,
    ...opts,
  }
}

export function useApiQuery<TData>(
  spec: ApiQuery<TData>,
  opts: ApiQueryOpts<TData> = {}
): UseQueryResult<TData, ApiError> {
  return useQuery<TData, ApiError>(apiQueryOptions(spec, opts))
}

/**
 * The caching policy for session-scoped reads: who is signed in, and what
 * this instance is configured to allow. Both are cheap, both change rarely
 * within a session, and both are read by nearly every screen — so the
 * answer keeps for a minute and every reader of them says so by sharing
 * this constant rather than by repeating the number.
 */
export const SESSION_STALE_TIME = 60_000

/**
 * The policy for reads that are effectively static for a session — the
 * login page's provider list, the build's version string. Refetching them
 * costs a round trip and can never change the screen.
 */
export const INSTANCE_STALE_TIME = 5 * 60_000
