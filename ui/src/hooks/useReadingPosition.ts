import { useCallback, useEffect, useRef } from "react"
import { useNavigate } from "@tanstack/react-router"

import type { Locator } from "@/lib/locator"
import { updateProgress } from "@/api/books"
import { encodeLocator } from "@/lib/locator"

/**
 * A reading position: how far through the book the user is (0-1) and the
 * token a reader resumes from.
 *
 * Held encoded rather than as a Locator, because encoding is what the
 * write does and doing it once at report time keeps the held value the
 * same shape the debounce, the ceiling and the exits all ship.
 */
type PendingPosition = {
  progress: number
  token: string
}

export type UseReadingPositionOptions = {
  /** The book being read. Writes and the exit route both key off it. */
  bookId: string
  /**
   * How long to hold a position before writing. Readers emit progress far
   * faster than it's worth writing: page turns land every few seconds,
   * audio timeupdate fires several times a second.
   */
  debounceMs?: number
}

/** The window for a reader whose positions arrive a page turn at a time. */
export const PAGE_DEBOUNCE_MS = 600

/**
 * The window for a reader that reports continuously. Longer because the
 * positions are worth less individually — a second of audio either way
 * is not a place in the book anyone can point at — and because the
 * ceiling below, not this, is what bounds the loss.
 */
export const CONTINUOUS_DEBOUNCE_MS = 5000

/**
 * The longest a position is held while progress keeps arriving.
 *
 * The debounce restarts on every call, which is right for page turns —
 * they land seconds apart, so the window elapses normally. Audio reports
 * progress several times a second, so the window was cancelled and
 * restarted faster than it could ever fire and the debounce was
 * effectively infinite: only an explicit flush wrote anything (#204).
 *
 * Twenty seconds is the most a continuously-reporting reader can lose to
 * a crash, and it is far enough above the audio window that a settling
 * reader still writes on the debounce rather than on the ceiling.
 */
const MAX_HOLD_MS = 20_000

/**
 * useReadingPosition is where a book's reading position is written, and
 * the only place that decides when.
 *
 * A shell reports where the reader is. It does not hold a timer, encode a
 * token, call the API, or watch for the session ending — those were the
 * contract this module used to *document* for its callers, and a
 * contract restated at four call sites is one the interface is not
 * carrying (#205). The four exit closures it replaces were identical,
 * three of them down to the comment.
 *
 * Every way a session can end is handled here:
 *
 *   - **exit** — the user leaves the reader; writes, then navigates
 *   - **settle** — the reader stops moving without leaving (playback
 *     pausing is the only caller today)
 *   - **backgrounding** — visibilitychange and pagehide
 *   - **unmount** — the backstop, which fires mid-teardown and is why
 *     exit does not rely on it
 */
export function useReadingPosition({
  bookId,
  debounceMs = PAGE_DEBOUNCE_MS,
}: UseReadingPositionOptions) {
  const navigate = useNavigate()

  const pendingRef = useRef<PendingPosition | null>(null)
  const timerRef = useRef<number | null>(null)
  // Fires MAX_HOLD_MS after the first still-unwritten position and is
  // deliberately not restarted by later ones, so a reader that never
  // settles cannot hold a position indefinitely.
  const ceilingRef = useRef<number | null>(null)

  // Held in a ref so the returned callbacks keep a stable identity: the
  // shells pass them straight into child reader props, and a fresh
  // function each render would retrigger those children's effects on
  // every page turn. Reading through the ref also means a position
  // reported after a book change cannot post to the book the hook
  // mounted with.
  const bookRef = useRef(bookId)
  useEffect(() => {
    bookRef.current = bookId
  }, [bookId])

  const cancelTimer = useCallback(() => {
    if (timerRef.current !== null) {
      window.clearTimeout(timerRef.current)
      timerRef.current = null
    }
  }, [])

  const cancelCeiling = useCallback(() => {
    if (ceilingRef.current !== null) {
      window.clearTimeout(ceilingRef.current)
      ceilingRef.current = null
    }
  }, [])

  /**
   * Writes any held position now. No-op when nothing is pending, which
   * is what makes every exit below safe to fire more than once.
   */
  const write = useCallback(() => {
    cancelTimer()
    cancelCeiling()
    const snapshot = pendingRef.current
    pendingRef.current = null
    if (snapshot) {
      void updateProgress(bookRef.current, snapshot.progress, snapshot.token)
    }
  }, [cancelCeiling, cancelTimer])

  /**
   * Records where the reader is, replacing any position still waiting,
   * and restarts the window. Latest-wins: a reader that emits five
   * positions while the user flicks through pages should write the one
   * they stopped on.
   */
  const report = useCallback(
    (progress: number, at: Locator) => {
      pendingRef.current = { progress, token: encodeLocator(at) }

      // Latest-wins, but not for longer than the ceiling. The debounce
      // restarts on every call; this one does not, which is what makes
      // it reachable for a reader reporting faster than the window.
      if (ceilingRef.current === null) {
        ceilingRef.current = window.setTimeout(write, MAX_HOLD_MS)
      }
      cancelTimer()
      timerRef.current = window.setTimeout(write, debounceMs)
    },
    [cancelTimer, debounceMs, write]
  )

  /**
   * The reader stopped moving but the session continues — playback
   * pausing. Writes now rather than waiting out a window the reader will
   * no longer close.
   */
  const settle = write

  /** Leaves the reader for the book page, writing the position first. */
  const exit = useCallback(() => {
    write()
    void navigate({ to: "/book/$id", params: { id: bookRef.current } })
  }, [navigate, write])

  // The exits that are not the shell's to see. Left to callers before,
  // and implemented by none of them, so a mobile listener who swiped the
  // browser away lost the session: unmount effects do not run on a tab
  // kill (#204).
  useEffect(() => {
    const onHide = () => {
      if (document.visibilityState === "hidden") write()
    }
    // pagehide as well as visibilitychange: iOS Safari has historically
    // fired one without the other, and a duplicate write is a no-op.
    document.addEventListener("visibilitychange", onHide)
    window.addEventListener("pagehide", write)
    return () => {
      document.removeEventListener("visibilitychange", onHide)
      window.removeEventListener("pagehide", write)
    }
  }, [write])

  // Backstop for a session too short to close a single debounce window.
  // Effects run cleanup on unmount only for an empty dep list, and the
  // refs it touches are stable, so nothing here goes stale.
  useEffect(() => {
    return () => {
      cancelTimer()
      cancelCeiling()
      const snapshot = pendingRef.current
      pendingRef.current = null
      if (snapshot) {
        void updateProgress(bookRef.current, snapshot.progress, snapshot.token)
      }
    }
  }, [cancelCeiling, cancelTimer])

  return { report, settle, exit }
}
