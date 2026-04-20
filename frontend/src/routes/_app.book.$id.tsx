import { useState, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute, useNavigate } from '@tanstack/react-router';

import {
  annotationKind,
  bookAnnotationsQueryKey,
  createAnnotation,
  deleteAnnotation,
  fetchBookAnnotations,
  recentAnnotationsQueryKey,
  type Annotation,
} from '@/api/annotations';
import { fetchMe, meQueryKey } from '@/api/auth';
import {
  addBookToShelf,
  bookQueryKey,
  booksQueryKey,
  deleteBook,
  fetchBook,
  fetchShelves,
  librariesQueryKey,
  removeBookFromShelf,
  shelvesQueryKey,
  type BookDetail as BookDetailPayload,
} from '@/api/books';
import {
  DEVICE_KIND_LABELS,
  devicesQueryKey,
  fetchDevices,
  sendBookToDevice,
  type Device,
} from '@/api/devices';
import { Cover, StarRating } from '@/components/Cover';
import { Icon } from '@/components/Icon';
import type { ApiError } from '@/api/client';

type Tab = 'overview' | 'notes' | 'annotations' | 'versions' | 'activity';

export const Route = createFileRoute('/_app/book/$id')({
  component: BookDetail,
});

function BookDetail() {
  const { id } = Route.useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [tab, setTab] = useState<Tab>('overview');

  const book = useQuery({
    queryKey: bookQueryKey(id),
    queryFn: () => fetchBook(id),
  });
  const me = useQuery({ queryKey: meQueryKey, queryFn: fetchMe, staleTime: 60_000 });
  const isAdmin = me.data?.role === 'admin';

  const deleteMut = useMutation({
    mutationFn: () => deleteBook(id),
    onSuccess: () => {
      queryClient.removeQueries({ queryKey: bookQueryKey(id) });
      queryClient.invalidateQueries({ queryKey: booksQueryKey() });
      queryClient.invalidateQueries({ queryKey: shelvesQueryKey });
      queryClient.invalidateQueries({ queryKey: librariesQueryKey });
      void navigate({ to: '/library' });
    },
  });
  const deleteError = deleteMut.error as unknown as ApiError | null;

  if (book.isLoading) {
    return (
      <div style={{ padding: 40 }}>
        <p className="t-small">Loading…</p>
      </div>
    );
  }
  if (book.isError) {
    const err = book.error as unknown as ApiError;
    return (
      <div style={{ padding: 40 }}>
        <p className="t-small" style={{ color: 'var(--color-accent-ink)' }}>
          {err?.status === 404 ? 'Book not found.' : 'Failed to load book.'}
        </p>
      </div>
    );
  }
  if (!book.data) return null;

  const b = book.data;
  const progress = b.progress ?? 0;

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
        <button className="btn ghost small" onClick={() => void navigate({ to: '/library' })}>
          <Icon name="arrow-left" size={14} /> Back to library
        </button>
        <div style={{ flex: 1 }} />
        <button
          className="btn small"
          onClick={() => void navigate({ to: '/book/$id/edit', params: { id } })}
        >
          <Icon name="edit" size={13} /> Edit metadata
        </button>
        <button className="btn small">
          <Icon name="download" size={13} /> Download
        </button>
        <SendToDeviceButton bookId={id} />
        <button className="btn ghost icon-only" aria-label="More">
          <Icon name="more" size={14} />
        </button>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '280px 1fr', gap: 48, padding: '40px 48px' }}>
        {/* Left — cover & actions */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
          <Cover book={b} size="hero" />
          <button
            className="btn primary"
            style={{ justifyContent: 'center', padding: '10px 14px', fontSize: 14 }}
            onClick={() => void navigate({ to: '/read/$id', params: { id } })}
          >
            <Icon name="book-open" size={14} /> {progress > 0 && progress < 1 ? 'Continue reading' : 'Open book'}
          </button>
          {progress > 0 && progress < 1 && (
            <div>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 6 }}>
                <span className="t-micro">Progress</span>
                <span className="mono" style={{ fontSize: 11 }}>
                  {Math.round(progress * 100)}%
                </span>
              </div>
              <div className="progress">
                <div style={{ width: `${progress * 100}%` }} />
              </div>
            </div>
          )}
          <ShelfCard book={b} />
        </div>

        {/* Right — info */}
        <div>
          <div className="t-micro" style={{ marginBottom: 8 }}>
            {b.format} · {b.year}
          </div>
          <h1 className="t-display" style={{ marginBottom: 6, textWrap: 'balance' }}>{b.title}</h1>
          <div style={{ fontSize: 17, color: 'var(--color-ink-2)', fontStyle: 'italic', marginBottom: 16 }}>
            by {b.author}
          </div>

          <div style={{ display: 'flex', alignItems: 'center', gap: 16, marginBottom: 28 }}>
            <StarRating rating={b.rating} size={15} />
            <span className="mono" style={{ fontSize: 12, color: 'var(--color-ink-2)' }}>
              {b.rating.toFixed(1)}
            </span>
            <span style={{ color: 'var(--color-rule)' }}>·</span>
            {(b.tags ?? []).map((t) => (
              <span key={t} className="chip">{t}</span>
            ))}
          </div>

          {b.description && (
            <p
              style={{
                fontSize: 16,
                lineHeight: 1.65,
                color: 'var(--color-ink-1)',
                marginBottom: 32,
                maxWidth: 640,
                textWrap: 'pretty',
              }}
            >
              {b.description}
            </p>
          )}

          <div style={{ borderBottom: '1px solid var(--color-rule-soft)', display: 'flex', gap: 0, marginBottom: 24 }}>
            {(['overview', 'notes', 'annotations', 'versions', 'activity'] as Tab[]).map((t) => (
              <button
                key={t}
                onClick={() => setTab(t)}
                style={{
                  background: 'none',
                  border: 'none',
                  cursor: 'pointer',
                  padding: '10px 16px 12px',
                  fontFamily: 'var(--font-serif)',
                  fontSize: 13.5,
                  color: tab === t ? 'var(--color-ink-1)' : 'var(--color-ink-3)',
                  borderBottom: tab === t ? '2px solid var(--color-accent)' : '2px solid transparent',
                  fontWeight: tab === t ? 500 : 400,
                  textTransform: 'capitalize',
                }}
              >
                {t}
              </button>
            ))}
          </div>

          {tab === 'overview' && (
            <div style={{ maxWidth: 640 }}>
              <Meta label="Title">{b.title}</Meta>
              <Meta label="Author">{b.author}</Meta>
              {b.series && (
                <Meta label="Series">
                  {b.series}
                  {b.seriesNum ? `, Book ${b.seriesNum}` : ''}
                </Meta>
              )}
              <Meta label="Published">{b.year}</Meta>
              <Meta label="Format">{b.format}</Meta>
              {b.publisher && <Meta label="Publisher">{b.publisher}</Meta>}
              <Meta label="Categories">{(b.tags ?? []).join(' · ') || '—'}</Meta>
              <Meta label="Added">{new Date(b.addedAt).toLocaleDateString()}</Meta>
              {b.isbn && (
                <Meta label="ISBN">
                  <span className="mono" style={{ fontSize: 11.5 }}>{b.isbn}</span>
                </Meta>
              )}
            </div>
          )}

          {tab === 'notes' && <NotesPanel bookId={id} />}

          {tab === 'annotations' && (
            <div className="t-small" style={{ fontStyle: 'italic' }}>
              No PDF annotations for this book.
            </div>
          )}

          {tab === 'versions' && (
            <div style={{ maxWidth: 640, display: 'flex', flexDirection: 'column', gap: 24 }}>
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 16,
                  padding: '10px 12px',
                  border: '1px solid var(--color-rule-soft)',
                  background: 'var(--color-paper-0)',
                }}
              >
                <Icon name="book" size={16} />
                <div style={{ flex: 1 }}>
                  <div style={{ fontSize: 13.5, fontWeight: 500 }}>
                    {b.title}.{b.format.toLowerCase()}
                  </div>
                  <div className="mono" style={{ fontSize: 11, color: 'var(--color-ink-3)' }}>
                    Primary · {b.format}
                  </div>
                </div>
                <button className="btn small">Replace</button>
              </div>

              {isAdmin && (
                <div
                  style={{
                    padding: 16,
                    border: '1px solid var(--color-accent-soft)',
                    background: 'var(--color-paper-0)',
                  }}
                >
                  <div className="t-label" style={{ marginBottom: 6 }}>Danger zone</div>
                  <p className="t-small" style={{ marginBottom: 10, maxWidth: 520 }}>
                    Permanently remove this book, its cover, its source file, and every
                    reader&apos;s progress, notes, and shelf placements for it. This cannot
                    be undone.
                  </p>
                  {deleteError && (
                    <div
                      style={{
                        padding: '8px 12px',
                        marginBottom: 10,
                        border: '1px solid var(--color-accent-soft)',
                        background: 'var(--color-accent-soft)',
                        color: 'var(--color-accent-ink)',
                        fontSize: 13,
                      }}
                    >
                      {deleteError.message}
                    </div>
                  )}
                  <button
                    type="button"
                    className="btn small"
                    style={{
                      color: 'var(--color-accent-ink)',
                      borderColor: 'var(--color-accent)',
                    }}
                    disabled={deleteMut.isPending}
                    onClick={() => {
                      if (
                        window.confirm(
                          `Delete “${b.title}”? This removes the file from disk and everyone’s notes for it.`,
                        )
                      ) {
                        deleteMut.mutate();
                      }
                    }}
                  >
                    <Icon name="close" size={12} />{' '}
                    {deleteMut.isPending ? 'Deleting…' : 'Delete book'}
                  </button>
                </div>
              )}
            </div>
          )}

          {tab === 'activity' && (
            <div className="t-small" style={{ fontStyle: 'italic' }}>
              Per-book activity timeline lands once reading sessions are tracked server-side.
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function Meta({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div
      style={{
        display: 'flex',
        gap: 14,
        padding: '7px 0',
        borderBottom: '1px dashed var(--color-rule-soft)',
      }}
    >
      <div className="t-label" style={{ width: 96, flexShrink: 0 }}>{label}</div>
      <div style={{ fontSize: 13.5, color: 'var(--color-ink-1)' }}>{children}</div>
    </div>
  );
}

// ShelfCard renders the book's current shelf memberships as chips and lets
// the user add/remove via a tiny picker. Optimistic on add/remove so the
// chip feedback lands immediately without waiting for the round trip.
function ShelfCard({ book }: { book: BookDetailPayload }) {
  const queryClient = useQueryClient();
  const shelves = useQuery({ queryKey: shelvesQueryKey, queryFn: fetchShelves });
  const [pickerOpen, setPickerOpen] = useState(false);

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: bookQueryKey(book.id) });
    queryClient.invalidateQueries({ queryKey: shelvesQueryKey });
    queryClient.invalidateQueries({ queryKey: booksQueryKey() });
  };

  const addMut = useMutation({
    mutationFn: (slug: string) => addBookToShelf(book.id, slug),
    onSuccess: () => {
      invalidate();
      setPickerOpen(false);
    },
  });
  const removeMut = useMutation({
    mutationFn: (slug: string) => removeBookFromShelf(book.id, slug),
    onSuccess: invalidate,
  });

  const currentSlugs = new Set(book.shelves);
  // Smart shelves are excluded — their membership is derived from the
  // rule, so a book is either matched or it isn't. The backend rejects
  // direct adds with 409, but hiding them in the picker avoids the
  // user having to discover that the hard way.
  const availableToAdd = (shelves.data ?? []).filter(
    (s) => !currentSlugs.has(s.slug) && !s.isSmart,
  );

  const shelfLabel = (slug: string): string =>
    shelves.data?.find((s) => s.slug === slug)?.name ?? slug;

  return (
    <div
      style={{
        border: '1px solid var(--color-rule-soft)',
        padding: 16,
        background: 'var(--color-paper-0)',
        borderRadius: 2,
      }}
    >
      <div className="t-label" style={{ marginBottom: 10 }}>Shelves</div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
        {book.shelves.length === 0 && !pickerOpen && (
          <span className="t-small" style={{ fontStyle: 'italic' }}>
            Not on any shelves yet.
          </span>
        )}
        {book.shelves.map((s) => (
          <button
            key={s}
            type="button"
            className="chip accent"
            onClick={() => removeMut.mutate(s)}
            disabled={removeMut.isPending}
            title="Remove from shelf"
            style={{ cursor: 'pointer' }}
          >
            {shelfLabel(s)}
            <Icon name="close" size={10} />
          </button>
        ))}
        {!pickerOpen && (
          <button
            type="button"
            className="chip"
            style={{ cursor: 'pointer' }}
            onClick={() => setPickerOpen(true)}
          >
            <Icon name="plus" size={10} /> Add
          </button>
        )}
      </div>

      {pickerOpen && (
        <div
          style={{
            marginTop: 10,
            paddingTop: 10,
            borderTop: '1px dashed var(--color-rule-soft)',
            display: 'flex',
            flexDirection: 'column',
            gap: 6,
          }}
        >
          <div className="t-micro">Add to shelf</div>
          {availableToAdd.length === 0 ? (
            <div className="t-small" style={{ fontStyle: 'italic' }}>
              Already on every shelf. Create one from the sidebar first.
            </div>
          ) : (
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
              {availableToAdd.map((s) => (
                <button
                  key={s.slug}
                  type="button"
                  className="chip"
                  style={{ cursor: 'pointer' }}
                  onClick={() => addMut.mutate(s.slug)}
                  disabled={addMut.isPending}
                >
                  {s.name}
                </button>
              ))}
            </div>
          )}
          <button
            type="button"
            className="btn ghost small"
            onClick={() => setPickerOpen(false)}
            style={{ alignSelf: 'flex-end', marginTop: 4 }}
          >
            Done
          </button>
        </div>
      )}
    </div>
  );
}

// NotesPanel renders every annotation on this book + an inline "new
// note" composer. The composer always creates a margin note
// (`selectedText` stays empty) — highlights come from the EPUB reader's
// selection flow, not from typing here.
function NotesPanel({ bookId }: { bookId: string }) {
  const queryClient = useQueryClient();
  const annotations = useQuery({
    queryKey: bookAnnotationsQueryKey(bookId),
    queryFn: () => fetchBookAnnotations(bookId),
  });

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: bookAnnotationsQueryKey(bookId) });
    queryClient.invalidateQueries({ queryKey: recentAnnotationsQueryKey });
  };

  const createMut = useMutation({
    mutationFn: (note: string) => createAnnotation(bookId, { note }),
    onSuccess: invalidate,
  });
  const deleteMut = useMutation({
    mutationFn: (a: Annotation) => deleteAnnotation(a.id),
    onSuccess: invalidate,
  });

  const [draft, setDraft] = useState('');
  const rows = annotations.data ?? [];
  const error = (createMut.error ?? deleteMut.error) as unknown as ApiError | null;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14, maxWidth: 640 }}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const value = draft.trim();
          if (!value) return;
          createMut.mutate(value);
          setDraft('');
        }}
        style={{
          display: 'flex',
          flexDirection: 'column',
          gap: 8,
          background: 'var(--color-paper-0)',
          border: '1px solid var(--color-rule-soft)',
          padding: 12,
          borderRadius: 2,
        }}
      >
        <textarea
          className="input"
          rows={3}
          placeholder="Add a note about this book…"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          style={{ fontFamily: 'var(--font-serif)', lineHeight: 1.5, resize: 'vertical' }}
        />
        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
          <button
            type="submit"
            className="btn primary small"
            disabled={createMut.isPending || draft.trim() === ''}
          >
            <Icon name="plus" size={12} /> {createMut.isPending ? 'Saving…' : 'Add note'}
          </button>
        </div>
      </form>

      {error && (
        <div
          style={{
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

      {annotations.isLoading && (
        <div className="t-small" style={{ fontStyle: 'italic' }}>Loading notes…</div>
      )}
      {!annotations.isLoading && rows.length === 0 && (
        <div className="t-small" style={{ fontStyle: 'italic' }}>
          No notes yet. Highlights and margin notes you take while reading will appear here.
        </div>
      )}
      {rows.map((a) => {
        const kind = annotationKind(a);
        return (
          <div
            key={a.id}
            style={{
              borderLeft: '3px solid var(--color-accent-soft)',
              padding: '8px 14px',
              background: 'var(--color-paper-0)',
            }}
          >
            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
              <span className="t-micro">
                {kind === 'highlight' ? 'Highlight' : kind === 'highlight+note' ? 'Highlight · Note' : 'Note'}
                {a.locator && ` · ${shortLocator(a.locator)}`}
              </span>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <span className="t-micro">{new Date(a.createdAt).toLocaleDateString()}</span>
                <button
                  type="button"
                  className="btn ghost small"
                  onClick={() => deleteMut.mutate(a)}
                  disabled={deleteMut.isPending}
                  aria-label="Delete"
                  title="Delete"
                  style={{ padding: 4 }}
                >
                  <Icon name="close" size={11} />
                </button>
              </div>
            </div>
            {a.selectedText && (
              <p
                style={{
                  fontSize: 14.5,
                  lineHeight: 1.55,
                  fontStyle: 'italic',
                  background: 'oklch(0.94 0.04 85)',
                  padding: '4px 8px',
                  marginBottom: a.note ? 8 : 0,
                }}
              >
                {a.selectedText}
              </p>
            )}
            {a.note && (
              <p style={{ fontSize: 14.5, lineHeight: 1.55 }}>{a.note}</p>
            )}
          </div>
        );
      })}
    </div>
  );
}

function shortLocator(locator: string): string {
  if (locator.startsWith('page:')) return `p.${locator.slice(5)}`;
  if (locator.startsWith('epubcfi')) return 'EPUB';
  return locator;
}

// SendToDeviceButton opens a tiny dropdown of paired devices. If none are
// paired, the button is a shortcut into Settings → Device sync.
function SendToDeviceButton({ bookId }: { bookId: string }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const devices = useQuery({ queryKey: devicesQueryKey, queryFn: fetchDevices });
  const [open, setOpen] = useState(false);
  const [toast, setToast] = useState<{ kind: 'ok' | 'err'; msg: string } | null>(null);

  const sendMut = useMutation({
    mutationFn: (deviceId: string) => sendBookToDevice(bookId, deviceId),
    onSuccess: (_data, deviceId) => {
      const target = devices.data?.find((d) => d.id === deviceId);
      setToast({
        kind: 'ok',
        msg: `Sent to ${target?.name ?? 'device'}.`,
      });
      queryClient.invalidateQueries({ queryKey: devicesQueryKey });
      setOpen(false);
    },
    onError: (e) => {
      setToast({ kind: 'err', msg: (e as unknown as ApiError).message });
    },
  });

  const list = devices.data ?? [];

  if (list.length === 0) {
    return (
      <button
        className="btn small"
        onClick={() => void navigate({ to: '/settings' })}
        title="No devices paired — add one in Settings"
      >
        <Icon name="device" size={13} /> Send to device
      </button>
    );
  }

  return (
    <div style={{ position: 'relative' }}>
      <button
        className="btn small"
        onClick={() => setOpen((v) => !v)}
        disabled={sendMut.isPending}
      >
        <Icon name="device" size={13} />{' '}
        {sendMut.isPending ? 'Sending…' : 'Send to device'}
      </button>
      {open && (
        <div
          style={{
            position: 'absolute',
            top: 'calc(100% + 4px)',
            right: 0,
            minWidth: 220,
            background: 'var(--color-paper-0)',
            border: '1px solid var(--color-rule)',
            boxShadow: '0 8px 24px oklch(0.2 0.02 60 / 0.15)',
            zIndex: 20,
          }}
        >
          {list.map((d) => (
            <SendTarget key={d.id} device={d} onPick={() => sendMut.mutate(d.id)} />
          ))}
        </div>
      )}
      {toast && (
        <div
          style={{
            position: 'absolute',
            top: 'calc(100% + 6px)',
            right: 0,
            padding: '8px 12px',
            background: 'var(--color-paper-0)',
            border: `1px solid ${toast.kind === 'ok' ? 'oklch(0.58 0.12 140)' : 'var(--color-accent)'}`,
            color: toast.kind === 'ok' ? 'oklch(0.45 0.12 140)' : 'var(--color-accent-ink)',
            fontSize: 12.5,
            maxWidth: 320,
            zIndex: 21,
          }}
          onClick={() => setToast(null)}
          role="status"
        >
          {toast.msg}
        </div>
      )}
    </div>
  );
}

function SendTarget({ device, onPick }: { device: Device; onPick: () => void }) {
  return (
    <button
      type="button"
      onClick={onPick}
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 2,
        padding: '10px 12px',
        width: '100%',
        textAlign: 'left',
        background: 'transparent',
        border: 'none',
        borderBottom: '1px solid var(--color-rule-soft)',
        cursor: 'pointer',
        fontFamily: 'inherit',
      }}
    >
      <span style={{ fontSize: 13.5, fontWeight: 500 }}>{device.name}</span>
      <span className="t-small" style={{ fontSize: 11 }}>
        {DEVICE_KIND_LABELS[device.kind]}
      </span>
    </button>
  );
}
