import { useMemo, useState, type CSSProperties } from 'react';
import { createFileRoute, useNavigate } from '@tanstack/react-router';

import { Cover, StarRating } from '@/components/Cover';
import { Icon } from '@/components/Icon';
import { TopBar } from '@/components/TopBar';
import { BOOKS, type Book, type BookFormat } from '@/data/mock';

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

function LibraryView() {
  const navigate = useNavigate();
  const { shelf: activeShelf, layout: layoutSearch } = Route.useSearch();
  const layout: Layout = layoutSearch ?? 'grid';

  const [search, setSearch] = useState('');
  const [sortBy, setSortBy] = useState<SortKey>('added');
  const [filterFormat, setFilterFormat] = useState<BookFormat | null>(null);

  const filtered = useMemo(() => {
    let list = [...BOOKS];
    if (activeShelf === 'reading')  list = list.filter((b) => b.shelf?.includes('Reading Now'));
    else if (activeShelf === 'new')      list = list.filter((b) => b.shelf?.includes('New'));
    else if (activeShelf === 'finished') list = list.filter((b) => b.shelf?.includes('Finished'));
    else if (activeShelf === 'tofinish') list = list.filter((b) => b.shelf?.includes('To Finish'));
    if (search) {
      const q = search.toLowerCase();
      list = list.filter(
        (b) =>
          b.title.toLowerCase().includes(q) || b.author.toLowerCase().includes(q),
      );
    }
    if (filterFormat) list = list.filter((b) => b.format === filterFormat);
    if (sortBy === 'title') list.sort((a, b) => a.title.localeCompare(b.title));
    else if (sortBy === 'author') list.sort((a, b) => a.author.localeCompare(b.author));
    else if (sortBy === 'rating') list.sort((a, b) => b.rating - a.rating);
    else list.sort((a, b) => (b.addedAt ?? '').localeCompare(a.addedAt ?? ''));
    return list;
  }, [search, sortBy, filterFormat, activeShelf]);

  const shelfTitle =
    activeShelf === 'reading' ? 'Reading Now'
      : activeShelf === 'new' ? 'Recently Added'
      : activeShelf === 'finished' ? 'Finished'
      : activeShelf === 'tofinish' ? 'To Finish'
      : 'All Books';

  const subtitle =
    activeShelf === 'reading' ? 'Books with a ribbon still in them.'
      : activeShelf === 'finished' ? 'Shelved, loved, occasionally revisited.'
      : 'Your complete collection across all libraries.';

  const setLayout = (next: Layout) => {
    void navigate({ to: '/library', search: (prev) => ({ ...prev, layout: next }) });
  };

  const openBook = (id: string) => void navigate({ to: '/book/$id', params: { id } });

  const layoutBtn = (name: Layout, label: string) => (
    <button
      onClick={() => setLayout(name)}
      className={`btn small ${layout === name ? 'primary' : ''}`}
      style={{ borderRadius: 2 }}
      title={label}
    >
      <Icon name={name === 'shelf' ? 'shelf' : name === 'grid' ? 'grid' : 'list'} size={13} />
    </button>
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
            onClick={() => setFilterFormat(f as BookFormat | null)}
            style={{ cursor: 'pointer', border: 'none' }}
          >
            {f ?? 'All formats'}
          </button>
        ))}
        <div style={{ flex: 1 }} />
        <span className="t-label">Sort</span>
        <select
          value={sortBy}
          onChange={(e) => setSortBy(e.target.value as SortKey)}
          className="input"
          style={{ width: 'auto', padding: '5px 10px', fontSize: 12.5, background: 'var(--color-paper-0)' }}
        >
          <option value="added">Recently added</option>
          <option value="title">Title</option>
          <option value="author">Author</option>
          <option value="rating">Rating</option>
        </select>
        <span className="mono" style={{ fontSize: 11, color: 'var(--color-ink-3)', marginLeft: 8 }}>
          {filtered.length} volumes
        </span>
      </div>

      {/* Content */}
      <div style={{ padding: '28px 32px 80px', flex: 1 }}>
        {layout === 'shelf' && <ShelfLayout books={filtered} onOpen={openBook} />}
        {layout === 'grid' && <GridLayout books={filtered} onOpen={openBook} />}
        {layout === 'list' && <ListLayout books={filtered} onOpen={openBook} />}
      </div>
    </div>
  );
}

function ShelfLayout({ books, onOpen }: { books: Book[]; onOpen: (id: string) => void }) {
  const rows: Book[][] = [];
  const perRow = 8;
  for (let i = 0; i < books.length; i += perRow) rows.push(books.slice(i, i + perRow));
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 42 }}>
      {rows.map((row, ri) => (
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

const LIST_GRID = '46px 2fr 1.2fr 80px 90px 80px 60px';

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
        <span className="t-label">Pages</span>
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
              {book.series} #{book.seriesNum}
            </div>
          )}
        </div>
        <div style={{ fontSize: 13, color: 'var(--color-ink-2)' }}>{book.author}</div>
        <div className="mono" style={{ fontSize: 11, color: 'var(--color-ink-3)' }}>{book.format}</div>
        <div className="mono" style={{ fontSize: 11, color: 'var(--color-ink-3)' }}>{book.pages} pp</div>
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
        {book.progress != null && book.progress > 0 && book.progress < 1 && (
          <div className="progress" style={{ marginTop: 6, height: 2 }}>
            <div style={{ width: `${book.progress * 100}%` }} />
          </div>
        )}
      </div>
    </div>
  );
}
