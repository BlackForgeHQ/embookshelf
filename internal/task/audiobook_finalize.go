// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/blackforge/embookshelf/internal/audio"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// audiobookGenre is what every generated file declares, so a player that
// groups by genre files it with audiobooks rather than with music.
const audiobookGenre = "Audiobook"

// AudiobookFinalize joins a finished run's staged segments into the one
// file that becomes a library artifact.
//
// The steps are ordered so a failure never leaves a half-published book:
// concatenate and tag into a temp file, place it, insert the files row,
// then mark the run ready. A crash before the last step leaves a file on
// disk and a run still marked running, which a Retry resolves; a crash
// after it leaves nothing to resolve.
func AudiobookFinalize(ctx context.Context, a AudiobookFinalizeArgs, deps AudiobookDeps) error {
	run, err := deps.Audiobooks.GetByBookID(ctx, a.BookID)
	if err != nil {
		return fmt.Errorf("load audiobook %s: %w", a.BookID, err)
	}
	if run.State == model.AudiobookCanceled {
		// Cancelled between the last segment landing and this job being
		// picked up. Sweep rather than publish — a cancel means the user
		// does not want the partial, and here the partial is complete.
		cleanStaging(deps.DataPath, a.BookID)
		return nil
	}
	if run.State == model.AudiobookReady {
		return nil
	}

	segments, err := deps.Audiobooks.ListSegments(ctx, a.BookID)
	if err != nil {
		return fmt.Errorf("list segments: %w", err)
	}
	if len(segments) == 0 {
		return fail(ctx, deps, a.BookID, errors.New("no segments to assemble"))
	}
	for _, s := range segments {
		if s.State != model.SegmentDone {
			// Reached before the run actually finished. Not an error —
			// the segment that completes last dispatches this again.
			slog.Debug("audiobook finalize deferred", "book", a.BookID, "seq", s.Seq)
			return nil
		}
	}

	book, err := deps.Books.GetByID(ctx, "", a.BookID)
	if err != nil {
		return fmt.Errorf("load book %s: %w", a.BookID, err)
	}

	assembled, chapters, totalMS, err := assemble(ctx, deps, book, segments)
	if err != nil {
		return fail(ctx, deps, a.BookID, err)
	}
	defer func() { _ = os.Remove(assembled) }()

	handle, err := deps.LibStore.For(ctx, book.LibraryID)
	if err != nil {
		return fail(ctx, deps, a.BookID, fmt.Errorf("resolve library: %w", err))
	}

	// Hashed before placement, because placement consumes the file:
	// content_hash is the authoritative identity of every other files row
	// (CONTEXT, Content hash) and a narration with a NULL one is invisible
	// to the rename safety net a library scan runs on.
	hash, err := hashFile(ctx, assembled)
	if err != nil {
		return fail(ctx, deps, a.BookID, fmt.Errorf("hash narration: %w", err))
	}

	// Deliberately PlaceNarration rather than the generic Placer: the
	// book's folder already exists, and Placer would answer that with a
	// "Title (2)" sibling — a second leaf that scan reads as a second book.
	placed, err := handle.PlaceNarration(ctx, book, assembled)
	if err != nil {
		return fail(ctx, deps, a.BookID, fmt.Errorf("place narration: %w", err))
	}

	// Regeneration lands on the same key, so the previous rendition's row
	// is updated rather than duplicated. Inserting unconditionally would
	// violate files' UNIQUE(library_id, location) on the second run, and
	// on a backend it would leave the old row pointing at bytes the new
	// upload has already overwritten.
	fileRow, err := upsertNarrationFile(ctx, deps, book, placed, hash)
	if err != nil {
		return fail(ctx, deps, a.BookID, fmt.Errorf("record narration file: %w", err))
	}

	// books.chapters and duration_seconds are what the existing audio
	// reader already consumes; this is their first writer. Narrator is
	// passed through unchanged — it means "what this file's tags said",
	// and a synthesized voice is not that (ADR-0025 §5).
	durationSeconds := int(totalMS / 1000)
	if err := deps.Books.UpdateAudio(ctx, book.ID, &durationSeconds, book.Narrator, chapters); err != nil {
		slog.Warn("audiobook: update book audio fields", "book", a.BookID, "err", err)
	}

	if err := deps.Audiobooks.SetReady(ctx, a.BookID, fileRow.ID, totalMS); err != nil {
		return fmt.Errorf("mark ready: %w", err)
	}
	cleanStaging(deps.DataPath, a.BookID)
	publishAudiobook(deps, a.BookID)
	return nil
}

// upsertNarrationFile records the placed audio, reusing the row a
// previous rendition left at the same location.
func upsertNarrationFile(
	ctx context.Context,
	deps AudiobookDeps,
	book model.Book,
	placed service.PlaceResult,
	hash []byte,
) (model.File, error) {
	existing, err := deps.Files.GetByLocation(ctx, book.LibraryID, placed.Location)
	if err == nil {
		if serr := deps.Files.SetContentHash(ctx, existing.ID, hash, placed.Size, placed.Mtime); serr != nil {
			return model.File{}, serr
		}
		existing.ContentHash, existing.Size, existing.Mtime = hash, placed.Size, placed.Mtime
		return existing, nil
	}
	if !errors.Is(err, repo.ErrNotFound) {
		return model.File{}, err
	}
	return deps.Files.Insert(ctx, model.File{
		LibraryID:   book.LibraryID,
		BookID:      book.ID,
		Location:    placed.Location,
		Size:        placed.Size,
		Mtime:       placed.Mtime,
		ContentHash: hash,
		Format:      "MP3",
	})
}

// assemble concatenates the staged segments, writes the ID3 tag, and
// returns the path of the finished file plus its chapter marks.
//
// Chapter marks are derived here rather than at plan time because only
// now is every segment's real duration known. Segments sharing a
// ChapterIndex collapse into one Chapter, so a chapter split across three
// jobs is still one entry in the reader's drawer.
func assemble(
	ctx context.Context,
	deps AudiobookDeps,
	book model.Book,
	segments []model.AudiobookSegment,
) (string, []model.Chapter, int64, error) {
	parts := make([][]byte, 0, len(segments))
	for _, s := range segments {
		b, err := os.ReadFile(s.StagedPath)
		if err != nil {
			return "", nil, 0, fmt.Errorf("read staged segment %d: %w", s.Seq, err)
		}
		parts = append(parts, b)
	}

	out, err := os.CreateTemp("", "embookshelf-audiobook-*.mp3")
	if err != nil {
		return "", nil, 0, err
	}
	defer func() { _ = out.Close() }()

	// The tag has to precede the frames, and its chapter times need the
	// frame durations, so the frames are joined into a buffer first,
	// timed, and only then written out behind the finished tag.
	var frames bytes.Buffer
	durations, err := audio.Concat(&frames, parts)
	if err != nil {
		return "", nil, 0, fmt.Errorf("join segments: %w", err)
	}

	chapters, totalMS := chapterMarks(segments, durations)

	tags := audio.Tags{
		Title:  book.Title,
		Artist: book.Author,
		Album:  book.Title,
		Genre:  audiobookGenre,
	}
	if cover, mime := loadCover(deps, book); len(cover) > 0 {
		tags.Cover, tags.CoverMIME = cover, mime
	}

	audioChapters := make([]audio.Chapter, 0, len(chapters))
	for _, c := range chapters {
		audioChapters = append(audioChapters, audio.Chapter{
			Title:   c.Title,
			StartMS: uint32(c.StartS * 1000),
			EndMS:   uint32(c.EndS * 1000),
		})
	}
	if err := audio.WriteID3(out, tags, audioChapters); err != nil {
		return "", nil, 0, fmt.Errorf("write tag: %w", err)
	}
	if _, err := out.Write(frames.Bytes()); err != nil {
		return "", nil, 0, fmt.Errorf("write frames: %w", err)
	}

	// Record where each segment landed, which completes the alignment map
	// the reading/listening progress bridge is built on.
	var elapsed int64
	for i, s := range segments {
		if err := deps.Audiobooks.SetSegmentStart(ctx, s.BookID, s.Seq, elapsed); err != nil {
			slog.Warn("audiobook: record segment start", "book", s.BookID, "seq", s.Seq, "err", err)
		}
		elapsed += durations[i]
	}
	return out.Name(), chapters, totalMS, nil
}

// chapterMarks folds segments into chapters using the measured durations.
func chapterMarks(segments []model.AudiobookSegment, durations []int64) ([]model.Chapter, int64) {
	var (
		chapters []model.Chapter
		elapsed  int64
		current  = -1
	)
	for i, s := range segments {
		if s.ChapterIndex != current {
			chapters = append(chapters, model.Chapter{
				Title:  s.ChapterTitle,
				StartS: float64(elapsed) / 1000,
			})
			current = s.ChapterIndex
		}
		elapsed += durations[i]
		chapters[len(chapters)-1].EndS = float64(elapsed) / 1000
	}
	return chapters, elapsed
}

// loadCover reads the book's cover so the finished file carries it. Best
// effort — a narration without embedded art is still a good narration.
func loadCover(deps AudiobookDeps, book model.Book) ([]byte, string) {
	if deps.Covers == nil || !book.HasCover || len(book.CoverHash) == 0 {
		return nil, ""
	}
	rc, err := deps.Covers.OpenBookHashed(book.CoverHash, book.CoverMime)
	if err != nil {
		return nil, ""
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		return nil, ""
	}
	return b, book.CoverMime
}

// fail records the reason on the run and stops River retrying.
//
// Staging is deliberately left in place: Retry re-enqueues only the
// segments that never finished, so every paid-for segment has to survive
// a failed finalize (ADR-0028 §6).
func fail(ctx context.Context, deps AudiobookDeps, bookID string, cause error) error {
	if err := deps.Audiobooks.SetState(ctx, bookID, model.AudiobookFailed, cause.Error()); err != nil {
		slog.Warn("audiobook: mark run failed", "book", bookID, "err", err)
	}
	publishAudiobook(deps, bookID)
	slog.Error("audiobook finalize failed", "book", bookID, "err", cause)
	return nil
}

// cleanStaging removes a book's staged segments.
func cleanStaging(dataPath, bookID string) {
	if dataPath == "" {
		return
	}
	if err := os.RemoveAll(StagingDir(dataPath, bookID)); err != nil {
		slog.Warn("audiobook: clean staging", "book", bookID, "err", err)
	}
}

// StaleStagingTTL is how long an abandoned failed or cancelled run keeps
// its staging before the sweeper reclaims it. Long enough that a Retry
// the next morning is still free; short enough that a run nobody comes
// back to does not park gigabytes forever.
const StaleStagingTTL = 7 * 24 * time.Hour

// SweepAudiobookStaging removes staging for runs whose staged segments
// have been dead weight for longer than StaleStagingTTL. Which runs
// those are is ListStaleStaging's judgement, not this loop's.
func SweepAudiobookStaging(ctx context.Context, deps AudiobookDeps) (int, error) {
	ids, err := deps.Audiobooks.ListStaleStaging(ctx, int(StaleStagingTTL/(24*time.Hour)))
	if err != nil {
		return 0, err
	}
	swept := 0
	for _, id := range ids {
		dir := StagingDir(deps.DataPath, id)
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		cleanStaging(deps.DataPath, id)
		swept++
	}
	return swept, nil
}

// LoopAudiobookStagingSweep runs the sweep hourly, matching the shape of
// the missing-file and orphaned-key sweepers.
func LoopAudiobookStagingSweep(ctx context.Context, deps AudiobookDeps) {
	if deps.DataPath == "" || deps.Audiobooks == nil {
		return
	}
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := SweepAudiobookStaging(ctx, deps)
			if err != nil {
				slog.Warn("audiobook staging sweep", "err", err)
				continue
			}
			if n > 0 {
				slog.Info("audiobook staging sweep", "swept", n)
			}
		}
	}
}
