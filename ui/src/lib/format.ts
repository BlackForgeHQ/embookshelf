// Display formatters: a number in, a string for a human out.
//
// Not to be confused with `lib/formats.ts`, which is about book formats
// (EPUB, PDF, CBZ) — this module has no opinion about files.
//
// Everything here is deliberately total: every input, including the ones
// an API can hand us by omitting a field, has an answer that a person
// can read. Nothing rendered "NaN B" on purpose.
//
// The reading-position formatter is not here. `formatHMS` in
// `lib/locator.ts` turns a locator into a timestamp, and it belongs with
// the locator type it reads, not with these.

/**
 * A byte count as B / KB / MB / GB.
 *
 * Zero is "0 B", not an em dash. It used to be both: the BookDrop route
 * rendered "—" and the BookDrop settings panel rendered "0 B" from
 * character-for-character the same function. "0 B" wins because the
 * panel's zeroes are real — "Files on disk: 0 (0 B)", "Wiped 0 files
 * (0 B)" — and an em dash there claims the size is unknown when it is
 * known to be nothing. The route's genuinely-absent values are already
 * handled a layer up, by the cell that knows a field is missing and dims
 * it; a dash returned from here arrived looking like data.
 */
export function formatBytes(n: number): string {
  // Negative and non-finite are not sizes. They reach here from a count
  // subtracted past zero and from a `bytes` field the API left out.
  if (!Number.isFinite(n) || n <= 0) return "0 B"
  const units = ["B", "KB", "MB", "GB"]
  let value = n
  let u = 0
  while (value >= 1024 && u < units.length - 1) {
    value /= 1024
    u++
  }
  // One decimal below ten, none above: a 4.2 MB book is worth the
  // precision, a 431 KB one is not.
  return `${value.toFixed(value < 10 ? 1 : 0)} ${units[u]}`
}

/**
 * A span of seconds as "7h 42m" or "42m".
 *
 * Zero is an em dash here, which is the opposite of `formatBytes` and
 * deliberately so: there is no honest zero-length narration, so a zero
 * duration means the field never got written.
 */
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return "—"
  // Rounded once, on the total, so the rounding cannot push the minutes
  // past an hour and leave the hours behind. Spelled per-part — hours
  // floored, minutes rounded from the remainder — everything from 59m30s
  // to the hour rendered "60m".
  const totalMinutes = Math.round(seconds / 60)
  const h = Math.floor(totalMinutes / 60)
  const m = totalMinutes % 60
  return h > 0 ? `${h}h ${m}m` : `${m}m`
}

/**
 * A dollar amount, for money about to be spent on a narration run.
 *
 * Two decimals until it rounds to nothing, because "$0.00" for a run
 * that costs three cents reads as free.
 */
export function formatCost(usd: number): string {
  // A price we could not compute is not a free one. This is what
  // rendered "$NaN" when an estimate came back without a cost.
  if (!Number.isFinite(usd) || usd < 0) return "—"
  if (usd === 0) return "free"
  if (usd < 0.01) return "<$0.01"
  return `$${usd.toFixed(2)}`
}

/**
 * How long ago a timestamp was, in the largest whole unit.
 *
 * Lived privately inside ProvidersPanel until the Instance panel needed
 * the same phrasing three times — for uptime, for how stale the board is,
 * and for a failing provider's last error.
 */
export function relativeTime(ms: number): string {
  if (!ms) return "—"
  const diff = Date.now() - ms
  if (diff < 0) return "in the future"
  const s = Math.floor(diff / 1000)
  if (s < 60) return `${s}s ago`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  const d = Math.floor(h / 24)
  return `${d}d ago`
}
