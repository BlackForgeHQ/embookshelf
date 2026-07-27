import { api } from "./client"
import { defineQuery } from "./query"

// Mirrors internal/handler/reading_stats.go readingStatsDTO.
export type ReadingStats = {
  heatmapDays: number
  heatmapMinutes: Array<number>
  thisWeekMinutes: number
  currentStreak: number
  quarterMinutes: number
  quarterSessions: number
  allTimeMinutes: number
}

export async function fetchReadingStats(days?: number): Promise<ReadingStats> {
  const query = days ? `?days=${encodeURIComponent(days)}` : ""
  const { reading } = await api<{ reading: ReadingStats }>(
    `/api/v1/stats/reading${query}`
  )
  return reading
}

// Stable query key so mutations elsewhere (progress, delete-book) can
// invalidate reading stats in one call.
export const readingStatsQueryKey = (days?: number) =>
  days ? (["stats", "reading", days] as const) : (["stats", "reading"] as const)

export const readingStatsQuery = (days?: number) =>
  defineQuery({
    key: readingStatsQueryKey(days),
    fn: () => fetchReadingStats(days),
  })
