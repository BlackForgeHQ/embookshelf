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
var ErrAudiobooksNotConfigured = errors.New("audiobook generation is not configured")

// ErrAudiobooksDisabled is returned when the admin has the feature off.
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

// WithSettings wires the AUDIOBOOK settings row. Read per call rather
// than captured, so an admin changing engine or voice takes effect on the
// next request instead of the next restart.
func (s *AudiobookService) WithSettings(fn func(context.Context) (repo.AudiobookConfig, error)) *AudiobookService {
	s.settings = fn
	return s
}

// WithContentHash wires the read of a book's current file hash.
//
// Injected because it lives on the files row behind a library handle, and
// this service deliberately reaches neither — the property that lets its
// whole lifecycle be exercised without storage or a database.
func (s *AudiobookService) WithContentHash(fn func(context.Context, model.Book) []byte) *AudiobookService {
	s.hash = fn
	return s
}

// narrationArtifacts is what finalize wrote outside the run's own table:
// the files row naming the generated audio, and the book's playback view
// — its duration and chapter list.
//
// A narrow interface rather than the opaque closure the byte sweep uses,
// because deleting a narration has to undo all of it and "did it?" is
// worth asserting. The service still cannot reach the files table
// itself, which is the property #191 established.
type narrationArtifacts interface {
	DeleteFile(ctx context.Context, fileID string) error
	ClearBookAudio(ctx context.Context, bookID string) error
}

// WithNarrationArtifacts wires the removal of what finalize wrote
// outside the run's table.
func (s *AudiobookService) WithNarrationArtifacts(a narrationArtifacts) *AudiobookService {
	s.artifacts = a
	return s
}

// WithNarrationSweeper wires the removal of a finished narration's bytes.
func (s *AudiobookService) WithNarrationSweeper(
	fn func(ctx context.Context, book model.Book, run model.Audiobook) error,
) *AudiobookService {
	s.sweepNarration = fn
	return s
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
	if s.settings == nil {
		return AudiobookOptions{}, ErrAudiobooksNotConfigured
	}
	cfg, err := s.settings(ctx)
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
		SourceContentHash:    s.contentHash(ctx, book),
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

// DeleteNarration removes a narration: the run record first, then its
// bytes. The book keeps its EPUB.
//
// The ordering is the invariant, and it is the same one DeleteBook has:
// the location has to be resolved while the row that names it still
// exists, and the bytes go only once the row is gone. Deliberately not a
// deferred cleanup — that would also fire on the failure path, deleting
// the audio out from under a run that still points at it.
func (s *AudiobookService) DeleteNarration(ctx context.Context, book model.Book) error {
	run, err := s.store.GetByBookID(ctx, book.ID)
	if err != nil {
		return err
	}
	if err := s.store.Delete(ctx, book.ID); err != nil {
		return fmt.Errorf("delete narration: %w", err)
	}

	// Everything finalize wrote, not just the run row. It writes four
	// things — the files row, the book's duration and chapters, the run,
	// and each segment's offset — and deleting the run took two of them
	// (the segments cascade). The files row survived pointing at bytes
	// that were about to go, and the reader kept finding a chapter list
	// for a narration that no longer existed (#208).
	//
	// Each failure is logged rather than returned, for the reason the
	// byte sweep gives: a user must not be left with a narration they
	// cannot remove. What is left behind is collectable — an orphaned
	// object by the key sweeper, an orphaned files row by the
	// missing-file purge after a scan.
	if s.artifacts != nil {
		if run.FileID != nil {
			if err := s.artifacts.DeleteFile(ctx, *run.FileID); err != nil {
				slog.Warn("audiobook: delete narration files row", "book", book.ID, "err", err)
			}
		}
		if err := s.artifacts.ClearBookAudio(ctx, book.ID); err != nil {
			slog.Warn("audiobook: clear book audio fields", "book", book.ID, "err", err)
		}
	}

	if s.sweepNarration == nil {
		return nil
	}
	// A failed cleanup leaves an object the orphaned-key sweeper will
	// collect. Failing the call instead would leave the user with a
	// narration they cannot remove.
	if err := s.sweepNarration(ctx, book, run); err != nil {
		slog.Warn("audiobook: narration byte cleanup", "book", book.ID, "err", err)
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
	current := s.contentHash(ctx, book)
	if len(current) == 0 {
		return false
	}
	return !bytes.Equal(current, run.SourceContentHash)
}

func (s *AudiobookService) contentHash(ctx context.Context, book model.Book) []byte {
	if s.hash == nil {
		return nil
	}
	return s.hash(ctx, book)
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
