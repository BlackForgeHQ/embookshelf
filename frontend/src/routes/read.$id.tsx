import { useEffect, useMemo, useRef, useState } from 'react';
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
import {
  bookQueryKey,
  fetchBook,
  updateProgress,
  type BookDetail,
} from '@/api/books';
import {
  EpubReader,
  type EpubHighlight,
  type EpubProgress,
  type EpubReaderHandle,
  type EpubTocEntry,
} from '@/components/EpubReader';
import { Icon } from '@/components/Icon';
import {
  PdfReader,
  type PdfProgress,
  type PdfReaderHandle,
} from '@/components/PdfReader';

export const Route = createFileRoute('/read/$id')({
  component: Reader,
});

type TocItem = { label: string; href: string };

// parseResumeToken separates the two resume-token shapes we store in the
// database: raw CFI strings (EPUB) and page:N tokens (PDF). Unknown tokens
// fall back to "start from the beginning".
function parseResumeToken(raw?: string): { cfi?: string; page?: number } {
  if (!raw) return {};
  if (raw.startsWith('page:')) {
    const page = Number.parseInt(raw.slice(5), 10);
    return Number.isFinite(page) ? { page } : {};
  }
  return { cfi: raw };
}

function Reader() {
  const { id } = Route.useParams();
  const navigate = useNavigate();

  const book = useQuery({ queryKey: bookQueryKey(id), queryFn: () => fetchBook(id) });

  if (book.isLoading) {
    return <FullScreenMessage>Loading…</FullScreenMessage>;
  }
  if (book.isError || !book.data) {
    return <FullScreenMessage>Book not found.</FullScreenMessage>;
  }
  const b = book.data;
  if (b.format !== 'EPUB' && b.format !== 'PDF') {
    return (
      <FullScreenMessage>
        Reader not implemented for <code>{b.format}</code> yet.
        <div style={{ marginTop: 16 }}>
          <button
            className="btn"
            onClick={() => void navigate({ to: '/book/$id', params: { id } })}
          >
            <Icon name="arrow-left" size={14} /> Back
          </button>
        </div>
      </FullScreenMessage>
    );
  }

  return <ReaderShell book={b} />;
}

function ReaderShell({ book }: { book: BookDetail }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { cfi: resumeCfi, page: resumePage } = parseResumeToken(book.resumeCfi);

  const [chromeVisible, setChromeVisible] = useState(true);
  const [tocOpen, setTocOpen] = useState(false);
  const [notesOpen, setNotesOpen] = useState(false);
  const [typePanelOpen, setTypePanelOpen] = useState(false);
  const [toc, setToc] = useState<TocItem[]>([]);

  // Progress state mirrors what the reader reports. Used for the bottom
  // scrubber and to compose the token we persist on unmount.
  const [percent, setPercent] = useState(0);
  const [pageState, setPageState] = useState<{ current: number; total: number } | null>(null);
  const [cfiState, setCfiState] = useState<string>(resumeCfi ?? '');

  // Pending EPUB selection — set by rendition.on('selected'), cleared
  // when the user saves or dismisses it. Absence hides the selection
  // toolbar, so this doubles as the toolbar's visibility switch.
  const [pendingSelection, setPendingSelection] = useState<
    { cfiRange: string; text: string } | null
  >(null);

  const epubRef = useRef<EpubReaderHandle>(null);
  const pdfRef = useRef<PdfReaderHandle>(null);

  const progressMut = useMutation({
    mutationFn: (args: { progress: number; resumeCfi: string }) =>
      updateProgress(book.id, args.progress, args.resumeCfi),
  });

  // Annotations for this book — drives the side panel AND the EPUB
  // highlight overlay.
  const annotations = useQuery({
    queryKey: bookAnnotationsQueryKey(book.id),
    queryFn: () => fetchBookAnnotations(book.id),
  });

  const invalidateAnnotations = () => {
    queryClient.invalidateQueries({ queryKey: bookAnnotationsQueryKey(book.id) });
    queryClient.invalidateQueries({ queryKey: recentAnnotationsQueryKey });
  };

  const createAnnotationMut = useMutation({
    mutationFn: (body: { locator?: string; selectedText?: string; note?: string }) =>
      createAnnotation(book.id, body),
    onSuccess: () => {
      invalidateAnnotations();
      setPendingSelection(null);
    },
  });

  const deleteAnnotationMut = useMutation({
    mutationFn: (a: Annotation) => deleteAnnotation(a.id),
    onSuccess: invalidateAnnotations,
  });

  // EPUB highlights for the rendition overlay. Stable reference when the
  // annotation list hasn't changed, so the effect in EpubReader doesn't
  // churn add/remove on every render.
  const epubHighlights = useMemo<EpubHighlight[]>(() => {
    if (book.format !== 'EPUB') return [];
    return (annotations.data ?? [])
      .filter((a) => !!a.selectedText && !!a.locator?.startsWith('epubcfi'))
      .map((a) => ({ cfiRange: a.locator!, color: 'oklch(0.92 0.07 85)' }));
  }, [book.format, annotations.data]);

  // Debounce + latest-wins: reader events fire every page turn; we hold
  // the newest tick for 600 ms and ship it, plus force a flush on unmount
  // so a short reading session still records progress.
  const pendingRef = useRef<{ progress: number; resumeCfi: string } | null>(null);
  const timerRef = useRef<number | null>(null);
  useEffect(() => {
    return () => {
      if (timerRef.current) window.clearTimeout(timerRef.current);
      if (pendingRef.current) {
        // Fire-and-forget — the component is already unmounting.
        void updateProgress(book.id, pendingRef.current.progress, pendingRef.current.resumeCfi);
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const queueProgress = (progress: number, resumeCfi: string) => {
    pendingRef.current = { progress, resumeCfi };
    if (timerRef.current) window.clearTimeout(timerRef.current);
    timerRef.current = window.setTimeout(() => {
      const snapshot = pendingRef.current;
      pendingRef.current = null;
      timerRef.current = null;
      if (snapshot) {
        progressMut.mutate(snapshot);
      }
    }, 600);
  };

  const onEpubProgress = (p: EpubProgress) => {
    setPercent(p.percent);
    setCfiState(p.cfi);
    queueProgress(p.percent, p.cfi);
  };
  const onPdfProgress = (p: PdfProgress) => {
    setPercent(p.percent);
    setPageState({ current: p.page, total: p.totalPages });
    queueProgress(p.percent, `page:${p.page}`);
  };

  const closePanels = () => {
    setTocOpen(false);
    setNotesOpen(false);
    setTypePanelOpen(false);
  };

  const exit = () => void navigate({ to: '/book/$id', params: { id: book.id } });

  // Derived footer values — keep both reader shapes on one code path.
  const footerPageLabel =
    book.format === 'PDF' && pageState
      ? `p.${pageState.current}`
      : book.format === 'EPUB' && percent
        ? `${Math.round(percent * 100)}%`
        : '—';
  const footerTotalLabel =
    book.format === 'PDF' && pageState ? `p.${pageState.total}` : '';

  return (
    <div
      className="fade-in"
      style={{
        position: 'fixed',
        inset: 0,
        background: 'var(--color-paper-0)',
        zIndex: 200,
        display: 'flex',
        flexDirection: 'column',
      }}
    >
      {chromeVisible && (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 12,
            padding: '10px 22px',
            borderBottom: '1px solid var(--color-rule-soft)',
            background: 'var(--color-paper-1)',
          }}
        >
          <button className="btn ghost small" onClick={exit}>
            <Icon name="arrow-left" size={14} /> Library
          </button>
          <div style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
            <div style={{ fontSize: 13, fontWeight: 500, fontStyle: 'italic' }}>{book.title}</div>
            <div className="t-micro" style={{ fontSize: 10 }}>
              {book.author} · {footerPageLabel}
              {footerTotalLabel ? ` / ${footerTotalLabel}` : ''}
            </div>
          </div>
          {book.format === 'EPUB' && (
            <button
              className={`btn ghost small ${tocOpen ? 'primary' : ''}`}
              onClick={() => {
                const next = !tocOpen;
                closePanels();
                setTocOpen(next);
              }}
            >
              <Icon name="contents" size={14} />
            </button>
          )}
          <button
            className={`btn ghost small ${typePanelOpen ? 'primary' : ''}`}
            onClick={() => {
              const next = !typePanelOpen;
              closePanels();
              setTypePanelOpen(next);
            }}
          >
            <Icon name="aA" size={14} />
          </button>
          <button className="btn ghost small" aria-label="Bookmark">
            <Icon name="bookmark" size={14} />
          </button>
          <button
            className={`btn ghost small ${notesOpen ? 'primary' : ''}`}
            onClick={() => {
              const next = !notesOpen;
              closePanels();
              setNotesOpen(next);
            }}
          >
            <Icon name="note" size={14} />
          </button>
          <button className="btn ghost small" onClick={() => setChromeVisible(false)} title="Hide chrome">
            <Icon name="close" size={14} />
          </button>
        </div>
      )}

      <div style={{ flex: 1, display: 'flex', overflow: 'hidden', position: 'relative' }}>
        {/* Left TOC (EPUB only) */}
        {tocOpen && book.format === 'EPUB' && (
          <aside
            style={{
              width: 280,
              borderRight: '1px solid var(--color-rule-soft)',
              background: 'var(--color-paper-1)',
              overflow: 'auto',
              padding: '18px 0',
              flexShrink: 0,
            }}
          >
            <div className="t-label" style={{ padding: '0 20px 10px' }}>Contents</div>
            {toc.length === 0 && (
              <div className="t-small" style={{ padding: '0 20px', fontStyle: 'italic' }}>
                Table of contents not available.
              </div>
            )}
            {toc.map((c, i) => (
              <button
                key={`${c.href}-${i}`}
                onClick={() => {
                  epubRef.current?.goTo(c.href);
                  setTocOpen(false);
                }}
                style={{
                  display: 'block',
                  padding: '8px 20px',
                  width: '100%',
                  textAlign: 'left',
                  border: 'none',
                  background: 'transparent',
                  fontFamily: 'var(--font-serif)',
                  fontSize: 13.5,
                  color: 'var(--color-ink-2)',
                  cursor: 'pointer',
                  borderLeft: '2px solid transparent',
                }}
              >
                {c.label}
              </button>
            ))}
          </aside>
        )}

        {/* Reading area */}
        <div
          onClick={() => setChromeVisible(true)}
          style={{
            flex: 1,
            overflow: 'hidden',
            position: 'relative',
            background: book.format === 'EPUB' ? 'var(--color-paper-0)' : 'var(--color-paper-2)',
          }}
        >
          {book.format === 'EPUB' ? (
            <EpubReader
              ref={epubRef}
              url={`/api/v1/books/${book.id}/file`}
              initialCfi={resumeCfi}
              highlights={epubHighlights}
              onReady={({ toc: t }) => setToc(t.map(flatten).flat())}
              onProgress={onEpubProgress}
              onSelect={(sel) => setPendingSelection(sel)}
            />
          ) : (
            <PdfReader
              ref={pdfRef}
              url={`/api/v1/books/${book.id}/file`}
              initialPage={resumePage}
              onReady={({ totalPages }) => setPageState({ current: resumePage ?? 1, total: totalPages })}
              onProgress={onPdfProgress}
            />
          )}

          {/* Selection toolbar — shown whenever the user drags across
              EPUB text and epub.js emits a `selected` event. Pending
              selection is cleared on save or dismiss. */}
          {pendingSelection && (
            <div
              style={{
                position: 'absolute',
                top: 16,
                left: '50%',
                transform: 'translateX(-50%)',
                zIndex: 10,
                display: 'flex',
                alignItems: 'center',
                gap: 10,
                padding: '8px 14px',
                background: 'var(--color-paper-0)',
                border: '1px solid var(--color-ink-3)',
                borderRadius: 3,
                boxShadow: '0 6px 20px -4px oklch(0.2 0.02 60 / 0.22)',
              }}
            >
              <span className="t-micro">Selection</span>
              <button
                type="button"
                className="btn primary small"
                disabled={createAnnotationMut.isPending}
                onClick={() =>
                  createAnnotationMut.mutate({
                    locator: pendingSelection.cfiRange,
                    selectedText: pendingSelection.text,
                  })
                }
              >
                <Icon name="highlight" size={12} /> Highlight
              </button>
              <button
                type="button"
                className="btn small"
                disabled={createAnnotationMut.isPending}
                onClick={() => {
                  const note = window.prompt('Add a note for this selection:');
                  if (!note || !note.trim()) return;
                  createAnnotationMut.mutate({
                    locator: pendingSelection.cfiRange,
                    selectedText: pendingSelection.text,
                    note: note.trim(),
                  });
                }}
              >
                <Icon name="note" size={12} /> Note
              </button>
              <button
                type="button"
                className="btn ghost small"
                onClick={() => setPendingSelection(null)}
                aria-label="Dismiss"
              >
                <Icon name="close" size={12} />
              </button>
            </div>
          )}
        </div>

        {/* Type panel (floating) */}
        {typePanelOpen && (
          <div
            style={{
              position: 'absolute',
              top: 0,
              right: 16,
              width: 260,
              background: 'var(--color-paper-0)',
              border: '1px solid var(--color-ink-3)',
              boxShadow: '0 12px 32px -8px oklch(0.2 0.02 60 / 0.22)',
              padding: '14px 16px',
              borderRadius: 2,
              zIndex: 5,
            }}
          >
            <div className="t-label" style={{ marginBottom: 10 }}>Reader type</div>
            <div style={{ fontSize: 12, color: 'var(--color-ink-3)', fontStyle: 'italic' }}>
              Font + size controls land once per-user reader preferences sync from the backend.
            </div>
          </div>
        )}

        {notesOpen && (
          <aside
            style={{
              width: 320,
              borderLeft: '1px solid var(--color-rule-soft)',
              background: 'var(--color-paper-1)',
              overflow: 'auto',
              padding: '18px 16px',
              flexShrink: 0,
              display: 'flex',
              flexDirection: 'column',
              gap: 10,
            }}
          >
            <div className="t-label">Notes on this book</div>

            {book.format === 'PDF' && pageState && (
              <button
                type="button"
                className="btn small"
                style={{ justifyContent: 'center' }}
                disabled={createAnnotationMut.isPending}
                onClick={() => {
                  const note = window.prompt(`Note on page ${pageState.current}:`);
                  if (!note || !note.trim()) return;
                  createAnnotationMut.mutate({
                    locator: `page:${pageState.current}`,
                    note: note.trim(),
                  });
                }}
              >
                <Icon name="plus" size={12} /> New note on page {pageState.current}
              </button>
            )}

            {annotations.isLoading && (
              <div className="t-small" style={{ fontStyle: 'italic' }}>Loading…</div>
            )}
            {!annotations.isLoading && (annotations.data ?? []).length === 0 && (
              <div className="t-small" style={{ fontStyle: 'italic' }}>
                {book.format === 'EPUB'
                  ? 'Select text in the page to highlight or annotate.'
                  : 'No notes yet.'}
              </div>
            )}

            {(annotations.data ?? []).map((a) => {
              const kind = annotationKind(a);
              return (
                <div
                  key={a.id}
                  style={{
                    borderLeft: '3px solid var(--color-accent-soft)',
                    padding: '6px 10px',
                    background: 'var(--color-paper-0)',
                  }}
                >
                  <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
                    <span className="t-micro" style={{ fontSize: 9.5 }}>
                      {kind === 'highlight' ? 'Highlight' : kind === 'highlight+note' ? 'Highlight · Note' : 'Note'}
                      {a.locator && a.locator.startsWith('page:') && ` · p.${a.locator.slice(5)}`}
                    </span>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                      {a.locator?.startsWith('epubcfi') && book.format === 'EPUB' && (
                        <button
                          type="button"
                          className="btn ghost small"
                          onClick={() => epubRef.current?.goTo(a.locator!)}
                          title="Go to highlight"
                          style={{ padding: 2 }}
                        >
                          <Icon name="arrow-right" size={10} />
                        </button>
                      )}
                      {a.locator?.startsWith('page:') && book.format === 'PDF' && (
                        <button
                          type="button"
                          className="btn ghost small"
                          onClick={() => {
                            const page = Number.parseInt(a.locator!.slice(5), 10);
                            if (Number.isFinite(page)) pdfRef.current?.goTo(page);
                          }}
                          title="Go to page"
                          style={{ padding: 2 }}
                        >
                          <Icon name="arrow-right" size={10} />
                        </button>
                      )}
                      <button
                        type="button"
                        className="btn ghost small"
                        onClick={() => deleteAnnotationMut.mutate(a)}
                        disabled={deleteAnnotationMut.isPending}
                        aria-label="Delete"
                        title="Delete"
                        style={{ padding: 2 }}
                      >
                        <Icon name="close" size={10} />
                      </button>
                    </div>
                  </div>
                  {a.selectedText && (
                    <p
                      style={{
                        fontSize: 12.5,
                        lineHeight: 1.5,
                        fontStyle: 'italic',
                        background: 'oklch(0.94 0.04 85)',
                        padding: '4px 6px',
                        marginBottom: a.note ? 6 : 0,
                      }}
                    >
                      {a.selectedText}
                    </p>
                  )}
                  {a.note && (
                    <p style={{ fontSize: 13, lineHeight: 1.5 }}>{a.note}</p>
                  )}
                </div>
              );
            })}
          </aside>
        )}
      </div>

      {/* Bottom — progress + page controls */}
      <div
        style={{
          borderTop: '1px solid var(--color-rule-soft)',
          padding: '10px 22px',
          display: 'flex',
          alignItems: 'center',
          gap: 14,
          background: 'var(--color-paper-1)',
        }}
      >
        <button
          className="btn ghost small"
          onClick={() => (book.format === 'EPUB' ? epubRef.current?.prev() : pdfRef.current?.prev())}
        >
          <Icon name="chevron-left" size={14} /> Prev
        </button>
        <div style={{ flex: 1, display: 'flex', alignItems: 'center', gap: 12 }}>
          <span className="mono" style={{ fontSize: 10.5, color: 'var(--color-ink-3)' }}>
            {footerPageLabel}
          </span>
          <div
            style={{
              flex: 1,
              position: 'relative',
              height: 4,
              background: 'var(--color-paper-3)',
              borderRadius: 2,
            }}
          >
            <div
              style={{
                height: 4,
                width: `${Math.round(percent * 100)}%`,
                background: 'var(--color-accent)',
                borderRadius: 2,
                transition: 'width 120ms ease',
              }}
            />
          </div>
          <span className="mono" style={{ fontSize: 10.5, color: 'var(--color-ink-3)' }}>
            {footerTotalLabel || `${Math.round(percent * 100)}%`}
          </span>
        </div>
        <button
          className="btn ghost small"
          onClick={() => (book.format === 'EPUB' ? epubRef.current?.next() : pdfRef.current?.next())}
        >
          Next <Icon name="chevron-right" size={14} />
        </button>
      </div>

      {!chromeVisible && (
        <button
          onClick={() => setChromeVisible(true)}
          className="btn ghost"
          style={{
            position: 'absolute',
            top: 8,
            right: 8,
            zIndex: 10,
            background: 'var(--color-paper-0)',
            border: '1px solid var(--color-rule-soft)',
          }}
        >
          <Icon name="menu" size={14} />
        </button>
      )}

      {/* Intentional reference — keeps cfiState alive for the bookmark
          feature once annotations land. */}
      <span hidden>{cfiState}</span>
    </div>
  );
}

function FullScreenMessage({ children }: { children: React.ReactNode }) {
  return (
    <div
      style={{
        position: 'fixed',
        inset: 0,
        background: 'var(--color-paper-0)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
      }}
    >
      <div className="t-small" style={{ textAlign: 'center', maxWidth: 360 }}>
        {children}
      </div>
    </div>
  );
}

// flatten walks the EPUB TOC tree into a flat list for the simple linear
// Contents panel. Full-tree rendering is a future visual polish.
function flatten(node: EpubTocEntry): TocItem[] {
  const self: TocItem = { label: node.label, href: node.href };
  const sub = (node.subitems ?? []).flatMap(flatten);
  return [self, ...sub];
}
