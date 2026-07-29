# The reader gets shared chrome, not a shared shell

The reader route is roughly 1,500 lines holding sibling shells that restate the same chrome around different renderers. The question this ADR settles is whether one shell module can own what does not vary by **Rendition** — position, progress, chrome, annotations — with each format contributing only a surface, or whether the honest seam is smaller than that.

## Status

accepted (2026-07-29)

## What the code actually looks like

The framing this started from said there are four sibling shells and that the same progress bar appears five times in the file plus twice elsewhere. Both are wrong in ways that change the answer, so they are recorded here rather than quietly corrected.

**There are three shells, and two formats already share one.** `ReaderShell` drives EPUB *and* PDF. That merge is the most useful evidence available, because it is the experiment this ADR would otherwise be proposing to repeat at a larger scale. The result is a 651-line component in which 8 of its 17 hooks serve exactly one format, sixteen render sites branch on `book.format`, and two imperative refs are allocated on every mount so that one of them can be `null` for the lifetime of the component:

```tsx
onClick={() => book.format === "EPUB" ? epubRef.current?.prev() : pdfRef.current?.prev()}
```

**The bar is duplicated ten times, and not where the framing said.** Three in the reader route — two of the claimed five are text labels, not bars — and seven elsewhere, in three unrelated idioms: the `.progress` class in `styles.css`, an inline `--color-rule-soft` variant, and Tailwind arbitrary values. `styles.css` also carries a `.reader-progress` rule set, written for this exact component at exactly its dimensions, referenced by nothing. That is an app-wide design-token problem. It can be fixed without any shell moving, and bundling it into a shell restructure conflates a primitive extraction with a control-flow one.

## Decisions bundled here

### 1. One shell per interaction model, not one shell per Rendition

A single shell needs a content slot, and the three renderers give three different answers to what goes in it.

`AudioReader` mounts *outside* the layout as a sibling with `display: none`; the audio shell's content area is entirely chrome — cover, transport, scrubber, chapter drawer. `ComicReader` renders its own full-height click zones and binds a window-level `keydown` handler. `EpubReader` renders into an iframe, which swallows the shell's own chrome-restore click so that only the letterbox margins respond to it.

Audio is not a different renderer, it is a different control topology. Position is continuous rather than discrete, which is why its debounce is 5000ms against the page shells' 600 and why it needs a flush on pause that they have no analogue for. More decisively, it has an input source none of the others has: `mediaSession` handlers mean a lock-screen or headphone pause drives the reader from outside the page, so play state is owned by the media element and never by whoever pressed the button. A shell abstracting over "the user advanced the position" has no home for that.

The rejected shape is the one the issue proposed: one shell owning position, progress, chrome and annotations, with each format contributing a surface. Rejected because the two-format version of it already exists and reads worse than what surrounds it, and because making it cover audio requires special-casing audio out of the shell's own content slot — an abstraction with a hole shaped exactly like its hardest case.

### 2. What the shell owns versus what a surface contributes

What genuinely does not vary is chrome and the position *contract*. What varies is everything with a lifecycle.

Shared, extractable today, no lifecycle: the fullscreen container, the header wrapper, the exit button, the bookmark button, the footer bar, the floating chrome-restore button, and the progress bar — each currently spelled two or three times, character-identical but for a background token.

Not shared, and the attempt to share them is what produced the 651-line component: renderer props (no two renderers agree beyond `onReady`/`onProgress`/`onError`), imperative handles (`goTo(href: string)` for EPUB against `goTo(page: number)` for PDF and comic, and audio's `play`/`seekTo`/`skip` overlapping neither), and restore timing (three incompatible answers to when it is safe to seek — after an async boot, after a `requestAnimationFrame` following a layout pass, and inside `onLoadedMetadata`).

The position contract is already extracted and already correct: `useReadingPosition` owns queue/flush/unmount, and `lib/locator.ts` owns the encoding both ends must agree on. Neither needs a shell to hold it. This is the part of the original framing that survives — it survives by having been done already.

### 3. The EPUB/PDF shell is split, not deepened

`ReaderShell` becomes a text shell and a PDF shell. This is the one place this ADR asks for *less* sharing than exists today, and it follows from §1: if the reason not to merge audio is that merging costs a format branch at every site, then the existing merge is already paying that cost sixteen times over.

The two shells then compose the same chrome pieces, which is what §2 makes cheap. Annotations stay with the text shell, where the highlight memo has to sit next to the query that feeds it — moving the query up into shared chrome and leaving the memo below would reintroduce the add/remove churn that memo exists to prevent.

### 4. The cross-Rendition progress bridge attaches at the locator layer

ADR-0025 §3 says reading and listening share one progress value, bridged by the **Alignment map**, and defers the sync itself. Where that bridge lands is a question about the *currency* positions are written in, not about which component writes them — so it attaches below every shell, at `lib/locator.ts` and the endpoint that feeds it.

Today `Locator` has four kinds — `cfi`, `page`, `time`, `unknown` — and all of them are written into one `resumeCfi` column. The bridge adds a fifth: a character offset, which is the one currency both renditions can be converted to, because `book_audiobook_segments` already holds `(char_start, char_end) ↔ (start_ms, duration_ms)` for every segment. The client cannot see any of it: the columns are on no DTO, there is no endpoint, and `grep` for the alignment map across `ui/src` returns nothing. So the work is a mapping endpoint over the segment rows plus a locator kind, after which a shell writes its native kind and the bridge converts — and no shell needs to know the other exists.

Recording this now because the seam in §1 must not foreclose it, and it does not: chrome pieces do not participate in position at all.

## Consequences

The immediate defects this analysis surfaced are not duplication and would not have been fixed by merging shells. They are filed and fixed on their own merits:

- The footer bar sits outside the `chromeVisible` guard in two shells, so "hide chrome" hides only the header.
- Nothing flushes progress on `visibilitychange` or `pagehide`, though `useReadingPosition` documents backgrounding the tab as one of the three exits a caller must flush on.
- Four locator kinds share one column, so in `NarratableShell` the same book is opened by two shells writing incompatible kinds — Read → Listen → Read loses the reader's place and restarts the book (#200).

## Slices

Each lands and is verifiable alone, in this order:

1. **One progress bar component**, replacing three in the reader and seven elsewhere, adopting the orphaned `.reader-progress` rules. No shell moves. Verified by the existing e2e reader specs plus a unit test.
2. **Chrome pieces** — container, header, exit, bookmark, footer, restore — composed by all three shells. Still no shell moves; each drops its copy.
3. **Split `ReaderShell`** into text and PDF shells (this is #179's "shell split"), each composing §2's pieces, with no format ternaries inside either.
4. **The Rendition module** (#179): one module answering which Renditions a book has and which is selected, with the route dispatching on it once. Table tests, no DOM.
5. **The locator bridge** (§4), once someone wants cross-rendition sync — a mapping endpoint over the segment rows and a fifth locator kind.

## Companion artifacts

- `docs/adr/0025-audiobook-as-rendition.md` — Rendition, and the deferred progress bridge.
- `CONTEXT.md` — Rendition, Alignment map.
- Issues: #199 (this decision), #179 (slices 3–4), #200 (the locator collision).
