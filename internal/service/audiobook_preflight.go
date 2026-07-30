// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// ErrAudiobooksNotConfigured is returned when the service has no settings
// reader wired at all — a deployment without the feature, distinct from
// one that has it and has turned it off.
//
// The handler answers it 503 with CodeAudiobooksDisabled.
var ErrAudiobooksNotConfigured = errors.New("audiobook generation is not configured")

// ErrAudiobooksDisabled is returned when the admin has the feature off.
//
// The handler answers it 503 with CodeAudiobooksDisabled, the same pair
// as the not-configured case above: the two are different causes with
// one consequence and one fix, and the client has a single branch for
// "narration is unavailable here". It reached the client as a bare 409
// with no code until #221, so the UI showed a generic conflict toast
// instead of the disabled affordance.
//
// **Distinct** from task.ErrEngineDisabledForJob, which shares neither
// this identity nor its purpose: that one is a retry verdict for River.
// The two carried the same name and were never related by errors.Is.
var ErrAudiobooksDisabled = errors.New("audiobook generation is not enabled")

// GenerateOverride is what the generate dialog may change about a run.
// Both empty means the instance defaults, which is the common case.
type GenerateOverride struct {
	Voice string
	Model string
}

// AudiobookReport is a run as a reader needs it: its state, how far it
// has got, and whether the book has changed underneath it.
type AudiobookReport struct {
	Run      model.Audiobook
	Coverage model.AudiobookCoverage
	// Stale reports that the EPUB has changed since this narration was
	// made. Surfaced, never acted on — discarding hours of audio because
	// someone re-uploaded a better copy would be worse than saying so.
	Stale bool
}

// narrationArtifacts is what finalize wrote outside the run's own table:
// the files row naming the generated audio, and the book's playback view
// — its duration and chapter list.
//
// A narrow interface because deleting a narration has to undo all of it
// and "did it?" is worth asserting. The service still cannot reach the
// files table itself, which is the property #191 established.
//
// DeleteFile is the row only. The bytes that row names are narrationBytes'
// below, which takes this method as the middle step of one operation — the
// two cannot be sequenced independently and be right.
type narrationArtifacts interface {
	DeleteFile(ctx context.Context, fileID string) error
	ClearBookAudio(ctx context.Context, bookID string) error
}

// narrationBytes removes the audio a finished run produced, taking the
// delete of the files row that names it as its middle step.
//
// The signature is the whole point, and it is DeleteBookAndBytes' (#260):
// the caller supplies *what* deletes the row and the operation decides
// *when*, because the location can only be read while the row exists and
// the bytes can only go once it does not. Composed the other way round —
// row here, bytes over there — the order is free to be wrong, and it was:
// the sweep asked for the book's file after the row delete had taken it
// away, was told "not found", and declined, so every deleted narration
// left half a gigabyte behind (#267).
//
// Two errors because the caller does two different things with them. err
// is the row delete's and it is the delete; bytesErr is the cleanup's and
// it is a warning — the row is already gone, and failing the call would
// tell a user their narration is still there when it is not.
type narrationBytes interface {
	DeleteNarrationBytes(
		ctx context.Context,
		book model.Book,
		fileID string,
		deleteRow func(context.Context) error,
	) (bytesErr error, err error)
}

// DeleteNarrationAndBytes removes one of a book's files rows and the bytes
// it named, in the only order that works, by taking the row delete as its
// middle step.
//
// The narration counterpart to DeleteBookAndBytes, and a separate
// operation rather than a call to it because the book outlives its
// narration: DeleteBookAndBytes snapshots every location the book owns,
// which here would take the EPUB the narration was made from.
//
// A row whose location cannot be read is reported rather than passed over.
// That silence is exactly how the audio came to outlive the narration: a
// lookup that answers "not found" and a cleanup that declines look
// identical to a caller, and one of them means half a gigabyte is still
// being paid for.
//
// Lives here, beside the narration delete it serves, rather than next to
// DeleteBookAndBytes in library_store.go — worth moving when that file is
// free to edit.
func (h *LibraryHandle) DeleteNarrationAndBytes(
	ctx context.Context,
	bookID, fileID string,
	deleteRow func(context.Context) error,
) (bytesErr error, err error) {
	f, found := h.BookFile(ctx, bookID, fileID)
	if err := deleteRow(ctx); err != nil {
		return nil, err
	}
	if !found || f.Location == "" {
		return fmt.Errorf("narration file %s of book %s has no readable location; its bytes are left behind",
			fileID, bookID), nil
	}
	return h.DeleteBookBytes(ctx, bookID, []string{f.Location}), nil
}

// Preflight resolves everything a run needs before it can start: the
// settings row, the enabled gate, the engine selection, the Narratable
// format gate, and the provenance hash.
//
// All of it used to be the handler's, which meant the handler decided
// domain questions and wrote HTTP responses in the same breath, and none
// of it could be exercised without an HTTP recorder (#191). The handler
// now maps the typed errors below onto status codes and nothing else.
func (s *AudiobookService) Preflight(
	ctx context.Context,
	book model.Book,
	over GenerateOverride,
) (AudiobookOptions, error) {
	cfg, err := s.d.Settings(ctx)
	if err != nil {
		return AudiobookOptions{}, fmt.Errorf("read audiobook settings: %w", err)
	}
	if !cfg.Enabled {
		return AudiobookOptions{}, ErrAudiobooksDisabled
	}
	id, engine, err := cfg.SelectedEngine()
	if err != nil {
		return AudiobookOptions{}, err
	}
	// The second of the Narratable format's three gates — the UI holds
	// the first and the segment worker the third — because a re-import
	// can change a book's format between them (ADR-0028 §4).
	if !Narratable(book.Format) {
		return AudiobookOptions{}, ErrNotNarratable
	}

	opts := AudiobookOptions{
		Engine:               string(id),
		Voice:                engine.DefaultVoice,
		Model:                engine.Model,
		SegmentChars:         cfg.SegmentChars,
		PricePerMillionChars: engine.PricePerMillionChars,
		SourceContentHash:    s.d.ContentHash(ctx, book),
	}
	if over.Voice != "" {
		opts.Voice = over.Voice
	}
	if over.Model != "" {
		opts.Model = over.Model
	}
	return opts, nil
}

// Generate preflights and starts a run: the one call a request handler
// makes.
func (s *AudiobookService) Generate(ctx context.Context, book model.Book, over GenerateOverride) error {
	opts, err := s.Preflight(ctx, book, over)
	if err != nil {
		return err
	}
	return s.Start(ctx, book, opts)
}

// EstimateRun preflights and prices a run without starting one.
func (s *AudiobookService) EstimateRun(
	ctx context.Context,
	book model.Book,
	over GenerateOverride,
) (AudiobookEstimate, error) {
	opts, err := s.Preflight(ctx, book, over)
	if err != nil {
		return AudiobookEstimate{}, err
	}
	return s.Estimate(ctx, book, opts)
}

// Report reads a run, reconciles it, and says whether the book has moved
// under it. One call, because the staleness comparison needs the run's
// recorded hash and the book's current one, and a caller assembling that
// itself is a caller that can get it wrong in only one direction: a
// narration silently reported as current.
func (s *AudiobookService) Report(ctx context.Context, book model.Book) (AudiobookReport, error) {
	run, cov, err := s.Status(ctx, book.ID)
	if err != nil {
		return AudiobookReport{}, err
	}
	return AudiobookReport{Run: run, Coverage: cov, Stale: s.stale(ctx, book, run)}, nil
}

// DeleteNarration removes a narration: the run record, then the files row
// naming its audio together with the audio itself. The book keeps its
// EPUB.
//
// The files row and the bytes go through one operation rather than being
// composed here, which is what makes the order unrepresentable instead of
// merely written down. Stated as two steps it was got wrong in the one
// direction that is silent: the row went first, the sweep then asked the
// library for the book's file to learn where the bytes were, and the row
// that answers that question was already gone (#267).
//
// Deliberately not a deferred cleanup either: that would also fire on the
// failure path, deleting the audio out from under a run that still points
// at it.
//
// The rest of what finalize wrote outside the run's own table goes too. It
// writes four things — the files row, the book's duration and chapters, the
// run, and each segment's offset — and deleting the run took two of them
// (the segments cascade), so the reader kept finding a chapter list for a
// narration that no longer existed (#208).
//
// Every failure past the run row is logged rather than returned: a user
// must not be left with a narration they cannot remove. What is left
// behind is collectable — an orphaned object by the key sweeper, an
// orphaned files row by the missing-file purge after a scan.
func (s *AudiobookService) DeleteNarration(ctx context.Context, book model.Book) error {
	run, err := s.d.Store.GetByBookID(ctx, book.ID)
	if err != nil {
		return err
	}
	if err := s.d.Store.Delete(ctx, book.ID); err != nil {
		return fmt.Errorf("delete narration: %w", err)
	}

	if run.FileID != nil {
		fileID := *run.FileID
		bytesErr, err := s.d.NarrationBytes.DeleteNarrationBytes(ctx, book, fileID,
			func(ctx context.Context) error { return s.d.Artifacts.DeleteFile(ctx, fileID) })
		if err != nil {
			slog.Warn("audiobook: delete narration files row", "book", book.ID, "err", err)
		}
		if bytesErr != nil {
			slog.Warn("audiobook: narration byte cleanup", "book", book.ID, "err", bytesErr)
		}
	}
	if err := s.d.Artifacts.ClearBookAudio(ctx, book.ID); err != nil {
		slog.Warn("audiobook: clear book audio fields", "book", book.ID, "err", err)
	}
	return nil
}

// stale compares what the run was made from against the book's current
// file. Both hashes have to be readable: without them the honest answer
// is "not known to be stale", because a badge shown on a comparison that
// never happened is a lie in the direction that costs money.
func (s *AudiobookService) stale(ctx context.Context, book model.Book, run model.Audiobook) bool {
	if len(run.SourceContentHash) == 0 {
		return false
	}
	current := s.d.ContentHash(ctx, book)
	if len(current) == 0 {
		return false
	}
	return !bytes.Equal(current, run.SourceContentHash)
}

// RepoNarrationArtifacts adapts the two repos to what DeleteNarration
// needs. A thin adapter rather than handing the service the repos: the
// pair of methods below is its whole reach outside the run's table.
type RepoNarrationArtifacts struct {
	Files *repo.FileRepo
	Books *repo.BookRepo
}

func (a RepoNarrationArtifacts) DeleteFile(ctx context.Context, fileID string) error {
	return a.Files.Delete(ctx, fileID)
}

// ClearBookAudio resets the playback view finalize wrote. The narrator
// is deliberately untouched — books.narrator means "what this file's
// tags said" and a generated narration never writes it (ADR-0025 §5).
func (a RepoNarrationArtifacts) ClearBookAudio(ctx context.Context, bookID string) error {
	zero := 0
	return a.Books.UpdateAudio(ctx, bookID, &zero, "", nil)
}

// LibraryNarrationBytes is the production narrationBytes: it resolves the
// book's library and hands the row delete to the handle's operation.
//
// Thin on purpose, and thin is the change. It replaces a closure at the
// composition root that composed the whole sequence itself — resolve, look
// the file up, and delete bytes that some other tier had already deleted
// the row for — which is precisely where the ordering was free to be
// wrong, and where nothing exercised it (#267). What survives here is the
// one thing the service cannot do for itself: reach a library handle
// (#191).
type LibraryNarrationBytes struct {
	libs LibraryStore
}

// NewLibraryNarrationBytes builds the adapter. A nil store is the
// no-library-wired install: see DeleteNarrationBytes.
func NewLibraryNarrationBytes(libs LibraryStore) *LibraryNarrationBytes {
	return &LibraryNarrationBytes{libs: libs}
}

// DeleteNarrationBytes resolves the library and delegates. A library it
// cannot reach is a degraded cleanup, never a blocked delete — the same
// reading DeleteBook takes of an unresolvable handle: resolving is
// read-only, so the row still goes and the bytes wait for an operator,
// reported rather than passed over in silence.
func (b *LibraryNarrationBytes) DeleteNarrationBytes(
	ctx context.Context,
	book model.Book,
	fileID string,
	deleteRow func(context.Context) error,
) (bytesErr error, err error) {
	if b == nil || b.libs == nil {
		if err := deleteRow(ctx); err != nil {
			return nil, err
		}
		return errors.New("no library store configured; narration bytes left behind"), nil
	}
	handle, herr := b.libs.For(ctx, book.LibraryID)
	if herr != nil {
		if err := deleteRow(ctx); err != nil {
			return nil, err
		}
		return fmt.Errorf("resolve library %s: %w", book.LibraryID, herr), nil
	}
	return handle.DeleteNarrationAndBytes(ctx, book.ID, fileID, deleteRow)
}
