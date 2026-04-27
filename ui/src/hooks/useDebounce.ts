import { useEffect, useState } from "react"

/**
 * useDebounce returns `value` after it has been stable for `delayMs`.
 * Used by the search surfaces to throttle keystrokes into one HTTP
 * request per pause.
 */
export function useDebounce<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value), delayMs)
    return () => clearTimeout(t)
  }, [value, delayMs])
  return debounced
}
