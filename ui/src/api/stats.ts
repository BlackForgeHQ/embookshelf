import { api } from "./client"

export type StatsBucket = { label: string; count: number }
export type StatsYearBucket = { decade: number; count: number }
export type StatsRatingBucket = { rating: number; count: number }

export type Stats = {
  totals: {
    books: number
    booksWithCover: number
  }
  user: {
    reading: number
    finished: number
    annotations: number
    shelves: number
    smartShelves: number
  }
  libraries: Array<StatsBucket>
  formats: Array<StatsBucket>
  topAuthors: Array<StatsBucket>
  topTags: Array<StatsBucket>
  yearHistogram: Array<StatsYearBucket>
  ratings: Array<StatsRatingBucket>
}

export async function fetchStats(): Promise<Stats> {
  const { stats } = await api<{ stats: Stats }>("/api/v1/stats")
  return stats
}

export const statsQueryKey = ["stats"] as const
