import { useEffect } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { useNavigate, useRouterState } from "@tanstack/react-router"
import { toast } from "sonner"

import { bookdropQueryKey } from "./bookdrop"
import { bookQueryKey, booksQueryKey, librariesQueryKey, shelvesQueryKey } from "./books"
import { settingsUsersQueryKey } from "./settings"

// Event names the server publishes. Keep the union narrow so the dispatch
// map below is exhaustive — TypeScript will catch a typo before it ships.
type RealtimeEvent =
  | "bookdrop.updated"
  | "bookdrop.cleared"
  | "users.updated"
  | "shelf.public.updated"
  | "shelf.public.removed"
  | "kindle.sent"
  | "kindle.failed"
  | "guide.updated"

type Handler = (data: string) => void

type SharedShelfPayload = { slug: string }

type KindleResultPayload = { book_id?: string; error?: string }

type ReadingGuidePayload = { bookId?: string }

function parseGuidePayload(raw: string): ReadingGuidePayload {
  try {
    return JSON.parse(raw) as ReadingGuidePayload
  } catch {
    return {}
  }
}

function parseKindlePayload(raw: string): KindleResultPayload {
  try {
    return JSON.parse(raw) as KindleResultPayload
  } catch {
    return {}
  }
}

function parseSharedShelfPayload(raw: string): SharedShelfPayload | null {
  try {
    const parsed = JSON.parse(raw) as SharedShelfPayload
    if (typeof parsed.slug === "string" && parsed.slug.length > 0) {
      return parsed
    }
  } catch {
    /* fall through */
  }
  return null
}

// useRealtime opens a single EventSource to the server's SSE stream and
// invalidates the right react-query caches when background state changes.
// The native EventSource handles reconnection on its own (exponential
// backoff, 3 s default); we only wire up teardown on unmount.
export function useRealtime() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const router = useRouterState()

  useEffect(() => {
    // Dispatch table: event name → list of queryKeys to bust. Bookdrop
    // approvals flip books + libraries book counts, so invalidate the
    // three caches that consume those lists.
    const handlers: Record<RealtimeEvent, Handler> = {
      "bookdrop.updated": () => {
        queryClient.invalidateQueries({ queryKey: bookdropQueryKey })
        queryClient.invalidateQueries({ queryKey: booksQueryKey() })
        queryClient.invalidateQueries({ queryKey: librariesQueryKey })
        // A fresh import lands as an unshelved book — bump the count
        // alongside the books / libraries lists so the sidebar's
        // Unshelved entry reflects the new arrival without a refresh.
        queryClient.invalidateQueries({ queryKey: shelvesQueryKey })
      },
      // Bulk clear of imported/rejected rows — only the queue list needs
      // to refetch; books/libraries are unaffected by history deletion.
      "bookdrop.cleared": () => {
        queryClient.invalidateQueries({ queryKey: bookdropQueryKey })
      },
      "users.updated": () => {
        queryClient.invalidateQueries({ queryKey: settingsUsersQueryKey })
      },
      // A shared shelf's owner just edited it (membership, name,
      // accent). Refresh the sidebar list and any open books-on-shelf
      // query so every connected viewer sees the change in lockstep
      // (ADR-0017). The single broadcast costs O(1) on the server
      // regardless of viewer count.
      "shelf.public.updated": (raw) => {
        queryClient.invalidateQueries({ queryKey: shelvesQueryKey })
        const payload = parseSharedShelfPayload(raw)
        if (payload) {
          queryClient.invalidateQueries({
            queryKey: booksQueryKey({ shelf: payload.slug }),
          })
        } else {
          // Defensive: invalidate every books query when payload is
          // malformed. Worst case is one extra refetch per viewer.
          queryClient.invalidateQueries({ queryKey: booksQueryKey() })
        }
      },
      // A shared shelf was un-published or deleted. Drop it from the
      // sidebar; if the active route happens to be displaying that
      // shelf, redirect back to /library with a toast so the viewer
      // doesn't sit on a 404 mid-page.
      "shelf.public.removed": (raw) => {
        queryClient.invalidateQueries({ queryKey: shelvesQueryKey })
        const payload = parseSharedShelfPayload(raw)
        const search = router.location.search as { shelf?: string }
        if (
          payload &&
          router.location.pathname.startsWith("/library") &&
          search.shelf === payload.slug
        ) {
          toast.info("Shared shelf is no longer available.")
          void navigate({ to: "/library", search: {} })
        }
        if (payload) {
          queryClient.invalidateQueries({
            queryKey: booksQueryKey({ shelf: payload.slug }),
          })
        }
      },
      // Send-to-Kindle runs on the queue, so the request returns 202 long
      // before the mail is away. These two events are the only feedback
      // the user gets. The server routes them to the requesting user's
      // subscriptions, so anything arriving here is ours — no filtering.
      "kindle.sent": () => {
        toast.success("Sent to Kindle.")
      },
      "kindle.failed": (raw) => {
        const { error } = parseKindlePayload(raw)
        toast.error(error ? `Send to Kindle failed: ${error}` : "Send to Kindle failed.")
      },
      // A guide finished generating. Bust that book's cache so an open
      // detail page fills in; no toast, because a bulk run over a
      // thousand books would otherwise bury the screen.
      "guide.updated": (raw) => {
        const { bookId } = parseGuidePayload(raw)
        if (bookId) {
          void queryClient.invalidateQueries({ queryKey: bookQueryKey(bookId) })
        }
      },
    }

    const es = new EventSource("/events", { withCredentials: true })

    // The MessageEvent.data field carries the raw JSON payload; some
    // events (bookdrop, users) ignore it, others (shelf.public.*)
    // parse it for the affected slug.
    const subscriptions: Array<[RealtimeEvent, (e: MessageEvent) => void]> = []
    for (const name of Object.keys(handlers) as Array<RealtimeEvent>) {
      const listener = (e: MessageEvent) =>
        handlers[name](typeof e.data === "string" ? e.data : "")
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
  }, [queryClient, navigate, router])
}
