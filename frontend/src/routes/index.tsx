import { useQuery } from '@tanstack/react-query';
import { createFileRoute, Link } from '@tanstack/react-router';

import { api } from '@/api/client';

type Health = { status: string; diskMode: string };

export const Route = createFileRoute('/')({
  component: HomePage,
});

function HomePage() {
  const { data, isLoading, error } = useQuery<Health>({
    queryKey: ['healthcheck'],
    queryFn: () => api<Health>('/api/v1/healthcheck'),
  });

  return (
    <main className="min-h-screen bg-paper-0 text-ink-1 px-8 py-16">
      <div className="mx-auto max-w-2xl space-y-6">
        <h1 className="text-4xl font-serif">embookshelf</h1>
        <p className="text-ink-2">
          Frontend scaffold is live. Backend JSON endpoints land next.
        </p>

        <section className="rounded border border-rule bg-paper-1 p-4">
          <h2 className="text-lg font-serif mb-2">Healthcheck</h2>
          {isLoading && <p className="text-ink-3">loading…</p>}
          {error && <p className="text-accent">backend unreachable</p>}
          {data && (
            <dl className="font-mono text-sm grid grid-cols-[8rem_1fr] gap-y-1">
              <dt className="text-ink-3">status</dt>
              <dd>{data.status}</dd>
              <dt className="text-ink-3">diskMode</dt>
              <dd>{data.diskMode}</dd>
            </dl>
          )}
        </section>

        <nav className="flex gap-4 text-sm">
          <Link to="/login" className="underline">Login</Link>
          <Link to="/library" className="underline">Library</Link>
        </nav>
      </div>
    </main>
  );
}
