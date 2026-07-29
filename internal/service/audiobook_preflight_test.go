// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/tts"
)

// Every gate below is reachable here, through fakes, with no Postgres
// and no HTTP recorder — which is the point of moving them off the
// handler (#191).
func enabledSettings() repo.AudiobookConfig {
	cfg := repo.DefaultAudiobookConfig()
	cfg.Enabled = true
	cfg.Engine = string(tts.EngineOpenAI)
	cfg.OpenAI.Enabled = true
	cfg.OpenAI.APIKey = "sk-test"
	cfg.OpenAI.DefaultVoice = "alloy"
	cfg.OpenAI.Model = "tts-1"
	cfg.OpenAI.PricePerMillionChars = 15
	return cfg
}

func preflightService(t *testing.T, cfg repo.AudiobookConfig, hash []byte) *AudiobookService {
	t.Helper()
	return NewAudiobookService(&fakeAudiobookStore{}, &epubOpener{src: buildTestEPUB(t, "text")}, &recordingEnqueuer{}).
		WithSettings(func(context.Context) (repo.AudiobookConfig, error) { return cfg, nil }).
		WithContentHash(func(context.Context, model.Book) []byte { return hash })
}

// The run records the engine, voice, model and price the settings row
// named, so what a run was made with is a fact rather than a guess.
func TestPreflightResolvesTheRunFromTheSettingsRow(t *testing.T) {
	t.Parallel()

	svc := preflightService(t, enabledSettings(), []byte{0xAB})

	opts, err := svc.Preflight(context.Background(), narratableBook(), GenerateOverride{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}

	if opts.Engine != string(tts.EngineOpenAI) || opts.Voice != "alloy" || opts.Model != "tts-1" {
		t.Errorf("resolved %q/%q/%q, want the settings row's own", opts.Engine, opts.Voice, opts.Model)
	}
	if opts.PricePerMillionChars != 15 {
		t.Errorf("price = %v, want the engine's own 15", opts.PricePerMillionChars)
	}
	if string(opts.SourceContentHash) != string([]byte{0xAB}) {
		t.Errorf("hash = %x, want the book's current one — provenance is what the staleness badge reads",
			opts.SourceContentHash)
	}
}

// The generate dialog exists because a different narrator for a
// different novel is most of the product (ADR-0026 §6).
func TestPreflightTakesTheDialogsOverrides(t *testing.T) {
	t.Parallel()

	svc := preflightService(t, enabledSettings(), nil)

	opts, err := svc.Preflight(context.Background(), narratableBook(),
		GenerateOverride{Voice: "shimmer", Model: "tts-1-hd"})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}

	if opts.Voice != "shimmer" || opts.Model != "tts-1-hd" {
		t.Errorf("resolved %q/%q, want the dialog's overrides", opts.Voice, opts.Model)
	}
}

func TestPreflightRefusesWhenTheFeatureIsOff(t *testing.T) {
	t.Parallel()

	cfg := enabledSettings()
	cfg.Enabled = false
	svc := preflightService(t, cfg, nil)

	_, err := svc.Preflight(context.Background(), narratableBook(), GenerateOverride{})

	if !errors.Is(err, ErrAudiobooksDisabled) {
		t.Errorf("err = %v, want ErrAudiobooksDisabled so the handler can answer 503", err)
	}
}

// The second of the Narratable format's three gates. A re-import can
// change a book's format between the UI offering the button and this
// running (ADR-0028 §4).
func TestPreflightRefusesABookWithNoTextToRead(t *testing.T) {
	t.Parallel()

	svc := preflightService(t, enabledSettings(), nil)
	book := narratableBook()
	book.Format = "CBZ"

	_, err := svc.Preflight(context.Background(), book, GenerateOverride{})

	if !errors.Is(err, ErrNotNarratable) {
		t.Errorf("err = %v, want ErrNotNarratable so the handler can answer 415", err)
	}
}

// A service with no settings reader cannot answer, and says so as its
// own error rather than by panicking on a nil call.
func TestPreflightRefusesWhenNothingIsConfigured(t *testing.T) {
	t.Parallel()

	svc := NewAudiobookService(&fakeAudiobookStore{}, nil, &recordingEnqueuer{})

	_, err := svc.Preflight(context.Background(), narratableBook(), GenerateOverride{})

	if !errors.Is(err, ErrAudiobooksNotConfigured) {
		t.Errorf("err = %v, want ErrAudiobooksNotConfigured", err)
	}
}

// A settings row naming an engine with no key selects nothing, and that
// is a configuration problem the admin has to see.
func TestPreflightSurfacesAnUnusableEngineSelection(t *testing.T) {
	t.Parallel()

	cfg := enabledSettings()
	cfg.Engine = "nonesuch"
	svc := preflightService(t, cfg, nil)

	_, err := svc.Preflight(context.Background(), narratableBook(), GenerateOverride{})

	if err == nil {
		t.Fatal("Preflight accepted a settings row whose engine cannot run")
	}
	if errors.Is(err, ErrNotNarratable) {
		t.Errorf("err = %v, want the engine-selection failure, not the format gate", err)
	}
}

// ---------------------------------------------------------------------------
// Staleness — derived where the run is, not in the DTO
// ---------------------------------------------------------------------------

// The audio is of the older text. Surfaced, never acted on: throwing
// away hours of narration because someone re-uploaded a better copy
// would be worse than telling them.
func TestReportMarksANarrationStaleAgainstANewerFile(t *testing.T) {
	t.Parallel()

	svc := preflightService(t, enabledSettings(), []byte{0x02})
	svc.store.(*fakeAudiobookStore).run = model.Audiobook{
		BookID: "b1", State: model.AudiobookReady, SourceContentHash: []byte{0x01},
	}

	rep, err := svc.Report(context.Background(), narratableBook())
	if err != nil {
		t.Fatalf("Report: %v", err)
	}

	if !rep.Stale {
		t.Error("a narration made from a different file is not reported stale")
	}
}

func TestReportDoesNotGuessStalenessWithoutBothHashes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		run     []byte
		current []byte
	}{
		{"run predates provenance", nil, []byte{0x02}},
		{"current file unreadable", []byte{0x01}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := preflightService(t, enabledSettings(), tc.current)
			svc.store.(*fakeAudiobookStore).run = model.Audiobook{
				BookID: "b1", State: model.AudiobookReady, SourceContentHash: tc.run,
			}

			rep, err := svc.Report(context.Background(), narratableBook())
			if err != nil {
				t.Fatalf("Report: %v", err)
			}
			if rep.Stale {
				t.Error("reported stale on a comparison it could not make — the badge would be a lie")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Delete — the ordering invariant, owned where the delete is
// ---------------------------------------------------------------------------

// Resolve the key before the row goes: deleting the run is what makes
// the location unknowable, exactly as DeleteBook has it.
func TestDeleteNarrationResolvesTheLocationBeforeTheRowGoes(t *testing.T) {
	t.Parallel()

	var order []string
	store := &fakeAudiobookStore{run: model.Audiobook{BookID: "b1", State: model.AudiobookReady}}
	store.onDelete = func() { order = append(order, "row") }
	svc := NewAudiobookService(store, nil, &recordingEnqueuer{}).
		WithNarrationSweeper(func(context.Context, model.Book, model.Audiobook) error {
			order = append(order, "bytes")
			return nil
		})

	if err := svc.DeleteNarration(context.Background(), narratableBook()); err != nil {
		t.Fatalf("DeleteNarration: %v", err)
	}

	if strings.Join(order, ",") != "row,bytes" {
		t.Errorf("order = %v, want the row deleted first and the bytes after", order)
	}
}

// A byte cleanup that fails leaves an orphaned object, which the
// orphaned-key sweeper is for. Failing the call would leave the user
// with a narration they cannot remove.
func TestDeleteNarrationSurvivesAByteCleanupFailure(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{run: model.Audiobook{BookID: "b1", State: model.AudiobookReady}}
	svc := NewAudiobookService(store, nil, &recordingEnqueuer{}).
		WithNarrationSweeper(func(context.Context, model.Book, model.Audiobook) error {
			return errors.New("backend refused")
		})

	if err := svc.DeleteNarration(context.Background(), narratableBook()); err != nil {
		t.Errorf("DeleteNarration = %v, want nil — the row is gone and the bytes are the sweeper's", err)
	}
	if !store.deleted {
		t.Error("the run row was not deleted")
	}
}

// Nothing to delete is a 404, and the row must not be deleted on the way
// to finding that out.
func TestDeleteNarrationReportsAMissingRun(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{getErr: repo.ErrNotFound}
	svc := NewAudiobookService(store, nil, &recordingEnqueuer{})

	err := svc.DeleteNarration(context.Background(), narratableBook())

	if !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if store.deleted {
		t.Error("deleted a run that was never found")
	}
}
