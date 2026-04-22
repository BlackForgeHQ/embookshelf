import type { CSSProperties, SVGProps } from 'react';

export type IconName =
  | 'book' | 'book-open' | 'home' | 'search' | 'sparkle' | 'check' | 'flag'
  | 'folder' | 'plus' | 'settings' | 'user' | 'grid' | 'list' | 'shelf'
  | 'chevron-right' | 'chevron-left' | 'chevron-down' | 'chevron-up'
  | 'arrow-left' | 'arrow-right' | 'close' | 'filter' | 'sort' | 'edit'
  | 'upload' | 'download' | 'bookmark' | 'highlight' | 'note' | 'contents'
  | 'star' | 'star-half' | 'star-filled' | 'circle' | 'dot' | 'clock'
  | 'calendar' | 'device' | 'bell' | 'menu' | 'more' | 'aA' | 'chart'
  | 'hash' | 'tune' | 'refresh' | 'x-circle' | 'check-circle' | 'alert'
  | 'library';

type Props = {
  name: IconName;
  size?: number;
  className?: string;
  style?: CSSProperties;
  title?: string;
};

// Minimal icon set — stroke 1.5, literary / editorial feel. No emoji.
export function Icon({ name, size = 16, className, style, title }: Props) {
  const svgProps: SVGProps<SVGSVGElement> = {
    width: size,
    height: size,
    viewBox: '0 0 24 24',
    fill: 'none',
    stroke: 'currentColor',
    strokeWidth: 1.5,
    strokeLinecap: 'round',
    strokeLinejoin: 'round',
    className,
    style,
  };
  return (
    <svg {...svgProps}>
      {title && <title>{title}</title>}
      {renderPath(name)}
    </svg>
  );
}

function renderPath(name: IconName) {
  switch (name) {
    case 'book':
      return <path d="M4 4h11a3 3 0 013 3v13H7a3 3 0 01-3-3V4zm0 13h14M4 4a1 1 0 00-1 1v12a3 3 0 003 3" />;
    case 'book-open':
      return (
        <>
          <path d="M3 5h7a2 2 0 012 2v13l-1-1H5a2 2 0 01-2-2V5z" />
          <path d="M21 5h-7a2 2 0 00-2 2v13l1-1h6a2 2 0 002-2V5z" />
        </>
      );
    case 'home':
      return <path d="M3 10l9-7 9 7v10a1 1 0 01-1 1h-5v-7h-6v7H4a1 1 0 01-1-1V10z" />;
    case 'search':
      return (
        <>
          <circle cx="11" cy="11" r="7" />
          <path d="M16 16l5 5" />
        </>
      );
    case 'sparkle':
      return <path d="M12 3v6M12 15v6M3 12h6M15 12h6M6 6l4 4M14 14l4 4M18 6l-4 4M10 14l-4 4" />;
    case 'check':
      return <path d="M4 12l5 5L20 6" />;
    case 'flag':
      return (
        <>
          <path d="M5 3v18" />
          <path d="M5 4h13l-3 4 3 4H5" />
        </>
      );
    case 'folder':
      return <path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v9a2 2 0 01-2 2H5a2 2 0 01-2-2V7z" />;
    case 'plus':
      return <path d="M12 5v14M5 12h14" />;
    case 'settings':
      return (
        <>
          <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1Z" />
          <circle cx="12" cy="12" r="3" />
        </>
      );
    case 'user':
      return (
        <>
          <circle cx="12" cy="8" r="4" />
          <path d="M4 21c0-4 4-7 8-7s8 3 8 7" />
        </>
      );
    case 'grid':
      return (
        <>
          <rect x="3" y="3" width="7" height="7" />
          <rect x="14" y="3" width="7" height="7" />
          <rect x="3" y="14" width="7" height="7" />
          <rect x="14" y="14" width="7" height="7" />
        </>
      );
    case 'list':
      return <path d="M3 6h18M3 12h18M3 18h18" />;
    case 'shelf':
      return (
        <>
          <path d="M3 7h18M3 17h18" />
          <path d="M6 7v10M10 7v10M14 7v10M18 7v10" />
        </>
      );
    case 'chevron-right':
      return <path d="M9 6l6 6-6 6" />;
    case 'chevron-left':
      return <path d="M15 6l-6 6 6 6" />;
    case 'chevron-down':
      return <path d="M6 9l6 6 6-6" />;
    case 'chevron-up':
      return <path d="M6 15l6-6 6 6" />;
    case 'arrow-left':
      return <path d="M20 12H4M10 6l-6 6 6 6" />;
    case 'arrow-right':
      return <path d="M4 12h16M14 6l6 6-6 6" />;
    case 'close':
      return <path d="M5 5l14 14M19 5L5 19" />;
    case 'filter':
      return <path d="M3 5h18l-7 8v6l-4-2v-4L3 5z" />;
    case 'sort':
      return <path d="M8 4v16M8 4l-3 3M8 4l3 3M16 20V4M16 20l-3-3M16 20l3-3" />;
    case 'edit':
      return (
        <>
          <path d="M4 20h4l10-10-4-4L4 16v4z" />
          <path d="M14 6l4 4" />
        </>
      );
    case 'upload':
      return (
        <>
          <path d="M12 4v12" />
          <path d="M6 10l6-6 6 6" />
          <path d="M4 20h16" />
        </>
      );
    case 'download':
      return (
        <>
          <path d="M12 4v12" />
          <path d="M6 14l6 6 6-6" />
          <path d="M4 4h16" />
        </>
      );
    case 'bookmark':
      return <path d="M6 3h12v18l-6-4-6 4V3z" />;
    case 'highlight':
      return (
        <>
          <rect x="4" y="12" width="16" height="8" />
          <path d="M8 12V6a2 2 0 012-2h4a2 2 0 012 2v6" />
        </>
      );
    case 'note':
      return (
        <>
          <rect x="4" y="4" width="16" height="16" />
          <path d="M8 9h8M8 13h8M8 17h5" />
        </>
      );
    case 'contents':
      return (
        <>
          <path d="M4 6h6M4 12h10M4 18h8" />
          <circle cx="18" cy="6" r="1" fill="currentColor" stroke="none" />
          <circle cx="18" cy="12" r="1" fill="currentColor" stroke="none" />
          <circle cx="18" cy="18" r="1" fill="currentColor" stroke="none" />
        </>
      );
    case 'star':
      return <path d="M12 3l2.6 5.9 6.4.6-4.8 4.4 1.4 6.3L12 17l-5.6 3.2 1.4-6.3L3 9.5l6.4-.6L12 3z" />;
    case 'star-half':
      return (
        <>
          <path d="M12 3v14l-5.6 3.2 1.4-6.3L3 9.5l6.4-.6L12 3z" fill="currentColor" stroke="none" />
          <path d="M12 3l2.6 5.9 6.4.6-4.8 4.4 1.4 6.3L12 17" />
        </>
      );
    case 'star-filled':
      return <path d="M12 3l2.6 5.9 6.4.6-4.8 4.4 1.4 6.3L12 17l-5.6 3.2 1.4-6.3L3 9.5l6.4-.6L12 3z" fill="currentColor" />;
    case 'circle':
      return <circle cx="12" cy="12" r="9" />;
    case 'dot':
      return <circle cx="12" cy="12" r="3" fill="currentColor" stroke="none" />;
    case 'clock':
      return (
        <>
          <circle cx="12" cy="12" r="9" />
          <path d="M12 7v5l3 2" />
        </>
      );
    case 'calendar':
      return (
        <>
          <rect x="3" y="5" width="18" height="16" rx="1" />
          <path d="M3 10h18M8 3v4M16 3v4" />
        </>
      );
    case 'device':
      return (
        <>
          <rect x="4" y="3" width="16" height="18" rx="2" />
          <path d="M10 18h4" />
        </>
      );
    case 'bell':
      return <path d="M6 9a6 6 0 1112 0c0 5 2 7 2 7H4s2-2 2-7zM10 20a2 2 0 004 0" />;
    case 'menu':
      return <path d="M3 6h18M3 12h18M3 18h18" />;
    case 'more':
      return (
        <>
          <circle cx="5" cy="12" r="1" fill="currentColor" stroke="none" />
          <circle cx="12" cy="12" r="1" fill="currentColor" stroke="none" />
          <circle cx="19" cy="12" r="1" fill="currentColor" stroke="none" />
        </>
      );
    case 'aA':
      return (
        <>
          <text x="3" y="17" fontFamily="Source Serif 4, Georgia, serif" fontSize="14">A</text>
          <text x="13" y="17" fontFamily="Source Serif 4, Georgia, serif" fontSize="10">a</text>
        </>
      );
    case 'chart':
      return (
        <>
          <path d="M3 20h18" />
          <path d="M6 16v-4M10 16v-7M14 16v-3M18 16v-9" />
        </>
      );
    case 'hash':
      return <path d="M5 9h14M5 15h14M10 3l-2 18M16 3l-2 18" />;
    case 'tune':
      return (
        <>
          <path d="M4 6h12M4 12h6M4 18h10" />
          <circle cx="18" cy="6" r="2" />
          <circle cx="13" cy="12" r="2" />
          <circle cx="16" cy="18" r="2" />
        </>
      );
    case 'refresh':
      return <path d="M4 12a8 8 0 0114-5l2-2v6h-6l2-2a6 6 0 10.5 6.5" />;
    case 'x-circle':
      return (
        <>
          <circle cx="12" cy="12" r="9" />
          <path d="M9 9l6 6M15 9l-6 6" />
        </>
      );
    case 'check-circle':
      return (
        <>
          <circle cx="12" cy="12" r="9" />
          <path d="M8 12l3 3 5-6" />
        </>
      );
    case 'alert':
      return (
        <>
          <path d="M12 3l10 18H2L12 3z" />
          <path d="M12 10v5M12 18v.5" />
        </>
      );
    case 'library':
      return <path d="M4 4v16M8 4v16M12 4v16M16 4v16M20 4v16M4 20h16" />;
    default:
      return null;
  }
}
