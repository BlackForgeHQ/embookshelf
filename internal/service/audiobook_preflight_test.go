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
	return NewAudiobookService(AudiobookDeps{Store: &fakeAudiobookStore{}, Books: &epubOpener{src: buildTestEPUB(t, "text")}, Enqueue: &recordingEnqueuer{}, Settings: func(context.Context) (repo.AudiobookConfig, error) { return cfg, nil }, ContentHash: func(context.Context, model.Book) []byte { return hash }})
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

	svc := NewAudiobookService(AudiobookDeps{Store: &fakeAudiobookStore{}, Enqueue: &recordingEnqueuer{}})

	_, err := svc.Preflight(context.Background(), narratableBook(), GenerateOverride{})

	if !errors.Is(err, ErrAudiobooksNotWired) {
		t.Errorf("err = %v, want ErrAudiobooksNotWired", err)
	}
}

// A settings row naming an engine the catalog does not have selects
// nothing, and that is a configuration problem the admin has to see —
// with the id they typed in it, since that is what they have to fix.
//
// The third of Preflight's exits before the format gate. It came back as
// a plain error, which the handler could only answer with its default
// arm: a bare 409, no code, nothing naming the engine (#274).
func TestPreflightSurfacesAnUnusableEngineSelection(t *testing.T) {
	t.Parallel()

	cfg := enabledSettings()
	cfg.Engine = "nonesuch"
	svc := preflightService(t, cfg, nil)

	_, err := svc.Preflight(context.Background(), narratableBook(), GenerateOverride{})

	if !errors.Is(err, repo.ErrUnknownAudiobookEngine) {
		t.Fatalf("err = %v, want repo.ErrUnknownAudiobookEngine so the handler can answer 503", err)
	}
	if errors.Is(err, ErrNotNarratable) {
		t.Errorf("err = %v, want the engine-selection failure, not the format gate", err)
	}
	if !strings.Contains(err.Error(), "nonesuch") {
		t.Errorf("err = %v, want the engine named", err)
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
	svc.d.Store.(*fakeAudiobookStore).run = model.Audiobook{
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

// TestReportNeverStalesAConcludedNonReadyRun is the pin for #322: a
// failed or canceled run has no artifact anything was compared
// against, so it must not receive a staleness verdict even when the
// hashes it happens to carry disagree. The preflight wrapper used to
// have no state gate at all — ready-gating lived only in the handler
// and, implicitly, in the feed's upstream switch — so this was the one
// caller a failed/canceled run's mismatched hash slipped a stale badge
// through.
func TestReportNeverStalesAConcludedNonReadyRun(t *testing.T) {
	t.Parallel()

	for _, state := range []model.AudiobookState{model.AudiobookFailed, model.AudiobookCanceled} {
		t.Run(string(state), func(t *testing.T) {
			svc := preflightService(t, enabledSettings(), []byte{0x02})
			svc.d.Store.(*fakeAudiobookStore).run = model.Audiobook{
				BookID: "b1", State: state, SourceContentHash: []byte{0x01},
			}

			rep, err := svc.Report(context.Background(), narratableBook())
			if err != nil {
				t.Fatalf("Report: %v", err)
			}
			if rep.Stale {
				t.Errorf("a %s run was reported stale — its outcome is already settled, "+
					"and nothing was compared against it (#322)", state)
			}
		})
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
			svc.d.Store.(*fakeAudiobookStore).run = model.Audiobook{
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
// Delete — the ordering invariant, owned by the operation
// ---------------------------------------------------------------------------

// narrationFiles is the files table as the delete path meets it: the
// narration's row is readable until the delete removes it, and gone
// afterwards, which is what makes a location resolved too late resolve to
// nothing.
//
// One double for both halves — the lister a LibraryHandle reads and the
// artifacts the service deletes through — because in production they are
// one table. A pair of doubles that could not disagree with each other is
// what let this ship: the byte sweep was stubbed out wholesale, so nothing
// ever asked it to find a row that had just been deleted (#267).
type narrationFiles struct {
	rows         []model.File
	deletedFiles []string
	clearedAudio []string
	order        *[]string
}

func (f *narrationFiles) ListByBook(context.Context, string) ([]model.File, error) {
	return f.rows, nil
}

func (f *narrationFiles) DeleteFile(_ context.Context, fileID string) error {
	kept := make([]model.File, 0, len(f.rows))
	for _, row := range f.rows {
		if row.ID != fileID {
			kept = append(kept, row)
		}
	}
	f.rows = kept
	f.deletedFiles = append(f.deletedFiles, fileID)
	f.note("file")
	return nil
}

func (f *narrationFiles) ClearBookAudio(_ context.Context, bookID string) error {
	f.clearedAudio = append(f.clearedAudio, bookID)
	f.note("audio")
	return nil
}

func (f *narrationFiles) has(fileID string) bool {
	for _, row := range f.rows {
		if row.ID == fileID {
			return true
		}
	}
	return false
}

func (f *narrationFiles) note(step string) {
	if f.order != nil {
		*f.order = append(*f.order, step)
	}
}

// The book, its text, and the narration made from it. The narration sits
// at the key NarrationKey derives for this book, because that is where
// finalize put it and where a regeneration will put the next one.
const narrationKey = "An Author/A Book/A Book.mp3"

func narrationRows() (string, *narrationFiles) {
	const fileID = "file-mp3"
	return fileID, &narrationFiles{rows: []model.File{
		{ID: "file-epub", Location: "An Author/A Book/book.epub", Format: "EPUB"},
		{ID: fileID, Location: narrationKey, Format: "MP3"},
	}}
}

func readyRun(fileID string) *fakeAudiobookStore {
	return &fakeAudiobookStore{run: model.Audiobook{
		BookID: "b1", State: model.AudiobookReady, FileID: &fileID,
	}}
}

// narratedLibrary is a library holding that book, on the backend kind the
// caller names.
func narratedLibrary(files *narrationFiles, objectStore bool) (*LibraryHandle, *deleteRecordingStorage, *recordingOrphans) {
	store := &deleteRecordingStorage{objectStore: objectStore}
	orphans := &recordingOrphans{}
	return &LibraryHandle{
		Library: model.Library{ID: "lib1"},
		Storage: store,
		files:   files,
		orphans: orphans,
	}, store, orphans
}

func deletingService(files *narrationFiles, run *fakeAudiobookStore, libs LibraryStore) *AudiobookService {
	return NewAudiobookService(AudiobookDeps{
		Store:          run,
		Enqueue:        &recordingEnqueuer{},
		Artifacts:      files,
		NarrationBytes: NewLibraryNarrationBytes(libs),
	})
}

// An object-store library defers its deletes to the orphan sweeper
// (ADR-0005), so "the bytes are gone" means the key was queued for it. A
// narration that is never queued is half a gigabyte billed monthly for
// something nobody can play (ADR-0025 §6).
func TestDeleteNarrationRemovesTheAudioFromAnObjectStoreLibrary(t *testing.T) {
	t.Parallel()

	fileID, files := narrationRows()
	handle, store, orphans := narratedLibrary(files, true)

	svc := deletingService(files, readyRun(fileID), &fakeLibStore{handle: handle})
	if err := svc.DeleteNarration(context.Background(), narratableBook()); err != nil {
		t.Fatalf("DeleteNarration: %v", err)
	}

	if len(orphans.rows) != 1 {
		t.Fatalf("queued %d keys for the sweeper, want the narration's own — its bytes outlive the row otherwise",
			len(orphans.rows))
	}
	if orphans.rows[0].Key != narrationKey {
		t.Errorf("queued %q, want %q", orphans.rows[0].Key, narrationKey)
	}
	if orphans.rows[0].LibraryID != "lib1" {
		t.Errorf("queued against library %q, want lib1", orphans.rows[0].LibraryID)
	}
	if len(store.deleted) != 0 {
		t.Errorf("removed %v inline; the grace window exists so an in-flight presigned play finishes", store.deleted)
	}
}

// A local library owns its bytes outright, so the file goes now. The
// storage double is asked, at the moment of the delete, whether the files
// row is already gone — an ordering assertion about when the bytes went,
// not about a call log the doubles agreed on between themselves.
func TestDeleteNarrationRemovesTheAudioFromALocalLibrary(t *testing.T) {
	t.Parallel()

	fileID, files := narrationRows()
	handle, store, _ := narratedLibrary(files, false)
	store.rowGone = func() bool { return !files.has(fileID) }

	svc := deletingService(files, readyRun(fileID), &fakeLibStore{handle: handle})
	if err := svc.DeleteNarration(context.Background(), narratableBook()); err != nil {
		t.Fatalf("DeleteNarration: %v", err)
	}

	if len(store.deleted) != 1 || store.deleted[0] != narrationKey {
		t.Fatalf("deleted %v, want just %q — anything else is a file the next scan re-indexes",
			store.deleted, narrationKey)
	}
	if len(store.sawRowGone) != 1 || !store.sawRowGone[0] {
		t.Errorf("the bytes went while the files row still pointed at them (%v)", store.sawRowGone)
	}
}

// The book outlives its narration: deleting the audio must not touch the
// text it was read from. This is why the narration delete is its own
// operation rather than a call to DeleteBookAndBytes, which snapshots
// every location the book owns.
func TestDeleteNarrationLeavesTheBooksOwnFileAlone(t *testing.T) {
	t.Parallel()

	fileID, files := narrationRows()
	handle, store, _ := narratedLibrary(files, false)

	svc := deletingService(files, readyRun(fileID), &fakeLibStore{handle: handle})
	if err := svc.DeleteNarration(context.Background(), narratableBook()); err != nil {
		t.Fatalf("DeleteNarration: %v", err)
	}

	for _, key := range store.deleted {
		if strings.HasSuffix(key, ".epub") {
			t.Errorf("deleted %q — the book is still readable as text", key)
		}
	}
	if len(files.rows) != 1 || files.rows[0].Format != "EPUB" {
		t.Errorf("files rows left = %v, want the EPUB row untouched", files.rows)
	}
}

// Nothing to delete is a 404, and the row must not be deleted on the way
// to finding that out.
func TestDeleteNarrationReportsAMissingRun(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{getErr: repo.ErrNotFound}
	svc := NewAudiobookService(AudiobookDeps{Store: store, Enqueue: &recordingEnqueuer{}})

	err := svc.DeleteNarration(context.Background(), narratableBook())

	if !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if store.deleted {
		t.Error("deleted a run that was never found")
	}
}

// Finalize writes four things: the files row for the generated audio, the
// book's duration and chapter list, the run, and each segment's offset.
// Deleting the run took two of them — the segments cascade — so the files
// row survived pointing at bytes that were about to go, and the reader
// kept finding a chapter list for a narration that no longer existed
// (#208).
func TestDeleteNarrationUndoesEverythingFinalizeWrote(t *testing.T) {
	t.Parallel()

	var order []string
	fileID, files := narrationRows()
	files.order = &order
	handle, store, _ := narratedLibrary(files, false)
	store.rowGone = func() bool { order = append(order, "bytes"); return true }

	run := readyRun(fileID)
	run.onDelete = func() { order = append(order, "row") }

	svc := deletingService(files, run, &fakeLibStore{handle: handle})
	if err := svc.DeleteNarration(context.Background(), narratableBook()); err != nil {
		t.Fatalf("DeleteNarration: %v", err)
	}

	if len(files.deletedFiles) != 1 || files.deletedFiles[0] != fileID {
		t.Errorf("deleted files rows %v, want the one the run pointed at", files.deletedFiles)
	}
	if len(files.clearedAudio) != 1 {
		t.Errorf("cleared audio for %v, want the book — the reader still finds chapters otherwise",
			files.clearedAudio)
	}
	// The files row and the bytes are adjacent because they are one
	// operation; the run row goes first because it is what names the file.
	if strings.Join(order, ",") != "row,file,bytes,audio" {
		t.Errorf("order = %v, want the run row first and the bytes straight after the row that named them", order)
	}
}

// A byte cleanup that fails leaves an orphaned object, which the
// orphaned-key sweeper is for. Failing the call would leave the user with
// a narration they cannot remove — and the rows are already gone by then.
func TestDeleteNarrationSurvivesAByteCleanupFailure(t *testing.T) {
	t.Parallel()

	fileID, files := narrationRows()
	handle, store, _ := narratedLibrary(files, false)
	store.failKey, store.failWith = narrationKey, errors.New("permission denied")

	run := readyRun(fileID)
	svc := deletingService(files, run, &fakeLibStore{handle: handle})

	if err := svc.DeleteNarration(context.Background(), narratableBook()); err != nil {
		t.Errorf("DeleteNarration = %v, want nil — the rows are gone and the bytes are the sweeper's", err)
	}
	if !run.deleted {
		t.Error("the run row was not deleted")
	}
	if files.has(fileID) {
		t.Error("the files row survived a failed byte delete, so the reader still offers audio that is going")
	}
}

// unreachableLibrary is a library whose backend will not resolve — down,
// or misconfigured.
type unreachableLibrary struct{}

func (unreachableLibrary) For(context.Context, string) (*LibraryHandle, error) {
	return nil, errors.New("backend unreachable")
}

// A library we cannot reach is a degraded cleanup, never a blocked delete:
// the rows still go, and the bytes wait for an operator. Same reading
// DeleteBook takes of an unresolvable handle.
func TestDeleteNarrationStillRemovesTheRowsWhenTheLibraryIsUnreachable(t *testing.T) {
	t.Parallel()

	fileID, files := narrationRows()
	run := readyRun(fileID)
	svc := deletingService(files, run, unreachableLibrary{})

	if err := svc.DeleteNarration(context.Background(), narratableBook()); err != nil {
		t.Errorf("DeleteNarration = %v, want nil — an unreachable backend must not pin a narration", err)
	}
	if !run.deleted || files.has(fileID) {
		t.Error("the rows survived a library we could not resolve")
	}
}

// A run that never finished has no files row to delete, and asking for
// one would be a nil dereference.
func TestDeleteNarrationSkipsTheFilesRowWhenTheRunNeverProducedOne(t *testing.T) {
	t.Parallel()

	_, files := narrationRows()
	handle, store, _ := narratedLibrary(files, false)
	run := &fakeAudiobookStore{run: model.Audiobook{BookID: "b1", State: model.AudiobookFailed}}

	svc := deletingService(files, run, &fakeLibStore{handle: handle})
	if err := svc.DeleteNarration(context.Background(), narratableBook()); err != nil {
		t.Fatalf("DeleteNarration: %v", err)
	}

	if len(files.deletedFiles) != 0 {
		t.Errorf("deleted %v, want none — the run never produced a files row", files.deletedFiles)
	}
	if len(store.deleted) != 0 {
		t.Errorf("deleted %v, want none — a failed run left no audio to remove", store.deleted)
	}
	if len(files.clearedAudio) != 1 {
		t.Error("the book's audio fields were left set")
	}
}

// Delete is not the end of the story: the usual reason to remove a
// narration is to make a better one. The delete has to leave the book
// narratable — its text row intact, no run in the way — and it has to
// vacate the very key the next run will write, since regeneration lands on
// the same one by design (ADR-0025 §4).
func TestANarrationCanBeRegeneratedAfterItIsDeleted(t *testing.T) {
	t.Parallel()

	fileID, files := narrationRows()
	handle, store, _ := narratedLibrary(files, false)
	run := readyRun(fileID)

	svc := NewAudiobookService(AudiobookDeps{
		Store:          run,
		Enqueue:        &recordingEnqueuer{},
		Books:          &epubOpener{src: buildTestEPUB(t, "text")},
		Settings:       func(context.Context) (repo.AudiobookConfig, error) { return enabledSettings(), nil },
		Artifacts:      files,
		NarrationBytes: NewLibraryNarrationBytes(&fakeLibStore{handle: handle}),
	})

	book := narratableBook()
	if err := svc.DeleteNarration(context.Background(), book); err != nil {
		t.Fatalf("DeleteNarration: %v", err)
	}

	// What the freed key was, and where the next run will write.
	if len(store.deleted) != 1 || store.deleted[0] != handle.DerivedKey(book, DerivedNarration) {
		t.Fatalf("deleted %v, want the key regeneration reuses (%q)", store.deleted, handle.DerivedKey(book, DerivedNarration))
	}
	if err := svc.Generate(context.Background(), book, GenerateOverride{}); err != nil {
		t.Fatalf("Generate after delete: %v", err)
	}
	if !run.started {
		t.Error("no new run was planned, so the narration cannot be remade")
	}
}
