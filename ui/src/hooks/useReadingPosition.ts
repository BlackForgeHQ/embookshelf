import { useCallback, useEffect, useRef } from "react"

/**
 * A reading position: how far through the book the user is (0-1) and an
 * opaque token the reader uses to resume exactly where they left off.
 *
 * The token is opaque here: `lib/locator` owns every shape it can take
 * and is the only module that encodes or decodes one.
 */
export type ReadingPosition = {
  progress: number
  token: string
}

export type UseReadingPositionOptions = {
  /** Persists a position. Called with the newest values, never a stale copy. */
  save: (progress: number, token: string) => void
  /**
   * How long to hold a position before saving. Readers emit progress far
   * faster than it's worth writing: page turns land every few seconds,
   * audio timeupdate fires several times a second.
   */
  debounceMs?: number
}

const DEFAULT_DEBOUNCE_MS = 600

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
 * useReadingPosition debounces reading-position writes and guarantees the
 * last one is not lost.
 *
 * Every reader shell needs the same three things — hold the newest position,
 * ship it once the user settles, and force it out when the session ends.
 * Keeping that in one place is what makes "the session ended" a single list
 * to extend: previously each shell decided for itself, and the audio shell
 * carried a comment promising a save-on-pause path it never actually had.
 *
 * The exits are this module's, not its callers': backgrounding, page
 * hide and unmount are all handled here. A caller still flushes for the
 * moments only it can see — pausing playback, leaving the reader — and
 * a duplicate flush is a no-op.
 */
export function useReadingPosition({
  save,
  debounceMs = DEFAULT_DEBOUNCE_MS,
}: UseReadingPositionOptions) {
  const pendingRef = useRef<ReadingPosition | null>(null)
  const timerRef = useRef<number | null>(null)
  // Fires MAX_HOLD_MS after the first still-unwritten position and is
  // deliberately not restarted by later ones, so a reader that never
  // settles cannot hold a position indefinitely.
  const ceilingRef = useRef<number | null>(null)

  // Held in a ref so queue and flush keep a stable identity: the shells pass
  // them straight into child reader props, and a fresh function each render
  // would retrigger those children's effects on every page turn. Reading
  // through the ref also means a save captured at mount can't keep posting
  // to a book the user has already left.
  const saveRef = useRef(save)
  useEffect(() => {
    saveRef.current = save
  }, [save])

  const cancelTimer = useCallback(() => {
    if (timerRef.current !== null) {
      window.clearTimeout(timerRef.current)
      timerRef.current = null
    }
  }, [])

  /** Writes any held position now. No-op when nothing is pending. */
  const flush = useCallback(() => {
    cancelTimer()
    if (ceilingRef.current !== null) {
      window.clearTimeout(ceilingRef.current)
      ceilingRef.current = null
    }
    const snapshot = pendingRef.current
    pendingRef.current = null
    if (snapshot) saveRef.current(snapshot.progress, snapshot.token)
  }, [cancelTimer])

  /**
   * Records a position, replacing any still waiting, and restarts the
   * window. Latest-wins: a reader that emits five positions while the user
   * flicks through pages should write the one they stopped on.
   */
  const queue = useCallback(
    (progress: number, token: string) => {
      pendingRef.current = { progress, token }

      // Latest-wins, but not for longer than the ceiling. The debounce
      // restarts on every call; this one does not, which is what makes
      // it reachable for a reader reporting faster than the window.
      if (ceilingRef.current === null) {
        ceilingRef.current = window.setTimeout(flush, MAX_HOLD_MS)
      }
      cancelTimer()
      timerRef.current = window.setTimeout(flush, debounceMs)
    },
    [cancelTimer, debounceMs, flush]
  )

  // The exits that are not an unmount. The hook's contract named all
  // three and enforced one; the other two were left to callers, who
  // implemented "leaving the reader" four times by copy-paste and
  // backgrounding not at all — so a mobile listener who swiped the
  // browser away lost the session, because unmount effects do not run on
  // a tab kill (#204).
  useEffect(() => {
    const onHide = () => {
      if (document.visibilityState === "hidden") flush()
    }
    // pagehide as well as visibilitychange: iOS Safari has historically
    // fired one without the other, and a duplicate flush is a no-op.
    document.addEventListener("visibilitychange", onHide)
    window.addEventListener("pagehide", flush)
    return () => {
      document.removeEventListener("visibilitychange", onHide)
      window.removeEventListener("pagehide", flush)
    }
  }, [flush])

  // Backstop for a session too short to close a single debounce window.
  // Effects run cleanup on unmount only for an empty dep list, and the refs
  // it touches are stable, so nothing here goes stale.
  useEffect(() => {
    return () => {
      cancelTimer()
      if (ceilingRef.current !== null) window.clearTimeout(ceilingRef.current)
      const snapshot = pendingRef.current
      pendingRef.current = null
      if (snapshot) saveRef.current(snapshot.progress, snapshot.token)
    }
  }, [cancelTimer])

  return { queue, flush }
}
