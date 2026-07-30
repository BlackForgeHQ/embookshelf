# Audiobook generation is an admin action executed chapter by chapter

Narrating one book is roughly 550,000 characters, 120 to 180 engine calls, ten to sixty minutes of wall clock, and eight to a hundred and seventy dollars. Every decision here follows from those four numbers.

## Status

accepted (2026-07-27)

## Decisions bundled here

### 1. Admin-only, per book, and there is no bulk run

ADR-0024 gave Reading guides to every signed-in user with an admin bulk **Guide run** behind a pre-flight estimate, on the reasoning that cost must follow visibly from something a person did. That reasoning holds here; the numbers do not. A guide is about a cent. An audiobook is eight dollars on OpenAI and over a hundred on ElevenLabs. The guide run across a thousand-book library costs ten dollars; the same run here costs eight thousand to a hundred and seventy thousand.

So generation is admin-gated at the router, per book. Only the person who owns the API key can spend it. Non-admins see and play an audiobook that exists and cannot create one.

And the bulk run is not built. This is the part worth stating explicitly, because the pattern already exists and copying it would be the path of least resistance: an estimate reading "$8,240" is not a guardrail, it is a dare, and unlike guides there is no cheap default escape hatch — a self-hoster pointing at local Kokoro pays nothing, but the shipped default is a metered cloud key.

`audiobook.allowNonAdmin` and per-user allowances are additive later. Building an allowance table before the feature has shipped once means guessing at a policy nobody has needed yet.

### 2. The estimate is in money, priced by the admin

Characters and estimated audio hours are never wrong but do not answer the question the admin is asking. A hardcoded price table answers it and goes stale silently, and the dangerous direction is an underestimate — someone budgets eight dollars and gets billed eighteen because a tier changed between our release and their run.

So each engine carries a `pricePerMillionChars` in the settings row, prefilled from a catalog default and labelled as configurable. The number shown is real, the accuracy is the operator's, and a stale default is theirs to correct rather than a bug we cannot close. It costs one float per engine in a struct that already exists.

### 3. One job per chapter, on a dedicated queue

Two constraints rule out the obvious shape. River's JobRescuer reclaims jobs still `running` after about an hour, so a book-length job gets restarted mid-flight and the whole book is synthesized and billed twice. And a transient failure at call 170 of 180 must not cost eight dollars and an hour, so per-segment output has to persist regardless of granularity.

Chapter granularity resolves both. Retry costs minutes of audio and cents of spend; every chapter job finishes well inside the rescue window; `Chapter` is already domain vocabulary the UI consumes. Per-chunk jobs — around 180 per book — were rejected because they need a coordinator to detect completion and would starve BookDrop ingest and Library scan through the shared worker pool.

That pool is why generation gets its own River queue with its own worker count. Today `queue.New` registers one `default` queue at `MaxWorkers: 4`; a ten-book run monopolising it would stall ingest for a day.

Chapters are uneven — a forty-page chapter beside a two-page one — so a **segment cap of about 40,000 characters** (roughly 45 minutes of audio) splits oversized chapters into several jobs sharing one chapter title. That cap is also the fallback for an EPUB with no usable structure, which would otherwise degenerate to the single unbounded job this section exists to prevent.

Per-segment MP3s stage on local disk at `${DATA_PATH}/audiobooks/{book_id}/` until finalize, following the `coverstore` precedent for derived bytes: local filesystem, outside `storage.Storage`, not a library artifact until it is finished.

### 4. Chapters come from the spine, titles from the TOC

`ExtractEPUBText` returns one flat string and discards spine boundaries at `epub_text.go:81`, though the loop already has `item.Href` per entry. There is no `toc.ncx` or EPUB3 `nav` parser anywhere in the codebase.

Boundaries come from spine items — free at that existing fork point, and file-granular, so no fragment anchors need resolving inside XHTML. Titles come from a new nav/ncx parser, mapping each TOC entry's href (fragment stripped) back to a spine item. An EPUB packing several chapters into one file collapses to one segment; that is accepted rather than solved.

Non-prose front matter — cover, the nav document, copyright — is skipped by manifest properties, media type, and an href heuristic. Synthesizing a read-aloud table of contents is worse than useless and cheap to detect.

Only EPUB can be narrated. This is the same wall ADR-0024 hit: no PDF library exists in `go.mod`, CBZ is images, and MOBI, AZW3 and FB2 have no extractor. ADR-0024 could route around it by giving non-EPUB books a metadata-only guide; there is no equivalent here, since nobody wants a narrated blurb. So `{epub}` is a gate, not a degradation — the new term **Narratable format**, deliberately not reusing `Eligible format`, which is claimed by Send-to-Kindle and means `{epub, pdf}`. It is enforced at three points like its namesake: the UI button, the handler (415 `FORMAT_NOT_NARRATABLE`), and the queue worker.

Adding PDF was rejected. The stakes differ from ADR-0024 by three orders of magnitude: a bad guide is a bad paragraph, a bad narration is eight dollars and eight hours of a robot reading "Chapter 4 · 127" between every paragraph, and scanned PDFs yield nothing at all.

### 5. Two tables, and the segments table is the alignment map

`book_audiobooks` holds one row per book — state, engine, voice, model, `source_content_hash`, `file_id`, error. `book_audiobook_segments` holds one row per segment — sequence, chapter title, character range, staged path, duration, start offset, state, error.

Segments as a JSONB array on the parent row was rejected on concurrency: four chapter workers read-modify-writing one column lose updates, and fixing that with `SELECT FOR UPDATE` on every completion serialises the thing that was just parallelised.

The segments table doubles as the **Alignment map** ADR-0025 §3 requires — `(char_start, char_end)` against `(start_s, start_s + duration_s)` per segment, with no separate storage and no extra work. `books.chapters` is written at finalize as the denormalised playback view.

### 6. Failure keeps the paid-for work; cancel does not

When a segment exhausts River's retries, the book fails: no `files` row, nothing published, staging and every completed segment retained. Retry re-enqueues only `pending` and `failed` segments, so thirty-eight already-synthesized chapters are never billed twice. This is the entire reason §5 puts segments in a table.

Shipping a book with a silent gap, or with a spoken "chapter twelve is unavailable", were both rejected: the first hides a defect in a 500 MB immutable artifact, and the second additionally makes the alignment map lie about that range.

Cancel is a distinct state the segment workers check before each engine call — the only stop-loss on a run that may be a hundred and seventy dollars — and it sweeps staging immediately, because a user who said stop does not want the partial. Failure retains staging, because they will probably retry. Abandoned `failed` and `canceled` runs are reaped after seven days by an hourly sweeper, following `LoopMissingPurge` and `LoopOrphanedKeys`; otherwise they park gigabytes indefinitely.

### 7. Progress is a coverage count, not job state

`GET /api/v1/books/:id/audiobook` returns state plus segments-done over segments-total, and the client polls at four seconds while running and stops otherwise — the self-terminating `refetchInterval` the guide run already uses. One terminal SSE event, `audiobook.updated`, with no toast.

Per-segment SSE was rejected. It emits forty events per book for real-time-ness nobody needs on a job measured in tens of minutes, and every event costs an edit in both `internal/sse/events.go` and `ui/src/api/realtime.ts` or two Go tests fail. The one thing a user genuinely needs during an hour-long job is reload survival, and a coverage count over persisted rows gives that for free where job state does not.

### 8. Text normalization stays minimal

Strip markup, preserve paragraph boundaries as blank lines (every engine pauses on them naturally), drop footnote references and footnote bodies, drop image alt text, collapse whitespace. Numbers, dates and abbreviations are left to each engine's own normalization, which is the state of the art we are already paying for.

A richer rule set — abbreviation dictionaries, roman numeral expansion, SSML pauses — was rejected on two grounds. SSML is not in the common denominator: OpenAI's speech endpoint has no SSML at all and ElevenLabs is partial, so SSML-based rules would land on Azure alone and break the interface parity ADR-0026 exists to provide. And a wrong expansion rule is baked into eight hours of audio, where duplicating engine normalization badly is the main way this feature produces embarrassing output.

An LLM cleanup pass was rejected outright: it doubles cost, adds hours of latency, and hallucination across 550,000 characters means the narrator confidently reads sentences the author never wrote.

Chunk splits land on sentence boundaries, never mid-sentence. A mid-sentence split produces an audible glitch at every one of roughly 180 seams and is the most noticeable artifact of chunked synthesis.

Following `llm.Client`'s deliberate omission, there is no HTTP-level retry decorator. Retry belongs to the queue, at segment granularity, where its cost is visible.

## Consequences

- **Amended by ADR-0031.** A run now carries a **generation**, bumped on every start and carried in the segment job args, because `(book, seq)` addresses the plan a regeneration deleted and the plan that replaced it identically. Both writes that touch a segment refuse a mismatch, staging is scoped by generation, and — new here — a start over a run that has not concluded is refused rather than clobbering it, which makes cancel the way through and §6's stop-loss the only route past a live run.

## Open questions

- Whether a bulk run ever arrives, and if so what would make an eight-thousand-dollar estimate safe — a spend ceiling, a dry run, a required local engine.
- Per-user allowances for shared instances, and whether they are worth a table.
- The phase-2 cross-rendition sync UX: how a text-reader locator translates to a character offset, and what happens to the alignment map when the source EPUB is replaced.
- Whether a partially failed run should be playable as far as it got, rather than withheld entirely.

## Companion artifacts

- `CONTEXT.md` — Audiobook run, Segment, Alignment map, Staging area, Narratable format.
- ADR-0025 — what the finished artifact becomes.
- ADR-0026 — the engines this pipeline calls.
- ADR-0027 — the concatenation and tagging performed at finalize.
- ADR-0021 — `Eligible format`, the three-point gating pattern §4 copies and the term it deliberately does not reuse.
- ADR-0024 — the cost-follows-action principle §1 inherits and the bulk-run pattern §1 declines.
- ADR-0031 — the run generation, and the start-over-a-live-run refusal.
