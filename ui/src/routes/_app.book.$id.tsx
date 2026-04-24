import { useEffect, useState, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { toast } from 'sonner';

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
} from '@/api/devices';
import { Cover, StarRating } from '@/components/Cover';
import { Icon } from '@/components/Icon';
import { useUserSettingsDialog } from '@/components/UserSettingsDialog';
import type { ApiError } from '@/api/client';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';

type Tab = 'overview' | 'notes' | 'annotations' | 'versions' | 'activity';

export const Route = createFileRoute('/_app/book/$id')({
  component: BookDetail,
});

function BookDetail() {
  const { id } = Route.useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [tab, setTab] = useState<Tab>('overview');
  const [deleteOpen, setDeleteOpen] = useState(false);

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
        <Button variant="ghost" size="sm" onClick={() => void navigate({ to: '/library' })}>
          <Icon name="arrow-left" size={14} /> Back to library
        </Button>
        <div className="grow" />
        <Button
          variant="outline"
          size="sm"
          onClick={() => void navigate({ to: '/book/$id/edit', params: { id } })}
        >
          <Icon name="edit" size={13} /> Edit metadata
        </Button>
        <Button variant="outline" size="sm" asChild>
          <a
            href={`/api/v1/books/${id}/file?download=1`}
            // `download` hints the browser save-as; the server already
            // sets Content-Disposition: attachment with the right
            // filename, so this attribute is mostly belt-and-braces.
            download
          >
            <Icon name="download" size={13} /> Download
          </a>
        </Button>
        <SendToDeviceButton bookId={id} />
      </div>

      <div className="page-split page-split--cover-main" style={{ padding: '40px 48px' }}>
        {/* Left — cover & actions */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
          <Cover book={b} size="hero" />
          <Button
            size="lg"
            className="w-full"
            onClick={() => void navigate({ to: '/read/$id', params: { id } })}
          >
            <Icon name="book-open" size={14} /> {progress > 0 && progress < 1 ? 'Continue reading' : 'Open book'}
          </Button>
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

          <Tabs value={tab} onValueChange={(v) => setTab(v as Tab)} className="mb-6">
            <TabsList
              variant="line"
              className="h-9 w-full justify-start gap-4 border-b border-(--color-rule-soft) px-0"
            >
              <TabsTrigger value="overview" className="flex-none px-3">Overview</TabsTrigger>
              <TabsTrigger value="notes" className="flex-none px-3">Notes</TabsTrigger>
              <TabsTrigger value="annotations" className="flex-none px-3">Annotations</TabsTrigger>
              <TabsTrigger value="versions" className="flex-none px-3">Versions</TabsTrigger>
              <TabsTrigger value="activity" className="flex-none px-3">Activity</TabsTrigger>
            </TabsList>

            <TabsContent value="overview">
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
            </TabsContent>

            <TabsContent value="notes">
              <NotesPanel bookId={id} />
            </TabsContent>

            <TabsContent value="annotations">
              <div className="t-small" style={{ fontStyle: 'italic' }}>
                No PDF annotations for this book.
              </div>
            </TabsContent>

            <TabsContent value="versions">
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
                  <div className="grow">
                    <div className="t-item-title">
                      {b.title}.{b.format.toLowerCase()}
                    </div>
                    <div className="mono" style={{ fontSize: 11, color: 'var(--color-ink-3)' }}>
                      Primary · {b.format}
                    </div>
                  </div>
                </div>

                {isAdmin && (
                  <div
                    style={{
                      padding: 16,
                      border: '1px solid var(--color-destructive)',
                      background: 'var(--color-paper-0)',
                    }}
                  >
                    <div
                      className="t-label"
                      style={{ marginBottom: 6, color: 'var(--color-destructive)' }}
                    >
                      Danger zone
                    </div>
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
                          border: '1px solid var(--color-destructive)',
                          color: 'var(--color-destructive)',
                          fontSize: 13,
                        }}
                      >
                        {deleteError.message}
                      </div>
                    )}
                    <Button
                      type="button"
                      variant="destructive"
                      size="sm"
                      disabled={deleteMut.isPending}
                      onClick={() => setDeleteOpen(true)}
                    >
                      <Icon name="close" size={12} />{' '}
                      {deleteMut.isPending ? 'Deleting…' : 'Delete book'}
                    </Button>

                    <DeleteBookDialog
                      open={deleteOpen}
                      onOpenChange={setDeleteOpen}
                      title={b.title}
                      busy={deleteMut.isPending}
                      onConfirm={() => {
                        deleteMut.mutate();
                        setDeleteOpen(false);
                      }}
                    />
                  </div>
                )}
              </div>
            </TabsContent>

            <TabsContent value="activity">
              <div className="t-small" style={{ fontStyle: 'italic' }}>
                Per-book activity timeline lands once reading sessions are tracked server-side.
              </div>
            </TabsContent>
          </Tabs>
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
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => setPickerOpen(false)}
            className="self-end mt-1"
          >
            Done
          </Button>
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
        <Textarea
          rows={3}
          placeholder="Add a note about this book…"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          className="resize-y"
        />
        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
          <Button
            type="submit"
            size="sm"
            disabled={createMut.isPending || draft.trim() === ''}
          >
            <Icon name="plus" size={12} /> {createMut.isPending ? 'Saving…' : 'Add note'}
          </Button>
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
// paired, the button opens the user settings dialog on the Devices panel.
function SendToDeviceButton({ bookId }: { bookId: string }) {
  const { open: openUserSettings } = useUserSettingsDialog();
  const queryClient = useQueryClient();
  const devices = useQuery({ queryKey: devicesQueryKey, queryFn: fetchDevices });
  const [open, setOpen] = useState(false);

  const sendMut = useMutation({
    mutationFn: (deviceId: string) => sendBookToDevice(bookId, deviceId),
    onSuccess: (_data, deviceId) => {
      const target = devices.data?.find((d) => d.id === deviceId);
      toast.success(`Sent to ${target?.name ?? 'device'}.`);
      queryClient.invalidateQueries({ queryKey: devicesQueryKey });
      setOpen(false);
    },
    onError: (e) => {
      toast.error((e as unknown as ApiError).message || 'Send failed.');
    },
  });

  const list = devices.data ?? [];

  if (list.length === 0) {
    return (
      <Button
        variant="outline"
        size="sm"
        onClick={() => openUserSettings('devices')}
        title="No devices paired — pair one in Account → Device sync"
      >
        <Icon name="device" size={13} /> Send to device
      </Button>
    );
  }

  return (
    <DropdownMenu open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" disabled={sendMut.isPending}>
          <Icon name="device" size={13} />{' '}
          {sendMut.isPending ? 'Sending…' : 'Send to device'}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-56">
        {list.map((d) => (
          <DropdownMenuItem
            key={d.id}
            onSelect={() => sendMut.mutate(d.id)}
            className="flex flex-col items-start gap-0.5"
          >
            <span className="t-item-title">{d.name}</span>
            <span className="t-small" style={{ fontSize: 11 }}>
              {DEVICE_KIND_LABELS[d.kind]}
            </span>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// DeleteBookDialog confirms a destructive book teardown. The "type the
// title to confirm" gate matches the weight of the operation — the DB
// row, its cover, the source file on disk, and every reader's notes,
// progress, and shelf placements go with it.
function DeleteBookDialog({
  open,
  onOpenChange,
  title,
  busy,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  busy: boolean;
  onConfirm: () => void;
}) {
  const [confirmInput, setConfirmInput] = useState('');

  useEffect(() => {
    if (!open) setConfirmInput('');
  }, [open]);

  const matches = confirmInput.trim() === title.trim();

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[440px]">
        <DialogHeader>
          <DialogTitle>Delete book</DialogTitle>
          <DialogDescription>
            Permanently remove <strong>{title}</strong> — the DB row, its
            cover, its source file on disk, and every reader&apos;s progress,
            notes, and shelf placements for it. This cannot be undone.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-2">
          <Label htmlFor="delete-book-confirm">
            Type the title to confirm.
          </Label>
          <Input
            id="delete-book-confirm"
            value={confirmInput}
            onChange={(e) => setConfirmInput(e.target.value)}
            placeholder={title}
            autoFocus
          />
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            onClick={() => onOpenChange(false)}
            disabled={busy}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            onClick={onConfirm}
            disabled={!matches || busy}
          >
            {busy ? 'Deleting…' : 'Delete book'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
