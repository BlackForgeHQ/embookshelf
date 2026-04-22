import { useEffect, useState, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute, useNavigate } from '@tanstack/react-router';

import {
  bookQueryKey,
  fetchBook,
  patchBook,
  type BookDetail,
  type BookPatch,
} from '@/api/books';
import type { ApiError } from '@/api/client';
import {
  applyCoverFromUrl,
  enrichQueryKey,
  fetchEnrichment,
  type EnrichMatch,
} from '@/api/enrich';
import { Cover } from '@/components/Cover';
import { Icon } from '@/components/Icon';

export const Route = createFileRoute('/_app/book/$id_/edit')({
  component: MetadataEditor,
});

// FormState mirrors the editor inputs as strings (native form shape);
// numeric fields get parsed back to numbers on save.
type FormState = {
  title: string;
  author: string;
  description: string;
  year: string;
  publisher: string;
  isbn: string;
  series: string;
  seriesNum: string;
  tags: string;
};

function blankForm(): FormState {
  return {
    title: '',
    author: '',
    description: '',
    year: '',
    publisher: '',
    isbn: '',
    series: '',
    seriesNum: '',
    tags: '',
  };
}

function bookToForm(b: BookDetail): FormState {
  return {
    title: b.title ?? '',
    author: b.author ?? '',
    description: b.description ?? '',
    year: b.year ? String(b.year) : '',
    publisher: b.publisher ?? '',
    isbn: b.isbn ?? '',
    series: b.series ?? '',
    seriesNum: b.seriesNum ? String(b.seriesNum) : '',
    tags: (b.tags ?? []).join(', '),
  };
}

function formToPatch(form: FormState): BookPatch {
  const patch: BookPatch = {
    title: form.title.trim(),
    author: form.author.trim(),
    description: form.description,
    publisher: form.publisher.trim(),
    isbn: form.isbn.trim(),
    series: form.series.trim(),
  };
  const year = Number.parseInt(form.year, 10);
  patch.year = Number.isFinite(year) ? year : 0;
  const seriesNum = Number.parseInt(form.seriesNum, 10);
  patch.seriesNum = Number.isFinite(seriesNum) ? seriesNum : 0;
  patch.tags = form.tags
    .split(',')
    .map((t) => t.trim())
    .filter(Boolean);
  return patch;
}

function MetadataEditor() {
  const { id } = Route.useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const book = useQuery({
    queryKey: bookQueryKey(id),
    queryFn: () => fetchBook(id),
  });

  const [form, setForm] = useState<FormState>(blankForm());
  // Sync form state once when the book loads. Subsequent refetches don't
  // overwrite in-flight edits.
  useEffect(() => {
    if (book.data) {
      setForm((prev) =>
        // Only initialize if we haven't customized yet — an empty title
        // is the sentinel that the form is fresh.
        prev.title === '' && prev.author === '' ? bookToForm(book.data) : prev,
      );
    }
  }, [book.data]);

  const set = <K extends keyof FormState>(k: K, v: string) =>
    setForm((f) => ({ ...f, [k]: v }));

  const saveMut = useMutation({
    mutationFn: () => patchBook(id, formToPatch(form)),
    onSuccess: (updated) => {
      queryClient.setQueryData(bookQueryKey(id), updated);
      // Library lists might show the patched title/author, so nuke the
      // cached lists — next visit refetches.
      queryClient.invalidateQueries({ queryKey: ['books'] });
      void navigate({ to: '/book/$id', params: { id } });
    },
  });

  if (book.isLoading) {
    return (
      <div style={{ padding: 40 }}>
        <p className="t-small">Loading…</p>
      </div>
    );
  }
  if (book.isError || !book.data) {
    return (
      <div style={{ padding: 40 }}>
        <p className="t-small">Book not found.</p>
      </div>
    );
  }
  const b = book.data;
  const error = saveMut.error as unknown as ApiError | null;

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
        <button className="btn small" disabled title="Metadata enrichment lands in a later slice">
          <Icon name="refresh" size={13} /> Refetch from sources
        </button>
        <button
          className="btn"
          onClick={() => void navigate({ to: '/book/$id', params: { id } })}
          disabled={saveMut.isPending}
        >
          Cancel
        </button>
        <button
          className="btn primary"
          onClick={() => saveMut.mutate()}
          disabled={saveMut.isPending}
        >
          {saveMut.isPending ? 'Saving…' : 'Save changes'}
        </button>
      </div>

      {error && (
        <div
          style={{
            margin: '16px 40px 0',
            padding: '10px 14px',
            border: '1px solid var(--color-accent-soft)',
            background: 'var(--color-accent-soft)',
            color: 'var(--color-accent-ink)',
            borderRadius: 2,
            fontSize: 13,
          }}
        >
          {error.message}
        </div>
      )}

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 320px', gap: 40, padding: '32px 40px' }}>
        <div style={{ maxWidth: 720 }}>
          <div className="t-label" style={{ marginBottom: 6 }}>Editing metadata</div>
          <h1 className="t-h1" style={{ marginBottom: 28 }}>{b.title}</h1>

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
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
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

        <EnrichmentPanel
          book={b}
          searchTitle={form.title}
          searchAuthor={form.author}
          onApplyFields={(m) => {
            setForm((prev) => ({
              ...prev,
              title: m.title || prev.title,
              author: m.authors.join(', ') || prev.author,
              description: m.description || prev.description,
              year: m.year ? String(m.year) : prev.year,
              publisher: m.publisher || prev.publisher,
              isbn: m.isbn || prev.isbn,
              series: m.series || prev.series,
              tags: [...new Set([
                ...prev.tags.split(',').map((t) => t.trim()).filter(Boolean),
                ...(m.categories ?? []),
              ])].join(', '),
            }));
          }}
        />
      </div>
    </div>
  );
}

function EnrichmentPanel({
  book,
  searchTitle,
  searchAuthor,
  onApplyFields,
}: {
  book: BookDetail;
  searchTitle: string;
  searchAuthor: string;
  onApplyFields: (m: EnrichMatch) => void;
}) {
  const queryClient = useQueryClient();
  const [opened, setOpened] = useState(false);

  const q = { title: searchTitle, author: searchAuthor };
  const enrich = useQuery({
    queryKey: enrichQueryKey(book.id, q),
    queryFn: () => fetchEnrichment(book.id, q),
    // Don't fire until the user opens the panel — a default load of the
    // editor shouldn't trigger outbound HTTP to Google Books + Open
    // Library. ensures manual trigger gate.
    enabled: opened,
    staleTime: 60_000,
  });

  const coverMut = useMutation({
    mutationFn: (url: string) => applyCoverFromUrl(book.id, url),
    onSuccess: () => {
      // Bust book detail + lists so the UI reflects the new has_cover.
      queryClient.invalidateQueries({ queryKey: bookQueryKey(book.id) });
      queryClient.invalidateQueries({ queryKey: ['books'] });
    },
  });

  const error = (enrich.error ?? coverMut.error) as unknown as ApiError | null;

  return (
    <div>
      <div className="t-label" style={{ marginBottom: 10 }}>Cover</div>
      <Cover book={book} size="hero" style={{ width: 240, height: 360 }} />
      {coverMut.isPending && (
        <div className="t-small" style={{ marginTop: 8, fontStyle: 'italic' }}>
          Fetching cover…
        </div>
      )}
      {coverMut.isSuccess && (
        <div className="t-small" style={{ marginTop: 8, color: 'var(--color-accent-ink)' }}>
          Cover updated. Save to keep the metadata changes.
        </div>
      )}

      <div className="t-label" style={{ marginTop: 28, marginBottom: 10 }}>Metadata sources</div>

      {!opened ? (
        <button
          type="button"
          className="btn small"
          style={{ width: '100%', justifyContent: 'center' }}
          onClick={() => setOpened(true)}
        >
          <Icon name="search" size={12} /> Find metadata online
        </button>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <button
            type="button"
            className="btn ghost small"
            disabled={enrich.isFetching}
            onClick={() =>
              queryClient.invalidateQueries({
                queryKey: enrichQueryKey(book.id, q),
              })
            }
          >
            <Icon name="refresh" size={12} />{' '}
            {enrich.isFetching ? 'Searching…' : 'Re-search with current fields'}
          </button>

          {error && (
            <div
              className="flash error"
              style={{
                padding: '8px 12px',
                border: '1px solid var(--color-accent-soft)',
                background: 'var(--color-accent-soft)',
                color: 'var(--color-accent-ink)',
                borderRadius: 2,
                fontSize: 12,
              }}
            >
              {error.message}
            </div>
          )}

          {enrich.data && enrich.data.matches.length === 0 && (
            <div className="t-small" style={{ fontStyle: 'italic' }}>
              No matches from Google Books or Open Library.
            </div>
          )}

          {(enrich.data?.matches ?? []).slice(0, 10).map((m) => (
            <MatchCard
              key={`${m.source}:${m.sourceId}`}
              match={m}
              applyFields={() => onApplyFields(m)}
              applyCover={() => coverMut.mutate(m.coverUrl ?? '')}
              coverBusy={coverMut.isPending}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function MatchCard({
  match,
  applyFields,
  applyCover,
  coverBusy,
}: {
  match: EnrichMatch;
  applyFields: () => void;
  applyCover: () => void;
  coverBusy: boolean;
}) {
  return (
    <div
      style={{
        display: 'flex',
        gap: 10,
        padding: 10,
        border: '1px solid var(--color-rule-soft)',
        background: 'var(--color-paper-0)',
        borderRadius: 2,
      }}
    >
      {match.coverUrl ? (
        <img
          src={match.coverUrl}
          alt=""
          width={52}
          height={78}
          style={{ width: 52, height: 78, objectFit: 'cover', flexShrink: 0, background: 'var(--color-paper-2)' }}
        />
      ) : (
        <div
          style={{
            width: 52,
            height: 78,
            background:
              'repeating-linear-gradient(135deg, var(--color-paper-3) 0 6px, var(--color-paper-2) 6px 12px)',
            flexShrink: 0,
          }}
        />
      )}
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 13, fontWeight: 500, textWrap: 'balance' }}>{match.title}</div>
        <div className="t-small" style={{ fontSize: 11.5, fontStyle: 'italic' }}>
          {match.authors.join(', ')}
          {match.year ? ` · ${match.year}` : ''}
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 4 }}>
          <span className="t-micro" style={{ fontSize: 9.5 }}>
            {match.source.replace('_', ' ')}
          </span>
          <span className="mono" style={{ fontSize: 10, color: 'var(--color-ink-3)' }}>
            conf {match.confidence}
          </span>
        </div>
        <div style={{ display: 'flex', gap: 6, marginTop: 8 }}>
          <button type="button" className="btn small" onClick={applyFields}>
            Use fields
          </button>
          <button
            type="button"
            className="btn small"
            onClick={applyCover}
            disabled={!match.coverUrl || coverBusy}
          >
            Use cover
          </button>
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
