// The reading-activity heatmap ramp, shared by the Dashboard and Stats
// views. It was copy-pasted in both routes with a comment promising they
// stay in sync; now the promise is structural. Colors are the
// --color-heat-* tokens from styles.css so the ramp re-themes with the
// palette.

/** Minutes read in a day → the cell's background color. */
export function heatColor(minutes: number): string {
  if (minutes === 0) return "var(--color-paper-2)"
  if (minutes < 20) return "var(--color-heat-1)"
  if (minutes < 35) return "var(--color-heat-2)"
  if (minutes < 50) return "var(--color-heat-3)"
  return "var(--color-heat-4)"
}
