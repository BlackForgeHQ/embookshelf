// Book cover component. Generates stylized typographic covers based on style + palette.

const PALETTES = {
  navy:   { bg: 'var(--cov-navy)',   ink: 'oklch(0.88 0.04 85)' },
  olive:  { bg: 'var(--cov-olive)',  ink: 'oklch(0.92 0.03 80)' },
  rust:   { bg: 'var(--cov-rust)',   ink: 'oklch(0.92 0.04 85)' },
  teal:   { bg: 'var(--cov-teal)',   ink: 'oklch(0.90 0.03 180)' },
  plum:   { bg: 'var(--cov-plum)',   ink: 'oklch(0.90 0.03 60)' },
  ochre:  { bg: 'var(--cov-ochre)',  ink: 'oklch(0.18 0.02 60)' },
  forest: { bg: 'var(--cov-forest)', ink: 'oklch(0.90 0.03 85)' },
  cream:  { bg: 'var(--cov-cream)',  ink: 'oklch(0.24 0.02 60)' },
  brick:  { bg: 'var(--cov-brick)',  ink: 'oklch(0.90 0.04 85)' },
  ink:    { bg: 'var(--cov-ink)',    ink: 'oklch(0.90 0.03 85)' },
};

function CoverInner({ book, size }) {
  if (book.placeholder) {
    return (
      <>
        <div className="c-top">
          <div className="c-author mono">{book.author}</div>
        </div>
        <div className="c-title">{book.title}</div>
      </>
    );
  }
  const style = book.style || 'centered-line';
  switch (style) {
    case 'centered-line':
      return (
        <>
          <div className="c-top">
            <div className="c-author">{book.author?.toUpperCase()}</div>
            <div className="c-ornament"></div>
          </div>
          <div className="c-title" style={{ textAlign: 'left' }}>{book.title}</div>
        </>
      );
    case 'minimal-top':
      return (
        <>
          <div className="c-title" style={{ fontStyle: 'italic' }}>{book.title}</div>
          <div className="c-author">{book.author}</div>
        </>
      );
    case 'stacked-serif':
      return (
        <>
          <div style={{ flex: 1 }}></div>
          <div>
            <div className="c-title" style={{ fontFamily: 'Spectral, serif', fontWeight: 600, letterSpacing: '-0.01em' }}>{book.title}</div>
            <div className="c-ornament" style={{ margin: '8px 0', width: '20%' }}></div>
            <div className="c-author">{book.author}</div>
          </div>
        </>
      );
    case 'block':
      return (
        <>
          <div className="c-author">{book.author}</div>
          <div className="c-title" style={{ fontSize: size === 'hero' ? 30 : size === 'lg' ? 22 : undefined, fontWeight: 700, textTransform: 'uppercase', lineHeight: 1, letterSpacing: '-0.01em' }}>
            {book.title}
          </div>
        </>
      );
    case 'typographic':
      return (
        <>
          <div className="c-top" style={{ flex: 1, justifyContent: 'center', alignItems: 'center', display: 'flex' }}>
            <div className="c-title" style={{ fontFamily: 'Spectral, serif', fontWeight: 300, fontStyle: 'italic', textAlign: 'center', fontSize: size === 'hero' ? 32 : size === 'lg' ? 24 : undefined }}>
              {book.title}
            </div>
          </div>
          <div className="c-author" style={{ textAlign: 'center' }}>{book.author}</div>
        </>
      );
    default:
      return null;
  }
}

const Cover = ({ book, size = 'md', onClick, style: extraStyle }) => {
  const pal = PALETTES[book.palette] || PALETTES.navy;
  const isPlaceholder = book.placeholder;
  const baseStyle = isPlaceholder ? {} : { background: pal.bg, color: pal.ink };
  return (
    <div
      className={`cover ${size} ${isPlaceholder ? 'placeholder' : ''}`}
      style={{ ...baseStyle, ...extraStyle }}
      onClick={onClick}
    >
      <CoverInner book={book} size={size} />
    </div>
  );
};

// Spine-only variant for a shelf view
const Spine = ({ book, onClick, index = 0 }) => {
  const pal = PALETTES[book.palette] || PALETTES.navy;
  const isPlaceholder = book.placeholder;
  // Vary spine widths for realism
  const widths = [26, 32, 22, 38, 28, 34, 24, 30];
  const heights = [210, 230, 200, 220, 240, 215, 205, 225];
  const w = widths[index % widths.length];
  const h = heights[index % heights.length];
  return (
    <div
      onClick={onClick}
      style={{
        width: w,
        height: h,
        background: isPlaceholder ? 'var(--paper-3)' : pal.bg,
        color: isPlaceholder ? 'var(--ink-2)' : pal.ink,
        display: 'flex',
        alignItems: 'flex-end',
        justifyContent: 'center',
        padding: '12px 4px',
        cursor: 'pointer',
        position: 'relative',
        boxShadow: 'inset 1px 0 0 oklch(0 0 0 / 0.2), inset -1px 0 0 oklch(0 0 0 / 0.15), 1px 2px 4px oklch(0.2 0.02 60 / 0.15)',
        flexShrink: 0,
        transition: 'transform 180ms ease',
      }}
      onMouseEnter={e => { e.currentTarget.style.transform = 'translateY(-6px)'; }}
      onMouseLeave={e => { e.currentTarget.style.transform = ''; }}
    >
      <div style={{
        writingMode: 'vertical-rl',
        transform: 'rotate(180deg)',
        fontSize: 10,
        fontFamily: 'Spectral, serif',
        fontWeight: 500,
        letterSpacing: '0.02em',
        textOverflow: 'ellipsis',
        overflow: 'hidden',
        whiteSpace: 'nowrap',
        maxHeight: h - 24,
      }}>
        {book.title}
      </div>
    </div>
  );
};

window.Cover = Cover;
window.Spine = Spine;
