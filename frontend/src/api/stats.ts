import { api } from './client';

export type StatsBucket = { label: string; count: number };
export type StatsYearBucket = { decade: number; count: number };
export type StatsRatingBucket = { rating: number; count: number };

export type Stats = {
  totals: {
    books: number;
    booksWithCover: number;
  };
  user: {
    reading: number;
    finished: number;
    annotations: number;
    shelves: number;
    smartShelves: number;
  };
  libraries: StatsBucket[];
  formats: StatsBucket[];
  topAuthors: StatsBucket[];
  topTags: StatsBucket[];
  yearHistogram: StatsYearBucket[];
  ratings: StatsRatingBucket[];
};

export async function fetchStats(): Promise<Stats> {
  const { stats } = await api<{ stats: Stats }>('/api/v1/stats');
  return stats;
}

export const statsQueryKey = ['stats'] as const;
