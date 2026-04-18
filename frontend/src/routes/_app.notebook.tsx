import { createFileRoute, useNavigate } from '@tanstack/react-router';

import { Cover } from '@/components/Cover';
import { TopBar } from '@/components/TopBar';
import { findBook, NOTES } from '@/data/mock';

export const Route = createFileRoute('/_app/notebook')({
  component: Notebook,
});

function Notebook() {
  const navigate = useNavigate();
  const openBook = (id: string) => void navigate({ to: '/book/$id', params: { id } });

  return (
    <div className="fade-in">
      <TopBar title="Notebook" subtitle="Every highlight and marginalia, across every book." />
      <div style={{ padding: '28px 32px 80px', maxWidth: 820 }}>
        {NOTES.map((n) => {
          const book = findBook(n.bookId);
          if (!book) return null;
          return (
            <div
              key={n.id}
              style={{
                display: 'grid',
                gridTemplateColumns: '60px 1fr',
                gap: 20,
                padding: '18px 0',
                borderBottom: '1px solid var(--color-rule-soft)',
              }}
            >
              <div onClick={() => openBook(book.id)} style={{ cursor: 'pointer' }}>
                <Cover book={book} size="xs" style={{ width: 60, height: 90 }} />
              </div>
              <div>
                <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 6 }}>
                  <span style={{ fontSize: 13, fontWeight: 500, fontStyle: 'italic' }}>{book.title}</span>
                  <span className="t-micro">
                    p.{n.page} · {n.date}
                  </span>
                </div>
                <p
                  style={{
                    fontSize: 15,
                    lineHeight: 1.6,
                    color: 'var(--color-ink-1)',
                    fontStyle: n.highlight ? 'italic' : 'normal',
                    background: n.highlight ? 'oklch(0.94 0.04 85)' : 'transparent',
                    padding: n.highlight ? '6px 10px' : 0,
                  }}
                >
                  {n.text}
                </p>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
