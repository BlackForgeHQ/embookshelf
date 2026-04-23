import { cn } from '@/lib/utils';

// SHELF_ACCENTS mirrors internal/model/book.go ShelfAccents — keep the two
// in sync. The first value ("accent") maps to the editorial burgundy; the
// rest reuse the cover palette so shelves and book covers share one vocabulary.
export const SHELF_ACCENTS = [
  'accent',
  'teal',
  'olive',
  'rust',
  'plum',
  'ochre',
  'forest',
  'brick',
] as const;

export type ShelfAccent = (typeof SHELF_ACCENTS)[number];

// ACCENT_COLORS resolves an accent id to a CSS color (oklch from styles.css).
// Returns the editorial burgundy for unknown values so stale records still
// render something, rather than a transparent dot.
const ACCENT_TOKENS: Record<ShelfAccent, string> = {
  accent: 'var(--color-editorial-accent)',
  teal: 'var(--color-cov-teal)',
  olive: 'var(--color-cov-olive)',
  rust: 'var(--color-cov-rust)',
  plum: 'var(--color-cov-plum)',
  ochre: 'var(--color-cov-ochre)',
  forest: 'var(--color-cov-forest)',
  brick: 'var(--color-cov-brick)',
};

export function accentColor(accent: string | null | undefined): string {
  if (accent && accent in ACCENT_TOKENS) {
    return ACCENT_TOKENS[accent as ShelfAccent];
  }
  return ACCENT_TOKENS.accent;
}

type Props = {
  value: ShelfAccent;
  onChange: (next: ShelfAccent) => void;
  className?: string;
};

// AccentPicker is a compact 8-swatch radio group. Each swatch is a real
// <button> so keyboard tab-order and focus rings come for free from the
// browser; the selected state is signaled with a ring instead of a check,
// to keep the control quiet at sidebar density.
export function AccentPicker({ value, onChange, className }: Props) {
  return (
    <div
      role="radiogroup"
      aria-label="Accent color"
      className={cn('flex flex-wrap gap-1.5', className)}
    >
      {SHELF_ACCENTS.map((a) => {
        const selected = a === value;
        return (
          <button
            key={a}
            type="button"
            role="radio"
            aria-checked={selected}
            aria-label={a}
            title={a}
            onClick={() => onChange(a)}
            className={cn(
              'relative size-5 shrink-0 rounded-full transition-transform',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-(--color-ink-3) focus-visible:ring-offset-2 focus-visible:ring-offset-(--color-paper-0)',
              selected
                ? 'ring-2 ring-(--color-ink-1) ring-offset-2 ring-offset-(--color-paper-0) scale-110'
                : 'hover:scale-110',
            )}
            style={{ background: ACCENT_TOKENS[a] }}
          />
        );
      })}
    </div>
  );
}
