# A generated audiobook is a rendition of the Book, not a new Book

embookshelf can narrate a book it already holds: an admin points a text-to-speech engine at an EPUB and gets back an MP3 that sits in the same library folder as the source. The question this ADR settles is what that MP3 *is* — a second Book, a derived cache like a cover, or another file belonging to the Book that was already there.

## Status

accepted (2026-07-27)

## Decisions bundled here

### 1. The generated audio is a `files` row on the same Book

A `books` row is the logical work; physical artifacts already hang off it as `files` rows. Narrating a book does not produce a different work, so it does not produce a different Book. The generated MP3 is placed by `service.Placer` into the existing LeafBook at `{Author}/{Title}/{Title}.mp3`, gets a `content_hash` like every other file, and is walked by Library scan like every other file.

The alternative — a derived-asset store outside the library, the way `coverstore` holds cover bytes under `${DATA_PATH}/covers/` — was the closer call, and it is what ADR-0024 chose for Reading guides. It was rejected because the artifact is different in kind. A guide is 2 KB of commentary that only means anything inside embookshelf. An audiobook is the thing the user wanted: they will download it, put it on a phone, and expect it to still be there after a rescan. Making it a library file is what delivers that, and it is what justifies writing chapter marks into the file itself (ADR-0027) rather than only into the database.

The cost is that machinery built for *ingested* files now points at a file we synthesized. That is acceptable because the file is genuinely a normal MP3 — nothing downstream needs to know where it came from.

### 2. Provenance is a pointer, not a flag

`book_audiobooks.file_id` names the generated `files` row. Every other row is ingested. No `is_generated` column on `files`, no change to `internal/scan`, no new branch in the differ.

`book_audiobooks.source_content_hash` records the EPUB the narration was made from. When it no longer matches that book's current EPUB, the UI says the audiobook was generated from an older copy rather than quietly implying it is current. Nothing is invalidated automatically — see §4.

### 3. `books.format` stays the primary format; the reader dispatches on a selected rendition

`books.format` is a denormalised cache of the *primary* format, and `EPUB` outranks `MP3` in the priority chain, so a narrated EPUB stays `format = EPUB`. Flipping it to `MP3` would have made everything downstream work with no code change — `service.primaryFile` would return the MP3, `read.$id.tsx` would open `AudioReader`, the library card would show an audiobook. It was rejected because it is destructive: the book stops being readable as text, the in-file metadata embed retargets to a format with no embed path, and Send-to-Kindle's eligible-format gate turns the button off. Generating audio must not take the book away.

So `book.format` stops being the reader's dispatch key. The reader dispatches on a **Rendition** — which way of consuming this book the user picked — and the book page offers Listen when an audio `files` row exists. This is a real conceptual addition and it is the main thing a future reader will trip over: several call sites branch on `book.format` today and are, after this, answering a subtly different question than the one they think they are.

Reading and listening share one progress value. Because embookshelf generated the audio from the text, the character-offset-to-seconds map is a byproduct of generation rather than an alignment problem to solve; it is persisted from the first version even though the cross-rendition sync itself is deferred. Regenerating a 500 MB audiobook later purely to recover data we already had would be absurd.

### 4. One audiobook per Book, and regeneration is destructive

`book_audiobooks.book_id` is the primary key, mirroring `book_reading_guides`. Regenerate overwrites: old `files` row and bytes deleted at finalize, `books.chapters` rewritten, type-to-confirm in the UI.

Several voices per book is a real product idea and was rejected on arithmetic. Each rendition is roughly 500 MB, so ten books in two voices is 10 GB. It also unpicks three decisions above: the Listen control would need a picker, `books.chapters` could no longer be the canonical playback view, and "which version am I listening to" becomes per-user state that does not exist anywhere today. One-to-many is a migration later, not a rewrite.

### 5. Generation never enters the metadata write-back pipeline

`MetadataWriter` owns three triggers — `manual_edit`, `apply_enrichment`, `auto_enrichment` — and generation is none of them. No Sidecar write, no in-file embed, no folder rename. This is the same boundary ADR-0024 drew for Reading guides, for the same reason: derived output must not masquerade as the publisher's metadata.

Concretely, `books.narrator` is **not** written. That column means "what this file's tags said", populated by `AudioProcessor` on ingest. The synthesized voice lives on `book_audiobooks.voice` and is surfaced in the audiobook UI. Writing `"alloy (OpenAI)"` into `narrator` would put a derived value into the field set ADR-0001 governs, and would then need rules for regeneration, for metadata re-extract, and for whether the lock flags apply — three questions with no good answers.

### 6. Deleting a Book must delete its bytes

`handler.BookDelete` today hard-deletes the row, cascades DB children, drops the cover, and unlinks only `book.Path` — the legacy single-path field. It never iterates `files` rows and never calls `storage.Storage.Delete`. Deleting a book on an S3-backed library already orphans its bytes, and the handler's own comment concedes it: "Leaving orphan bytes on disk is fixable."

Tolerable for a 2 MB EPUB. Not tolerable for a 500 MB MP3. `BookDelete` is fixed to iterate `files` rows and delete each through the library's `Storage`, deferring to `pending_orphans` on S3 per ADR-0005.

This is a fix to already-shipped shared behaviour, not something the audiobook feature introduced — the audiobook only makes the existing leak 250× more expensive. It should land as its own change, ahead of this feature. A bespoke delete path for audiobook bytes alone was rejected: it contradicts §1 and guarantees two delete paths that drift.

## Considered options

### Rejected: a new `books` row per audiobook

Everything downstream works untouched — the card shows an audiobook, the reader opens `AudioReader`, `primaryFile` is right. Rejected because it forks identity: two cards in the library for one work, duplicated metadata, two progress values, two rating fields, and a shelf that contains the text but not the audio. Every one of those is a bug report waiting to be filed.

### Rejected: a derived-asset store outside the library

The `coverstore` shape — hash-keyed bytes under `${DATA_PATH}`, served by a dedicated handler, regenerable and evictable, never in `files`, never walked by scan. Genuinely attractive: it keeps synthesized bytes out of machinery meant for ingested ones, exactly as ADR-0024 kept guide text out of the metadata pipeline. Rejected because the user's expectation for an audiobook is a file they own, and a cache is not that. See §1.

### Rejected: flipping `books.format` to `MP3`

Covered in §3. One line, and it takes the book away.

### Rejected: an audiobook tab beside the Guide tab

Least invasive — the reader is not touched at all, `book.format` keeps its meaning, and a small player lives in a side panel. Rejected because it makes listening a second-class accessory rather than a way of reading the book, and it duplicates a player (`AudioReader`) that already exists and is good.

## Open questions

- Whether the generated file appears in OPDS feeds, and whether an OPDS client should see one entry or two.
- What the library card shows for a book with both renditions — one badge, two, or nothing.
- Whether a stale audiobook (§2) should be actively discouraged from playing, or merely labelled.

## Companion artifacts

- `CONTEXT.md` — Rendition, Narratable format, Alignment map, Audiobook run, Segment, Staging area.
- ADR-0026 — the TTS engine catalog that produces the bytes.
- ADR-0027 — why those bytes are MP3 with ID3 chapter frames.
- ADR-0028 — how generation is triggered and executed.
- ADR-0001 — the metadata write-back pipeline this deliberately sits outside.
- ADR-0003 — the LeafBook folder layout the generated file is placed into.
- ADR-0005 — the S3 deferred-delete path §6 reuses.
- ADR-0024 — Reading guides, whose boundary-drawing this follows and whose storage choice it deliberately does not.
