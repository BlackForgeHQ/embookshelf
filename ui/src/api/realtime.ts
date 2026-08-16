import { useEffect } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { useNavigate, useRouter } from "@tanstack/react-router"
import { toast } from "sonner"

import { bookAudiobookQueryKey } from "./audiobooks"
import { bookdropQueryKey } from "./bookdrop"
import { bookQueryKey, booksQueryKey, librariesQueryKey, shelvesQueryKey } from "./books"
import { bookGuideQueryKey } from "./guides"
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
  | "audiobook.updated"

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
  // The router instance, not a slice of its state. Only the two
  // shared-shelf handlers below need the location, and they need it at
  // event time — so they read `router.state.location`, a live getter, at
  // the moment the event arrives. Subscribing to the location instead
  // (useRouterState) put a value that changes on every navigation into
  // this effect's dependency array, which tore the EventSource down and
  // reopened it on each one, costing a reconnect round-trip and
  // defeating the single-connection-per-session property in
  // ARCHITECTURE.md §5.7. `router` and `navigate` both keep one identity
  // for the life of the app, so the effect now runs exactly once.
  const router = useRouter()

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
        // Read at event time: the viewer may have navigated many times
        // since this listener was attached.
        const location = router.state.location
        const search = location.search as { shelf?: string }
        if (
          payload &&
          location.pathname.startsWith("/library") &&
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
      // A guide finished generating. Bust the keys the panels actually
      // read — declared in BOOK_EVENT_KEYS below, not guessed here: the
      // old handlers invalidated ["book", id] alone, which the guide and
      // narration panels never read, so the events reached nothing
      // (#349). No toast, because a bulk run over a thousand books
      // would otherwise bury the screen.
      "guide.updated": (raw) => {
        invalidateBookEvent(queryClient, "guide.updated", parseGuidePayload(raw).bookId)
      },
      // A narration reached a terminal state. No toast: a run takes
      // tens of minutes and the user is rarely looking at the page it
      // finishes on.
      "audiobook.updated": (raw) => {
        invalidateBookEvent(queryClient, "audiobook.updated", parseGuidePayload(raw).bookId)
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

// BOOK_EVENT_KEYS is the one declared mapping from a book-scoped SSE
// event to the query keys it busts — the SSE twin of defineMutation's
// `invalidates` (#349). HTTP mutations declare their keys beside the
// mutation; SSE was the one path that re-guessed them per handler, and
// the guess had already drifted: the panels read their own keys.
//
// audiobook.updated carries the book key too: a finalize lands a new
// files row (the MP3), so the detail page's formats and chapters
// change with it. The guide is not embedded in the detail payload, so
// its event busts only its own key.
const BOOK_EVENT_KEYS = {
  "guide.updated": (bookId: string) => [bookGuideQueryKey(bookId)],
  "audiobook.updated": (bookId: string) => [
    bookAudiobookQueryKey(bookId),
    bookQueryKey(bookId),
  ],
} as const

function invalidateBookEvent(
  queryClient: ReturnType<typeof useQueryClient>,
  event: keyof typeof BOOK_EVENT_KEYS,
  bookId: string | undefined,
) {
  if (!bookId) return
  for (const key of BOOK_EVENT_KEYS[event](bookId)) {
    void queryClient.invalidateQueries({ queryKey: key })
  }
}
