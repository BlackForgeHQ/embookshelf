import { useMemo, useState, type CSSProperties } from 'react';
import { useQuery } from '@tanstack/react-query';
import { createFileRoute, useNavigate } from '@tanstack/react-router';

import {
  booksQueryKey,
  fetchBooks,
  fetchLibraries,
  fetchShelves,
  librariesQueryKey,
  shelvesQueryKey,
  type Book,
} from '@/api/books';
import { Cover, StarRating } from '@/components/Cover';
import { Icon } from '@/components/Icon';
import { TopBar } from '@/components/TopBar';
import { Button } from '@/components/ui/button';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';

type Layout = 'shelf' | 'grid' | 'list';
type SortKey = 'added' | 'title' | 'author' | 'rating';

type LibrarySearch = {
  shelf?: string;
  library?: string;
  layout?: Layout;
};

export const Route = createFileRoute('/_app/library')({
  validateSearch: (raw: Record<string, unknown>): LibrarySearch => ({
    shelf: typeof raw.shelf === 'string' ? raw.shelf : undefined,
    library: typeof raw.library === 'string' ? raw.library : undefined,
    layout:
      raw.layout === 'shelf' || raw.layout === 'grid' || raw.layout === 'list'
        ? raw.layout
        : undefined,
  }),
  component: LibraryView,
});

// sortKeyToApi maps our local UI sort terms onto the backend's vocabulary.
// The backend uses "recent" for created_at DESC; the UI calls it "added".
function sortKeyToApi(k: SortKey): 'title' | 'author' | 'recent' | 'rating' {
  return k === 'added' ? 'recent' : k;
}

function LibraryView() {
  const navigate = useNavigate();
  const { shelf: activeShelf, library: activeLibrary, layout: layoutSearch } =
    Route.useSearch();
  const layout: Layout = layoutSearch ?? 'grid';

  const [search, setSearch] = useState('');
  const [sortBy, setSortBy] = useState<SortKey>('added');
  const [filterFormat, setFilterFormat] = useState<string | null>(null);

  // Shelf filter takes precedence on the server; library + q + format are
  // merged as additional filters otherwise.
  const queryParams = {
    shelf: activeShelf || undefined,
    library: activeLibrary || undefined,
    q: search || undefined,
    format: filterFormat ? [filterFormat] : undefined,
    sort: sortKeyToApi(sortBy),
  };

  const books = useQuery({
    queryKey: booksQueryKey(queryParams),
    queryFn: () => fetchBooks(queryParams),
    placeholderData: (prev) => prev,
  });

  const libraries = useQuery({ queryKey: librariesQueryKey, queryFn: fetchLibraries });
  const shelves = useQuery({ queryKey: shelvesQueryKey, queryFn: fetchShelves });

  const rows = books.data?.books ?? [];

  const { shelfTitle, subtitle } = useMemo(() => {
    if (activeShelf) {
      const s = shelves.data?.find((x) => x.slug === activeShelf);
      if (s) {
        return {
          shelfTitle: s.name,
          subtitle:
            activeShelf === 'reading'
              ? 'Books with a ribbon still in them.'
              : activeShelf === 'finished'
                ? 'Shelved, loved, occasionally revisited.'
                : `${s.bookCount} volumes on this shelf.`,
        };
      }
      return { shelfTitle: 'Shelf', subtitle: '' };
    }
    if (activeLibrary) {
      const lib = libraries.data?.find((x) => x.slug === activeLibrary);
      if (lib) {
        return {
          shelfTitle: lib.name,
          subtitle: `${lib.bookCount} volumes in this library.`,
        };
      }
    }
    return {
      shelfTitle: 'All Books',
      subtitle: 'Your complete collection across every library.',
    };
  }, [activeShelf, activeLibrary, shelves.data, libraries.data]);

  const setLayout = (next: Layout) => {
    void navigate({ to: '/library', search: (prev) => ({ ...prev, layout: next }) });
  };

  const openBook = (id: string) => void navigate({ to: '/book/$id', params: { id } });

  const layoutBtn = (name: Layout, label: string) => (
    <Button
      variant={layout === name ? 'default' : 'outline'}
      size="icon-sm"
      onClick={() => setLayout(name)}
      title={label}
    >
      <Icon name={name === 'shelf' ? 'shelf' : name === 'grid' ? 'grid' : 'list'} size={13} />
    </Button>
  );

  return (
    <div className="fade-in">
      <TopBar
        title={shelfTitle}
        subtitle={subtitle}
        search={search}
        setSearch={setSearch}
        right={
          <div style={{ display: 'flex', gap: 4 }}>
            {layoutBtn('shelf', 'Shelf')}
            {layoutBtn('grid', 'Grid')}
            {layoutBtn('list', 'List')}
          </div>
        }
      />

      {/* Filter rail */}
      <div
        style={{
          padding: '12px 32px',
          borderBottom: '1px solid var(--color-rule-soft)',
          display: 'flex',
          alignItems: 'center',
          gap: 10,
          background: 'var(--color-paper-1)',
        }}
      >
        <span className="t-label">Filter</span>
        {[null, 'EPUB', 'PDF', 'CBZ', 'M4B'].map((f) => (
          <button
            key={f ?? 'all'}
            className={`chip ${filterFormat === f ? 'active' : ''}`}
            onClick={() => setFilterFormat(f)}
            style={{ cursor: 'pointer', border: 'none' }}
          >
            {f ?? 'All formats'}
          </button>
        ))}
        <div style={{ flex: 1 }} />
        <span className="t-label">Sort</span>
        <Select value={sortBy} onValueChange={(v) => setSortBy(v as SortKey)}>
          <SelectTrigger size="sm" className="w-auto">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="added">Recently added</SelectItem>
            <SelectItem value="title">Title</SelectItem>
            <SelectItem value="author">Author</SelectItem>
            <SelectItem value="rating">Rating</SelectItem>
          </SelectContent>
        </Select>
        <span className="mono" style={{ fontSize: 11, color: 'var(--color-ink-3)', marginLeft: 8 }}>
          {books.isLoading ? 'loading…' : `${rows.length} volumes`}
        </span>
      </div>

      {/* Content */}
      <div style={{ padding: '28px 32px 80px', flex: 1 }}>
        {books.isError ? (
          <ErrorPanel message="Failed to load books." />
        ) : rows.length === 0 && !books.isLoading ? (
          <EmptyPanel />
        ) : layout === 'shelf' ? (
          <ShelfLayout books={rows} onOpen={openBook} />
        ) : layout === 'grid' ? (
          <GridLayout books={rows} onOpen={openBook} />
        ) : (
          <ListLayout books={rows} onOpen={openBook} />
        )}
      </div>
    </div>
  );
}

function ShelfLayout({ books, onOpen }: { books: Book[]; onOpen: (id: string) => void }) {
  const chunks: Book[][] = [];
  const perRow = 8;
  for (let i = 0; i < books.length; i += perRow) chunks.push(books.slice(i, i + perRow));
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 42 }}>
      {chunks.map((row, ri) => (
        <div key={ri} className="shelf-row">
          <div
            style={{
              display: 'flex',
              alignItems: 'flex-end',
              gap: 20,
              minHeight: 200,
              padding: '8px 0 0',
            }}
          >
            {row.map((b) => (
              <Cover key={b.id} book={b} size="md" onClick={() => onOpen(b.id)} />
            ))}
          </div>
          <div className="shelf-plank" />
        </div>
      ))}
    </div>
  );
}

function GridLayout({ books, onOpen }: { books: Book[]; onOpen: (id: string) => void }) {
  return (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fill, minmax(150px, 1fr))',
        gap: '32px 24px',
      }}
    >
      {books.map((b) => (
        <BookCard key={b.id} book={b} onOpen={onOpen} layout="grid" />
      ))}
    </div>
  );
}

const LIST_GRID = '46px 2fr 1.2fr 80px 80px 60px';

function ListLayout({ books, onOpen }: { books: Book[]; onOpen: (id: string) => void }) {
  return (
    <div style={{ background: 'var(--color-paper-0)', border: '1px solid var(--color-rule-soft)', borderRadius: 2 }}>
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: LIST_GRID,
          gap: 16,
          padding: '10px 16px',
          borderBottom: '1px solid var(--color-rule)',
          background: 'var(--color-paper-2)',
        }}
      >
        <span />
        <span className="t-label">Title</span>
        <span className="t-label">Author</span>
        <span className="t-label">Format</span>
        <span className="t-label">Rating</span>
        <span className="t-label">Year</span>
      </div>
      {books.map((b) => (
        <BookCard key={b.id} book={b} onOpen={onOpen} layout="list" />
      ))}
    </div>
  );
}

function BookCard({ book, onOpen, layout }: { book: Book; onOpen: (id: string) => void; layout: 'grid' | 'list' }) {
  if (layout === 'list') {
    const rowStyle: CSSProperties = {
      display: 'grid',
      gridTemplateColumns: LIST_GRID,
      alignItems: 'center',
      gap: 16,
      padding: '10px 16px',
      borderBottom: '1px solid var(--color-rule-soft)',
      cursor: 'pointer',
    };
    return (
      <div
        onClick={() => onOpen(book.id)}
        style={rowStyle}
        onMouseEnter={(e) => (e.currentTarget.style.background = 'var(--color-paper-2)')}
        onMouseLeave={(e) => (e.currentTarget.style.background = '')}
      >
        <Cover book={book} size="xs" />
        <div>
          <div style={{ fontWeight: 500, fontSize: 14 }}>{book.title}</div>
          {book.series && (
            <div className="t-small" style={{ fontStyle: 'italic' }}>
              {book.series}
              {book.seriesNum ? ` #${book.seriesNum}` : ''}
            </div>
          )}
        </div>
        <div style={{ fontSize: 13, color: 'var(--color-ink-2)' }}>{book.author}</div>
        <div className="mono" style={{ fontSize: 11, color: 'var(--color-ink-3)' }}>{book.format}</div>
        <StarRating rating={book.rating} size={11} />
        <div className="mono" style={{ fontSize: 11, color: 'var(--color-ink-3)' }}>{book.year}</div>
      </div>
    );
  }
  return (
    <div
      onClick={() => onOpen(book.id)}
      style={{ display: 'flex', flexDirection: 'column', gap: 10, width: 130, cursor: 'pointer' }}
    >
      <Cover book={book} size="md" />
      <div>
        <div style={{ fontSize: 13, fontWeight: 500, lineHeight: 1.3, textWrap: 'balance' }}>{book.title}</div>
        <div className="t-small" style={{ fontSize: 12, fontStyle: 'italic' }}>{book.author}</div>
        {book.progress > 0 && book.progress < 1 && (
          <div className="progress" style={{ marginTop: 6, height: 2 }}>
            <div style={{ width: `${book.progress * 100}%` }} />
          </div>
        )}
      </div>
    </div>
  );
}

function EmptyPanel() {
  return (
    <div
      style={{
        padding: '48px 24px',
        textAlign: 'center',
        border: '1px dashed var(--color-rule)',
        borderRadius: 2,
        background: 'var(--color-paper-0)',
      }}
    >
      <div className="t-h3" style={{ marginBottom: 8 }}>Nothing to show yet.</div>
      <div className="t-small" style={{ fontStyle: 'italic' }}>
        Drop an EPUB into <span className="mono">/bookdrop</span> or register a library path in Settings.
      </div>
    </div>
  );
}

function ErrorPanel({ message }: { message: string }) {
  return (
    <div
      style={{
        padding: 16,
        border: '1px solid var(--color-accent-soft)',
        background: 'var(--color-accent-soft)',
        color: 'var(--color-accent-ink)',
        borderRadius: 2,
        fontSize: 13,
      }}
    >
      {message}
    </div>
  );
}
