import type { CSSProperties, MouseEventHandler } from 'react';

import type { Book, CoverPalette } from '@/data/mock';
import { Icon } from './Icon';

type CoverSize = 'xs' | 'sm' | 'md' | 'lg' | 'hero';

const PALETTES: Record<CoverPalette, { bg: string; ink: string }> = {
  navy:   { bg: 'var(--color-cov-navy)',   ink: 'oklch(0.88 0.04 85)' },
  olive:  { bg: 'var(--color-cov-olive)',  ink: 'oklch(0.92 0.03 80)' },
  rust:   { bg: 'var(--color-cov-rust)',   ink: 'oklch(0.92 0.04 85)' },
  teal:   { bg: 'var(--color-cov-teal)',   ink: 'oklch(0.90 0.03 180)' },
  plum:   { bg: 'var(--color-cov-plum)',   ink: 'oklch(0.90 0.03 60)' },
  ochre:  { bg: 'var(--color-cov-ochre)',  ink: 'oklch(0.18 0.02 60)' },
  forest: { bg: 'var(--color-cov-forest)', ink: 'oklch(0.90 0.03 85)' },
  cream:  { bg: 'var(--color-cov-cream)',  ink: 'oklch(0.24 0.02 60)' },
  brick:  { bg: 'var(--color-cov-brick)',  ink: 'oklch(0.90 0.04 85)' },
  ink:    { bg: 'var(--color-cov-ink)',    ink: 'oklch(0.90 0.03 85)' },
};

function CoverInner({ book, size }: { book: Book; size: CoverSize }) {
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
            <div className="c-ornament" />
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
          <div style={{ flex: 1 }} />
          <div>
            <div className="c-title" style={{ fontFamily: 'var(--font-serif)', fontWeight: 600, letterSpacing: '-0.01em' }}>{book.title}</div>
            <div className="c-ornament" style={{ margin: '8px 0', width: '20%' }} />
            <div className="c-author">{book.author}</div>
          </div>
        </>
      );
    case 'block': {
      const blockFontSize = size === 'hero' ? 30 : size === 'lg' ? 22 : undefined;
      return (
        <>
          <div className="c-author">{book.author}</div>
          <div
            className="c-title"
            style={{
              fontSize: blockFontSize,
              fontWeight: 700,
              textTransform: 'uppercase',
              lineHeight: 1,
              letterSpacing: '-0.01em',
            }}
          >
            {book.title}
          </div>
        </>
      );
    }
    case 'typographic': {
      const typoSize = size === 'hero' ? 32 : size === 'lg' ? 24 : undefined;
      return (
        <>
          <div className="c-top" style={{ flex: 1, justifyContent: 'center', alignItems: 'center', display: 'flex' }}>
            <div
              className="c-title"
              style={{
                fontFamily: 'var(--font-serif)',
                fontWeight: 300,
                fontStyle: 'italic',
                textAlign: 'center',
                fontSize: typoSize,
              }}
            >
              {book.title}
            </div>
          </div>
          <div className="c-author" style={{ textAlign: 'center' }}>{book.author}</div>
        </>
      );
    }
    default:
      return null;
  }
}

type CoverProps = {
  book: Book;
  size?: CoverSize;
  onClick?: MouseEventHandler<HTMLDivElement>;
  style?: CSSProperties;
};

export function Cover({ book, size = 'md', onClick, style }: CoverProps) {
  const palette = PALETTES[book.palette ?? 'navy'];
  const isPlaceholder = Boolean(book.placeholder);
  const baseStyle: CSSProperties = isPlaceholder
    ? {}
    : { background: palette.bg, color: palette.ink };
  return (
    <div
      className={`cover ${size} ${isPlaceholder ? 'placeholder' : ''}`}
      style={{ ...baseStyle, ...style }}
      onClick={onClick}
    >
      <CoverInner book={book} size={size} />
    </div>
  );
}

const SPINE_WIDTHS = [26, 32, 22, 38, 28, 34, 24, 30];
const SPINE_HEIGHTS = [210, 230, 200, 220, 240, 215, 205, 225];

type SpineProps = {
  book: Book;
  index?: number;
  onClick?: MouseEventHandler<HTMLDivElement>;
};

export function Spine({ book, index = 0, onClick }: SpineProps) {
  const palette = PALETTES[book.palette ?? 'navy'];
  const isPlaceholder = Boolean(book.placeholder);
  const w = SPINE_WIDTHS[index % SPINE_WIDTHS.length];
  const h = SPINE_HEIGHTS[index % SPINE_HEIGHTS.length];
  return (
    <div
      onClick={onClick}
      style={{
        width: w,
        height: h,
        background: isPlaceholder ? 'var(--color-paper-3)' : palette.bg,
        color: isPlaceholder ? 'var(--color-ink-2)' : palette.ink,
        display: 'flex',
        alignItems: 'flex-end',
        justifyContent: 'center',
        padding: '12px 4px',
        cursor: 'pointer',
        position: 'relative',
        boxShadow:
          'inset 1px 0 0 oklch(0 0 0 / 0.2), inset -1px 0 0 oklch(0 0 0 / 0.15), 1px 2px 4px oklch(0.2 0.02 60 / 0.15)',
        flexShrink: 0,
        transition: 'transform 180ms ease',
      }}
      onMouseEnter={(e) => { e.currentTarget.style.transform = 'translateY(-6px)'; }}
      onMouseLeave={(e) => { e.currentTarget.style.transform = ''; }}
    >
      <div
        style={{
          writingMode: 'vertical-rl',
          transform: 'rotate(180deg)',
          fontSize: 10,
          fontFamily: 'var(--font-serif)',
          fontWeight: 500,
          letterSpacing: '0.02em',
          textOverflow: 'ellipsis',
          overflow: 'hidden',
          whiteSpace: 'nowrap',
          maxHeight: h - 24,
        }}
      >
        {book.title}
      </div>
    </div>
  );
}

type StarRatingProps = {
  rating: number;
  size?: number;
};

export function StarRating({ rating, size = 13 }: StarRatingProps) {
  const full = Math.floor(rating);
  const fractional = rating - full;
  const half = fractional >= 0.3 && fractional <= 0.7;
  return (
    <div style={{ display: 'flex', gap: 1, color: 'var(--color-accent)' }}>
      {[0, 1, 2, 3, 4].map((i) => {
        const name = i < full
          ? 'star-filled'
          : i === full && half
            ? 'star-half'
            : 'star';
        return <Icon key={i} name={name} size={size} />;
      })}
    </div>
  );
}
