import { createFileRoute } from '@tanstack/react-router';

export const Route = createFileRoute('/library')({
  component: LibraryPage,
});

function LibraryPage() {
  return (
    <main className="min-h-screen bg-paper-0 text-ink-1 px-8 py-16">
      <div className="mx-auto max-w-4xl space-y-4">
        <h1 className="text-3xl font-serif">Library</h1>
        <p className="text-ink-3 text-sm">
          Book grid pending — will consume <code>/api/v1/books</code>.
        </p>
      </div>
    </main>
  );
}
