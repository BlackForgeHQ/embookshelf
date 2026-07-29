// The self-terminating poll, declared once.
//
// Two surfaces watch something that finishes — a narration run and the
// reading-guide backfill — and each wrote its own four-second interval
// and its own "stop when idle" predicate (#197).

/**
 * How often a live run is re-read while it is moving.
 *
 * Declared once. Both the narration panel and the guide run polled at
 * four seconds and each wrote the number down (#197).
 */
export const LIVE_POLL_MS = 4000

/**
 * A self-terminating poll: the shared cadence while `active` holds of the
 * data already fetched, and off otherwise.
 *
 * A predicate rather than an interval because the point is that it
 * stops — an idle instance should never be polled, and a run that has
 * finished has nothing left to report.
 */
export function pollWhile<T>(
  active: (data: T) => boolean,
): (query: { state: { data: T | undefined | null } }) => number | false {
  return (query) => {
    const data = query.state.data
    // Null is a book with no run at all, and undefined is a first fetch
    // still in flight. Neither is something to poll about.
    return data !== undefined && data !== null && active(data) ? LIVE_POLL_MS : false
  }
}
