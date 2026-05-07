---
paths:
  - "ui/**/*.tsx"
  - "ui/**/*.ts"
  - "ui/**/*.css"
  - "ui/src/components/**"
  - "ui/src/routes/**"
  - "ui/src/hooks/**"
  - "ui/src/styles.css"
---

# Frontend

## Project stack (use what's already here)

| Layer | This project uses |
|---|---|
| Framework | React 19 + TanStack Start / Router / Query |
| Build | Vite 8 + Bun |
| CSS | Tailwind 4 (tokens under `@theme` in `ui/src/styles.css`) |
| Primitives | shadcn/ui (in `ui/src/components/ui/`) + Radix |
| Icons | Lucide |
| Realtime | SSE via `useRealtime()` hook → invalidates TanStack Query cache |

Do not introduce competing libraries (no Mantine/Chakra/MUI, no Framer Motion unless asked, no Recharts without discussion).

## Design tokens

Tokens live in `ui/src/styles.css` under `@theme`. Never hardcode raw color or spacing values in components — extend `@theme` instead. Categories: colors (semantic, with dark mode), spacing, radius, shadows, typography, breakpoints, transitions, z-index.

## Components

- shadcn primitives go under `ui/src/components/ui/`. App components live directly in `ui/src/components/` as `PascalCase.tsx`.
- One component per file. Named exports over default.
- Routes are file-based under `ui/src/routes/`. `routeTree.gen.ts` is generated — never edit by hand.
- API calls live in `ui/src/api/` as typed functions. Wrap them with TanStack Query in components, not raw `fetch`.
- SSE-driven cache invalidation flows through `useRealtime()`; new realtime events should plug into that hook, not new ad-hoc EventSource wiring.

## Layout

- CSS Grid for 2D, Flexbox for 1D. Use `gap`, not margin hacks.
- Semantic HTML: `<header>`, `<nav>`, `<main>`, `<section>`, `<article>`, `<footer>`.
- Mobile-first. Touch targets: minimum 44x44px.

## Accessibility (non-negotiable)

- All interactive elements keyboard-accessible.
- Images: meaningful `alt`. Decorative: `alt=""`.
- Form inputs: associated `<label>` or `aria-label`.
- Contrast: 4.5:1 normal text, 3:1 large text.
- Visible focus indicators. Never `outline: none` without replacement.
- Color never the sole indicator.
- `aria-live` for dynamic content. Respect `prefers-reduced-motion` and `prefers-color-scheme`.

## Performance

- Images: `loading="lazy"` below fold, explicit `width`/`height`.
- Fonts: `font-display: swap` (Inter is loaded via `@fontsource-variable/inter`).
- Animations: `transform` and `opacity` only.
- Large lists: virtualize at 100+ items.
- Bundle size: never import a whole library for one function.
