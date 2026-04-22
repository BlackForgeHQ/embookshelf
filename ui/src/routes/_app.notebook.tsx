import { useMemo } from 'react';
import {
  useMutation,
  useQueries,
  useQueryClient,
} from '@tanstack/react-query';
import { createFileRoute, useNavigate } from '@tanstack/react-router';

import {
  annotationKind,
  bookAnnotationsQueryKey,
  deleteAnnotation,
  fetchRecentAnnotations,
  recentAnnotationsQueryKey,
  type Annotation,
} from '@/api/annotations';
import {
  booksQueryKey,
  fetchBooks,
  type Book,
} from '@/api/books';
import { Cover } from '@/components/Cover';
import { Icon } from '@/components/Icon';
import { TopBar } from '@/components/TopBar';
import { Button } from '@/components/ui/button';

export const Route = createFileRoute('/_app/notebook')({
  component: Notebook,
});

function Notebook() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const openBook = (id: string) => void navigate({ to: '/book/$id', params: { id } });

  // One request for annotations + one request for the books list so each
  // row can render its cover + title. We cap the books list at whatever
  // the server returns (currently 500) — more than enough for a single
  // user's annotated collection.
  const [annotations, books] = useQueries({
    queries: [
      { queryKey: recentAnnotationsQueryKey, queryFn: () => fetchRecentAnnotations() },
      { queryKey: booksQueryKey(), queryFn: () => fetchBooks() },
    ],
  });

  const bookIndex = useMemo(() => {
    const map = new Map<string, Book>();
    for (const b of books.data?.books ?? []) map.set(b.id, b);
    return map;
  }, [books.data]);

  const deleteMut = useMutation({
    mutationFn: (a: Annotation) => deleteAnnotation(a.id),
    onSuccess: (_res, a) => {
      queryClient.invalidateQueries({ queryKey: recentAnnotationsQueryKey });
      // Keep the per-book cache in sync in case the user's looking at
      // that book detail in another tab.
      queryClient.invalidateQueries({ queryKey: bookAnnotationsQueryKey(a.bookId) });
    },
  });

  const rows = annotations.data ?? [];

  return (
    <div className="fade-in">
      <TopBar title="Notebook" subtitle="Every highlight and marginalia, across every book." />
      <div style={{ padding: '28px 32px 80px' }}>
        {annotations.isLoading && (
          <div className="t-small" style={{ fontStyle: 'italic' }}>Loading notebook…</div>
        )}
        {annotations.isError && (
          <div className="t-small" style={{ color: 'var(--color-accent-ink)' }}>
            Failed to load annotations.
          </div>
        )}
        {!annotations.isLoading && rows.length === 0 && (
          <div
            style={{
              padding: '40px 24px',
              textAlign: 'center',
              border: '1px dashed var(--color-rule)',
              borderRadius: 2,
              background: 'var(--color-paper-0)',
            }}
          >
            <div className="t-h3" style={{ marginBottom: 6 }}>No notes yet.</div>
            <div className="t-small" style={{ fontStyle: 'italic' }}>
              Highlights and margin notes you take while reading will appear here.
            </div>
          </div>
        )}
        {rows.map((a) => {
          const book = bookIndex.get(a.bookId);
          const kind = annotationKind(a);
          return (
            <div
              key={a.id}
              style={{
                display: 'grid',
                gridTemplateColumns: '60px 1fr',
                gap: 20,
                padding: '18px 0',
                borderBottom: '1px solid var(--color-rule-soft)',
              }}
            >
              <div
                onClick={() => book && openBook(book.id)}
                style={{ cursor: book ? 'pointer' : 'default' }}
              >
                {book ? (
                  <Cover book={book} size="xs" style={{ width: 60, height: 90 }} />
                ) : (
                  <div
                    style={{
                      width: 60,
                      height: 90,
                      background: 'var(--color-paper-3)',
                      border: '1px solid var(--color-rule)',
                    }}
                  />
                )}
              </div>
              <div>
                <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 6, gap: 12 }}>
                  <span style={{ fontSize: 13, fontWeight: 500, fontStyle: 'italic' }}>
                    {book?.title ?? 'Book no longer in library'}
                  </span>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <span className="t-micro">
                      {kind === 'highlight' ? 'highlight' : kind === 'highlight+note' ? 'highlight · note' : 'note'}
                      {a.locator ? ` · ${locatorLabel(a.locator)}` : ''} ·{' '}
                      {new Date(a.createdAt).toLocaleDateString()}
                    </span>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-xs"
                      onClick={() => deleteMut.mutate(a)}
                      disabled={deleteMut.isPending}
                      aria-label="Delete"
                      title="Delete"
                    >
                      <Icon name="close" size={11} />
                    </Button>
                  </div>
                </div>
                {a.selectedText && (
                  <p
                    style={{
                      fontSize: 15,
                      lineHeight: 1.6,
                      color: 'var(--color-ink-1)',
                      fontStyle: 'italic',
                      background: 'oklch(0.94 0.04 85)',
                      padding: '6px 10px',
                      marginBottom: a.note ? 10 : 0,
                    }}
                  >
                    {a.selectedText}
                  </p>
                )}
                {a.note && (
                  <p
                    style={{
                      fontSize: 14.5,
                      lineHeight: 1.6,
                      color: 'var(--color-ink-1)',
                    }}
                  >
                    {a.note}
                  </p>
                )}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

// locatorLabel renders the stored locator in a reader-friendly form.
// EPUB CFI strings are opaque → show them as "EPUB". PDF `page:N`
// tokens render as "p.N".
function locatorLabel(locator: string): string {
  if (locator.startsWith('page:')) return `p.${locator.slice(5)}`;
  if (locator.startsWith('epubcfi')) return 'EPUB';
  return locator;
}
