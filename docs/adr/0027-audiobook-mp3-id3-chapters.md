# Generated audiobooks are MP3 with ID3 chapter frames, not M4B

ADR-0025 makes the generated narration a real library file that a user can download and take elsewhere. That commits us to a container, and the container decision is where a pure-Go single-binary application meets the fact that audiobooks are conventionally M4B.

## Status

accepted (2026-07-27)

## Decisions bundled here

### 1. Every engine emits MP3; none emits M4B

ElevenLabs returns `mp3_44100_128`, OpenAI returns `mp3`, Azure returns `audio-44khz-*-mono-mp3`. Only OpenAI offers AAC, and AAC is what an M4B actually needs — MP3 inside an MP4 container is legal and badly supported in the wild.

So any M4B path requires encoding AAC ourselves, and there is no pure-Go AAC encoder worth depending on. That is not a preference; it is the blocker, and it is not routable around.

MP3 has the property that makes the rest of this feature cheap: frames concatenate. Given the same engine, voice and settings — which holds for every segment of one generation — the per-segment responses can be joined byte-wise into one valid file with no transcode, no muxer, and no new dependency. The concatenation is the entire "finalize" step.

### 2. Chapters live in the file *and* in the database

`books.chapters JSONB` already exists as `[]model.Chapter{Title, StartS, EndS}`, and `ui/src/components/AudioReader.tsx` already reads it to render a chapter drawer and seek. Nothing writes that column today; narration becomes its first writer. In-app chapter playback therefore needs no container work at all.

That alone would be enough for embookshelf, and it is tempting to stop there. It is refused because ADR-0025 §1 chose a portable library file specifically so it keeps working outside embookshelf, and a chapterless eight-hour MP3 on a phone is a bad artifact. So the finalize step also writes ID3v2.3 `CTOC` and `CHAP` frames — a few hundred lines of pure Go — which Apple Books, Pocket Casts, VLC and most podcast players read. Standard ID3 tags go in alongside: title, artist from author, album from title, genre `Audiobook`, and the book's cover art as the embedded picture.

The database copy stays canonical for in-app playback; the file copy exists so the file is self-describing when it leaves. They are written from the same source in the same step, so they cannot disagree.

### 3. No ffmpeg

One `ffmpeg -f concat -c:a aac -f mp4` invocation would produce a real M4B with chapter metadata and an embedded cover, roughly 40% smaller at equal quality, and would take an afternoon. Rejected because embookshelf is a single binary with an embedded UI, and requiring an external executable on `PATH` — present in some container images, absent in others, differently built everywhere — turns a first-class feature into one that is silently dark on half of installs. Trading the product's defining property for container polish is the wrong direction.

## Considered options

### Rejected: M4B via a pure-Go MP4 muxer

`abema/go-mp4` can write MP4, and real chapter atoms plus `M4B` outranking `MP3` in the primary-format priority chain would be tidier. Rejected on the codec, per §1: MP3-in-MP4 is poorly supported and AAC is unavailable without an encoder that does not exist in Go.

### Rejected: ffmpeg as an optional dependency, feature dark when absent

Covered in §3.

### Rejected: one MP3 per chapter, N `files` rows

No concatenation at all, and per-chapter files are individually retryable and individually servable. Rejected because `AudioReader` takes a single `src` and would need a playlist concept it does not have, and because it puts twenty to forty rows per book into `files`, which then appear in every surface that lists a book's files.

### Rejected: chapters only in `books.chapters`

Free, and correct inside embookshelf. Rejected per §2 — it makes the downloaded file worse than the one in the app, which contradicts the reason ADR-0025 made it a library file.

## Open questions

- Bitrate and mono/stereo. Engines default to 128 kbps stereo-ish output; mono at 64 kbps would roughly quarter a 500 MB file at no real cost for speech, if every engine can be made to agree.
- Whether ID3v2.4 is preferable to 2.3 for `CHAP`; 2.3 is more widely read, 2.4 is cleaner.
- Whether a segment boundary can ever land mid-frame in a way that produces an audible click, and whether frame-aligned trimming is needed.

## Companion artifacts

- `CONTEXT.md` — Rendition, Alignment map.
- ADR-0025 — why the artifact is a portable library file, which is what forces §2.
- ADR-0026 — the engines whose output formats constrain §1.
- ADR-0028 — the pipeline whose finalize step performs the concatenation and tagging.
