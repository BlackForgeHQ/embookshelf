import { useState, type ReactNode } from 'react';
import { createFileRoute, useNavigate } from '@tanstack/react-router';

import { Cover } from '@/components/Cover';
import { Icon } from '@/components/Icon';
import { findBook } from '@/data/mock';

export const Route = createFileRoute('/_app/book/$id/edit')({
  component: MetadataEditor,
});

function MetadataEditor() {
  const { id } = Route.useParams();
  const navigate = useNavigate();
  const book = findBook(id);

  const initial = {
    title: book?.title ?? '',
    author: book?.author ?? '',
    year: book?.year != null ? String(book.year) : '',
    description: book?.description ?? '',
    tags: (book?.tags ?? []).join(', '),
    series: book?.series ?? '',
    seriesNum: book?.seriesNum != null ? String(book.seriesNum) : '',
    isbn: '978-1-' + (book?.id.charCodeAt(1) ?? 0) * 7 + 24000,
    publisher: 'Starling Press',
    language: 'English',
  };

  const [form, setForm] = useState(initial);
  const set = <K extends keyof typeof form>(k: K, v: string) =>
    setForm((f) => ({ ...f, [k]: v }));

  if (!book) {
    return (
      <div style={{ padding: 40 }}>
        <p className="t-small">Book not found.</p>
      </div>
    );
  }

  return (
    <div className="fade-in">
      <div
        style={{
          padding: '16px 32px',
          borderBottom: '1px solid var(--color-rule-soft)',
          display: 'flex',
          alignItems: 'center',
          gap: 12,
        }}
      >
        <button
          className="btn ghost small"
          onClick={() => void navigate({ to: '/book/$id', params: { id } })}
        >
          <Icon name="arrow-left" size={14} /> Back to book
        </button>
        <div style={{ flex: 1 }} />
        <button className="btn small">
          <Icon name="refresh" size={13} /> Refetch from sources
        </button>
        <button className="btn" onClick={() => void navigate({ to: '/book/$id', params: { id } })}>
          Cancel
        </button>
        <button className="btn primary">Save changes</button>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 320px', gap: 40, padding: '32px 40px' }}>
        <div style={{ maxWidth: 720 }}>
          <div className="t-label" style={{ marginBottom: 6 }}>Editing metadata</div>
          <h1 className="t-h1" style={{ marginBottom: 28 }}>{book.title}</h1>

          <Section title="Core">
            <Row label="Title">
              <input className="input" value={form.title} onChange={(e) => set('title', e.target.value)} />
            </Row>
            <Row label="Author">
              <input className="input" value={form.author} onChange={(e) => set('author', e.target.value)} />
            </Row>
            <Row label="Description">
              <textarea
                className="input"
                rows={5}
                value={form.description}
                onChange={(e) => set('description', e.target.value)}
                style={{ fontFamily: 'var(--font-serif)', lineHeight: 1.5, resize: 'vertical' }}
              />
            </Row>
          </Section>

          <Section title="Publication">
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 14 }}>
              <Row label="Year">
                <input className="input" value={form.year} onChange={(e) => set('year', e.target.value)} />
              </Row>
              <Row label="Publisher">
                <input
                  className="input"
                  value={form.publisher}
                  onChange={(e) => set('publisher', e.target.value)}
                />
              </Row>
              <Row label="Language">
                <input
                  className="input"
                  value={form.language}
                  onChange={(e) => set('language', e.target.value)}
                />
              </Row>
            </div>
            <Row label="ISBN">
              <input
                className="input mono"
                value={form.isbn}
                onChange={(e) => set('isbn', e.target.value)}
              />
            </Row>
          </Section>

          <Section title="Series">
            <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 14 }}>
              <Row label="Series name">
                <input
                  className="input"
                  value={form.series}
                  onChange={(e) => set('series', e.target.value)}
                  placeholder="—"
                />
              </Row>
              <Row label="Book #">
                <input
                  className="input"
                  value={form.seriesNum}
                  onChange={(e) => set('seriesNum', e.target.value)}
                  placeholder="—"
                />
              </Row>
            </div>
          </Section>

          <Section title="Categories & tags">
            <Row label="Tags">
              <input className="input" value={form.tags} onChange={(e) => set('tags', e.target.value)} />
            </Row>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 8 }}>
              {['Fiction', 'Literary', 'Essays', 'Poetry', 'Nonfiction', 'History', 'Philosophy', 'Memoir'].map(
                (t) => (
                  <button
                    key={t}
                    type="button"
                    className="chip"
                    style={{ cursor: 'pointer' }}
                    onClick={() => set('tags', form.tags ? `${form.tags}, ${t}` : t)}
                  >
                    + {t}
                  </button>
                ),
              )}
            </div>
          </Section>
        </div>

        {/* Right — cover & sources */}
        <div>
          <div className="t-label" style={{ marginBottom: 10 }}>Cover</div>
          <Cover book={book} size="hero" style={{ width: 240, height: 360 }} />
          <div style={{ display: 'flex', gap: 6, marginTop: 12 }}>
            <button className="btn small" style={{ flex: 1, justifyContent: 'center' }}>
              <Icon name="upload" size={12} /> Upload
            </button>
            <button className="btn small" style={{ flex: 1, justifyContent: 'center' }}>
              <Icon name="search" size={12} /> Search
            </button>
          </div>

          <div className="t-label" style={{ marginTop: 28, marginBottom: 10 }}>
            Metadata sources
          </div>
          {[
            { name: 'Google Books', fields: 8, conf: 'high' },
            { name: 'Open Library', fields: 6, conf: 'med' },
            { name: 'Amazon', fields: 5, conf: 'med' },
            { name: 'Embedded (EPUB)', fields: 4, conf: 'high' },
          ].map((s) => (
            <div
              key={s.name}
              style={{ padding: '10px 0', borderBottom: '1px dashed var(--color-rule-soft)' }}
            >
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <span style={{ fontSize: 13 }}>{s.name}</span>
                <span className="mono" style={{ fontSize: 10, color: 'var(--color-ink-3)' }}>
                  {s.fields} fields · {s.conf}
                </span>
              </div>
            </div>
          ))}

          <div
            style={{
              marginTop: 24,
              padding: 12,
              background: 'var(--color-paper-2)',
              border: '1px solid var(--color-rule-soft)',
              borderRadius: 2,
            }}
          >
            <div className="t-label" style={{ marginBottom: 6 }}>Storage</div>
            <div
              className="mono"
              style={{ fontSize: 11, color: 'var(--color-ink-2)', lineHeight: 1.5, wordBreak: 'break-all' }}
            >
              /books/main/halden_mira/the_cartographers_of_dusk.epub
            </div>
            <div className="t-small" style={{ fontSize: 11.5, marginTop: 6 }}>
              Mode: LOCAL · Metadata will be written back to the file.
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div
      style={{
        marginBottom: 28,
        paddingBottom: 24,
        borderBottom: '1px solid var(--color-rule-soft)',
      }}
    >
      <div className="t-label" style={{ marginBottom: 14 }}>{title}</div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>{children}</div>
    </div>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <div
        style={{
          fontSize: 12,
          color: 'var(--color-ink-3)',
          marginBottom: 4,
          fontFamily: 'var(--font-mono)',
          letterSpacing: '0.04em',
        }}
      >
        {label}
      </div>
      {children}
    </div>
  );
}
