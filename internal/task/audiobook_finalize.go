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
	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// audiobookGenre is what every generated file declares, so a player that
// groups by genre files it with audiobooks rather than with music.
const audiobookGenre = "Audiobook"

// finalizeStore is the slice of BookAudiobookRepo finalize touches.
type finalizeStore interface {
	GetByBookID(ctx context.Context, bookID string) (model.Audiobook, error)
	ListSegments(ctx context.Context, bookID string) ([]model.AudiobookSegment, error)
	SetSegmentStart(ctx context.Context, bookID string, seq int, startMS int64) error
}

// narrationReporter is the run service. The worker reports that it has
// the file; what the run's state becomes, and whether an open page is
// told, is the service's (#210). It used to mark the run ready through
// the repo directly, which is how a run cancelled mid-assembly came back
// as ready.
type narrationReporter interface {
	NarrationAssembled(ctx context.Context, bookID, fileID string, durationMS int64) error
}

// bookAudioWriter reads the book and writes back what only a finished
// narration knows: its duration and its chapter marks.
type bookAudioWriter interface {
	bookReader
	UpdateAudio(ctx context.Context, id string, durationSeconds *int, narrator string, chapters []model.Chapter) error
}

// narrationFiles is the files-table slice finalize needs to record the
// placed audio, reusing the row a previous rendition left behind.
type narrationFiles interface {
	GetByLocation(ctx context.Context, libraryID, location string) (model.File, error)
	SetContentHash(ctx context.Context, fileID string, hash []byte, size int64, mtime time.Time) error
	Insert(ctx context.Context, f model.File) (model.File, error)
}

// FinalizeDeps groups the seams the finalize worker needs.
type FinalizeDeps struct {
	Runs   finalizeStore
	Report narrationReporter
	Books  bookAudioWriter
	Files  narrationFiles
	// Place moves the assembled file into the book's own folder.
	// Narrower than a LibraryStore on purpose: handing over the whole
	// store would let a later edit reach more than placing one file
	// needs. Why the closure that builds this field must call
	// PlaceNarration rather than the generic Placer is that closure's
	// decision to explain, not this worker's — see the registry.
	Place func(ctx context.Context, book model.Book, srcPath string) (service.PlaceResult, error)
	// Cover supplies the art embedded in the finished file. Nil-able and
	// best effort: a narration without embedded art is still a good
	// narration.
	Cover func(book model.Book) (io.ReadCloser, error)
	// Fail marks the run failed and publishes, through the one module
	// that owns that transition.
	Fail func(ctx context.Context, bookID, msg string) error
	// Staging is the area holding this run's segments until they are
	// joined. Taken as the value rather than as the root it was built
	// from, so the one module that knows where staging lives is also the
	// one that removes it.
	Staging Staging
}

// AudiobookFinalize joins a finished run's staged segments into the one
// file that becomes a library artifact.
//
// The steps are ordered so a failure never leaves a half-published book:
// concatenate and tag into a temp file, place it, insert the files row,
// then mark the run ready. A crash before the last step leaves a file on
// disk and a run still marked running, which a Retry resolves; a crash
// after it leaves nothing to resolve.
func AudiobookFinalize(ctx context.Context, a jobs.AudiobookFinalizeArgs, deps FinalizeDeps) error {
	run, err := deps.Runs.GetByBookID(ctx, a.BookID)
	if err != nil {
		return fmt.Errorf("load audiobook %s: %w", a.BookID, err)
	}
	if run.State == model.AudiobookCanceled {
		// Cancelled between the last segment landing and this job being
		// picked up. Sweep rather than publish — a cancel means the user
		// does not want the partial, and here the partial is complete.
		deps.Staging.Clean(a.BookID)
		return nil
	}
	if run.State == model.AudiobookReady {
		return nil
	}

	segments, err := deps.Runs.ListSegments(ctx, a.BookID)
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

	// Hashed before placement, because placement consumes the file:
	// content_hash is the authoritative identity of every other files row
	// (CONTEXT, Content hash) and a narration with a NULL one is invisible
	// to the rename safety net a library scan runs on.
	hash, err := hashFile(ctx, assembled)
	if err != nil {
		return fail(ctx, deps, a.BookID, fmt.Errorf("hash narration: %w", err))
	}

	placed, err := deps.Place(ctx, book, assembled)
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

	// The service publishes if the run actually moved, so there is no
	// publish here: a run the user cancelled while this was assembling
	// does not move, and telling open pages otherwise would contradict
	// the cancel they just watched land.
	if err := deps.Report.NarrationAssembled(ctx, a.BookID, fileRow.ID, totalMS); err != nil {
		return fmt.Errorf("mark ready: %w", err)
	}
	deps.Staging.Clean(a.BookID)
	return nil
}

// upsertNarrationFile records the placed audio, reusing the row a
// previous rendition left at the same location.
func upsertNarrationFile(
	ctx context.Context,
	deps FinalizeDeps,
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
	deps FinalizeDeps,
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
		if err := deps.Runs.SetSegmentStart(ctx, s.BookID, s.Seq, elapsed); err != nil {
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
//
// Asks by book rather than by hash: a library the Covers backfill has not
// finished has rows with no cover_hash at all, and those books used to
// finalize with no art for no better reason than which namespace their
// bytes happened to be in.
func loadCover(deps FinalizeDeps, book model.Book) ([]byte, string) {
	if deps.Cover == nil || !book.HasCover {
		return nil, ""
	}
	rc, err := deps.Cover(book)
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
// The write goes through the one module that marks a run failed, which
// is also what publishes — this worker used to do both itself, and the
// status read doing the same thing published nothing (#190).
//
// Staging is deliberately left in place: Retry re-enqueues only the
// segments that never finished, so every paid-for segment has to survive
// a failed finalize (ADR-0028 §6).
func fail(ctx context.Context, deps FinalizeDeps, bookID string, cause error) error {
	if err := deps.Fail(ctx, bookID, cause.Error()); err != nil {
		slog.Warn("audiobook: mark run failed", "book", bookID, "err", err)
	}
	slog.Error("audiobook finalize failed", "book", bookID, "err", cause)
	return nil
}
