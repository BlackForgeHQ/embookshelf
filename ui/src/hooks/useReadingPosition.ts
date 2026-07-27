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
 * useReadingPosition debounces reading-position writes and guarantees the
 * last one is not lost.
 *
 * Every reader shell needs the same three things — hold the newest position,
 * ship it once the user settles, and force it out when the session ends.
 * Keeping that in one place is what makes "the session ended" a single list
 * to extend: previously each shell decided for itself, and the audio shell
 * carried a comment promising a save-on-pause path it never actually had.
 *
 * Callers must flush on every exit that isn't an unmount — pausing playback,
 * navigating away, backgrounding the tab. Unmount is handled here as a
 * backstop, not as the primary path.
 */
export function useReadingPosition({
  save,
  debounceMs = DEFAULT_DEBOUNCE_MS,
}: UseReadingPositionOptions) {
  const pendingRef = useRef<ReadingPosition | null>(null)
  const timerRef = useRef<number | null>(null)

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
      cancelTimer()
      timerRef.current = window.setTimeout(flush, debounceMs)
    },
    [cancelTimer, debounceMs, flush]
  )

  // Backstop for a session too short to close a single debounce window.
  // Effects run cleanup on unmount only for an empty dep list, and the refs
  // it touches are stable, so nothing here goes stale.
  useEffect(() => {
    return () => {
      cancelTimer()
      const snapshot = pendingRef.current
      pendingRef.current = null
      if (snapshot) saveRef.current(snapshot.progress, snapshot.token)
    }
  }, [cancelTimer])

  return { queue, flush }
}
