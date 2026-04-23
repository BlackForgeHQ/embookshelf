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
  formatProviderList,
  type EnrichMatch,
} from '@/api/enrich';
import { Cover } from '@/components/Cover';
import { Icon } from '@/components/Icon';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';

export const Route = createFileRoute('/_app/book/$id_/edit')({
  component: MetadataEditor,
});

// FormState mirrors the editor inputs as strings (native form shape);
// numeric fields get parsed back to numbers on save. publicReviews is
// tri-state — '' means "No Value" (null), 'yes' / 'no' map to true/false.
type FormState = {
  title: string;
  subtitle: string;
  author: string;
  description: string;
  year: string;
  publishDate: string;
  language: string;
  publisher: string;
  isbn13: string;
  isbn10: string;
  series: string;
  seriesNum: string;
  seriesTotal: string;
  genres: string;
  moods: string;
  tags: string;
  ageRating: string;
  contentRating: string;
  pages: string;
  publicReviews: '' | 'yes' | 'no';
};

function blankForm(): FormState {
  return {
    title: '',
    subtitle: '',
    author: '',
    description: '',
    year: '',
    publishDate: '',
    language: '',
    publisher: '',
    isbn13: '',
    isbn10: '',
    series: '',
    seriesNum: '',
    seriesTotal: '',
    genres: '',
    moods: '',
    tags: '',
    ageRating: '',
    contentRating: '',
    pages: '',
    publicReviews: '',
  };
}

function bookToForm(b: BookDetail): FormState {
  const pr = b.publicReviews;
  return {
    title: b.title ?? '',
    subtitle: b.subtitle ?? '',
    author: b.author ?? '',
    description: b.description ?? '',
    year: b.year ? String(b.year) : '',
    publishDate: b.publishDate ?? '',
    language: b.language ?? '',
    publisher: b.publisher ?? '',
    isbn13: b.isbn ?? '',
    isbn10: b.isbn10 ?? '',
    series: b.series ?? '',
    seriesNum: b.seriesNum ? String(b.seriesNum) : '',
    seriesTotal: b.seriesTotal ? String(b.seriesTotal) : '',
    genres: (b.genres ?? []).join(', '),
    moods: (b.moods ?? []).join(', '),
    tags: (b.tags ?? []).join(', '),
    ageRating: b.ageRating ?? '',
    contentRating: b.contentRating ?? '',
    pages: b.pages ? String(b.pages) : '',
    publicReviews: pr === true ? 'yes' : pr === false ? 'no' : '',
  };
}

function splitCsv(raw: string): string[] {
  return raw
    .split(',')
    .map((t) => t.trim())
    .filter(Boolean);
}

function formToPatch(form: FormState): BookPatch {
  const patch: BookPatch = {
    title: form.title.trim(),
    subtitle: form.subtitle.trim(),
    author: form.author.trim(),
    description: form.description,
    language: form.language.trim(),
    publisher: form.publisher.trim(),
    isbn: form.isbn13.trim(),
    isbn10: form.isbn10.trim(),
    series: form.series.trim(),
    ageRating: form.ageRating.trim(),
    contentRating: form.contentRating.trim(),
    publishDate: form.publishDate.trim(),
  };
  const year = Number.parseInt(form.year, 10);
  patch.year = Number.isFinite(year) ? year : 0;
  const seriesNum = Number.parseInt(form.seriesNum, 10);
  patch.seriesNum = Number.isFinite(seriesNum) ? seriesNum : 0;
  const seriesTotal = Number.parseInt(form.seriesTotal, 10);
  patch.seriesTotal = Number.isFinite(seriesTotal) ? seriesTotal : 0;
  const pages = Number.parseInt(form.pages, 10);
  patch.pages = Number.isFinite(pages) ? pages : 0;
  patch.genres = splitCsv(form.genres);
  patch.moods = splitCsv(form.moods);
  patch.tags = splitCsv(form.tags);
  if (form.publicReviews === 'yes') {
    patch.publicReviews = true;
  } else if (form.publicReviews === 'no') {
    patch.publicReviews = false;
  } else {
    patch.publicReviewsClear = true;
  }
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
        <Button
          variant="ghost"
          size="sm"
          onClick={() => void navigate({ to: '/book/$id', params: { id } })}
        >
          <Icon name="arrow-left" size={14} /> Back to book
        </Button>
        <div style={{ flex: 1 }} />
        <Button variant="outline" size="sm" disabled title="Metadata enrichment lands in a later slice">
          <Icon name="refresh" size={13} /> Refetch from sources
        </Button>
        <Button
          variant="outline"
          onClick={() => void navigate({ to: '/book/$id', params: { id } })}
          disabled={saveMut.isPending}
        >
          Cancel
        </Button>
        <Button
          onClick={() => saveMut.mutate()}
          disabled={saveMut.isPending}
        >
          {saveMut.isPending ? 'Saving…' : 'Save changes'}
        </Button>
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
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
              <Row label="Title">
                <Input value={form.title} onChange={(e) => set('title', e.target.value)} />
              </Row>
              <Row label="Subtitle">
                <Input
                  value={form.subtitle}
                  onChange={(e) => set('subtitle', e.target.value)}
                  placeholder="—"
                />
              </Row>
            </div>
            <Row label="Authors">
              <Input value={form.author} onChange={(e) => set('author', e.target.value)} />
            </Row>
            <Row label="Description">
              <Textarea
                rows={5}
                value={form.description}
                onChange={(e) => set('description', e.target.value)}
                className="resize-y min-h-[140px]"
              />
            </Row>
          </Section>

          <Section title="Publication">
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 14 }}>
              <Row label="Publisher">
                <Input
                  value={form.publisher}
                  onChange={(e) => set('publisher', e.target.value)}
                />
              </Row>
              <Row label="Publish date">
                <Input
                  type="date"
                  value={form.publishDate}
                  onChange={(e) => set('publishDate', e.target.value)}
                />
              </Row>
              <Row label="Year">
                <Input value={form.year} onChange={(e) => set('year', e.target.value)} />
              </Row>
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 14 }}>
              <Row label="Language">
                <Input
                  value={form.language}
                  onChange={(e) => set('language', e.target.value)}
                  placeholder="en"
                  className="mono"
                />
              </Row>
              <Row label="Pages">
                <Input
                  inputMode="numeric"
                  value={form.pages}
                  onChange={(e) => set('pages', e.target.value)}
                  placeholder="—"
                />
              </Row>
              <div />
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
              <Row label="ISBN 13">
                <Input
                  className="mono"
                  value={form.isbn13}
                  onChange={(e) => set('isbn13', e.target.value)}
                />
              </Row>
              <Row label="ISBN 10">
                <Input
                  className="mono"
                  value={form.isbn10}
                  onChange={(e) => set('isbn10', e.target.value)}
                />
              </Row>
            </div>
          </Section>

          <Section title="Series">
            <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr 1fr', gap: 14 }}>
              <Row label="Series name">
                <Input
                  value={form.series}
                  onChange={(e) => set('series', e.target.value)}
                  placeholder="—"
                />
              </Row>
              <Row label="Book #">
                <Input
                  value={form.seriesNum}
                  onChange={(e) => set('seriesNum', e.target.value)}
                  placeholder="—"
                />
              </Row>
              <Row label="Total">
                <Input
                  value={form.seriesTotal}
                  onChange={(e) => set('seriesTotal', e.target.value)}
                  placeholder="—"
                />
              </Row>
            </div>
          </Section>

          <Section title="Categories & tags">
            <Row label="Genres">
              <Input
                value={form.genres}
                onChange={(e) => set('genres', e.target.value)}
                placeholder="Fiction, Science"
              />
            </Row>
            <Row label="Moods">
              <Input
                value={form.moods}
                onChange={(e) => set('moods', e.target.value)}
                placeholder="Hopeful, Reflective"
              />
            </Row>
            <Row label="Tags">
              <Input value={form.tags} onChange={(e) => set('tags', e.target.value)} />
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

          <Section title="Ratings & reviews">
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 14 }}>
              <Row label="Age rating">
                <select
                  value={form.ageRating}
                  onChange={(e) => set('ageRating', e.target.value)}
                  className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                >
                  <option value="">—</option>
                  <option value="All ages">All ages</option>
                  <option value="8+">8+</option>
                  <option value="12+">12+</option>
                  <option value="16+">16+</option>
                  <option value="18+">18+</option>
                </select>
              </Row>
              <Row label="Content rating">
                <select
                  value={form.contentRating}
                  onChange={(e) => set('contentRating', e.target.value)}
                  className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                >
                  <option value="">—</option>
                  <option value="G">G</option>
                  <option value="PG">PG</option>
                  <option value="PG-13">PG-13</option>
                  <option value="R">R</option>
                  <option value="NC-17">NC-17</option>
                </select>
              </Row>
              <Row label="Public reviews">
                <select
                  value={form.publicReviews}
                  onChange={(e) =>
                    setForm((f) => ({
                      ...f,
                      publicReviews: e.target.value as FormState['publicReviews'],
                    }))
                  }
                  className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
                >
                  <option value="">No value</option>
                  <option value="yes">Allowed</option>
                  <option value="no">Blocked</option>
                </select>
              </Row>
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
              isbn13: m.isbn || prev.isbn13,
              series: m.series || prev.series,
              genres: [
                ...new Set([
                  ...splitCsv(prev.genres),
                  ...(m.categories ?? []),
                ]),
              ].join(', '),
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
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="w-full"
          onClick={() => setOpened(true)}
        >
          <Icon name="search" size={12} /> Find metadata online
        </Button>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={enrich.isFetching}
            onClick={() =>
              queryClient.invalidateQueries({
                queryKey: enrichQueryKey(book.id, q),
              })
            }
          >
            <Icon name="refresh" size={12} />{' '}
            {enrich.isFetching ? 'Searching…' : 'Re-search with current fields'}
          </Button>

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
              {enrich.data.providers.length === 0
                ? 'No metadata providers are enabled. An admin can turn them on in Settings → Metadata providers.'
                : `No matches from ${formatProviderList(enrich.data.providers)}.`}
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
          <Button type="button" variant="outline" size="sm" onClick={applyFields}>
            Use fields
          </Button>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={applyCover}
            disabled={!match.coverUrl || coverBusy}
          >
            Use cover
          </Button>
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
