import { useEffect } from "react"
import { useQueryClient } from "@tanstack/react-query"

import { bookdropQueryKey } from "./bookdrop"
import { booksQueryKey, librariesQueryKey } from "./books"

// Event names the server publishes. Keep the union narrow so the dispatch
// map below is exhaustive — TypeScript will catch a typo before it ships.
type RealtimeEvent = "bookdrop.updated" | "bookdrop.cleared"

type Handler = () => void

// useRealtime opens a single EventSource to the server's SSE stream and
// invalidates the right react-query caches when background state changes.
// The native EventSource handles reconnection on its own (exponential
// backoff, 3 s default); we only wire up teardown on unmount.
export function useRealtime() {
  const queryClient = useQueryClient()

  useEffect(() => {
    // Dispatch table: event name → list of queryKeys to bust. Bookdrop
    // approvals flip books + libraries book counts, so invalidate the
    // three caches that consume those lists.
    const handlers: Record<RealtimeEvent, Handler> = {
      "bookdrop.updated": () => {
        queryClient.invalidateQueries({ queryKey: bookdropQueryKey })
        queryClient.invalidateQueries({ queryKey: booksQueryKey() })
        queryClient.invalidateQueries({ queryKey: librariesQueryKey })
      },
      // Bulk clear of imported/rejected rows — only the queue list needs
      // to refetch; books/libraries are unaffected by history deletion.
      "bookdrop.cleared": () => {
        queryClient.invalidateQueries({ queryKey: bookdropQueryKey })
      },
    }

    const es = new EventSource("/events", { withCredentials: true })

    // addEventListener's second argument is typed for DOM events; cast to
    // MessageEvent-compatible so TypeScript doesn't whine. We don't parse
    // the data today (no payload fields need it) — that changes when
    // future events carry structured payloads.
    const subscriptions: Array<[RealtimeEvent, (e: MessageEvent) => void]> = []
    for (const name of Object.keys(handlers) as Array<RealtimeEvent>) {
      const listener = () => handlers[name]()
      es.addEventListener(name, listener as EventListener)
      subscriptions.push([name, listener])
    }

    // onerror fires on transient disconnects; the browser reconnects
    // automatically so we don't need to rebuild the source. Log-only.
    es.onerror = () => {
      // Mildly noisy during dev reconnects — keep at debug level.
      if (import.meta.env.DEV) {
        console.debug("[realtime] EventSource error — browser will retry")
      }
    }

    return () => {
      for (const [name, listener] of subscriptions) {
        es.removeEventListener(name, listener as EventListener)
      }
      es.close()
    }
  }, [queryClient])
}
