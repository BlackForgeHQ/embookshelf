// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/provider"
	"github.com/blackforge/embookshelf/internal/sidecar"
)

var errStepFailed = errors.New("permission denied")

// newDegradedWriter is newPipelineWriter over a format with no in-file
// embed target, so the sidecar is the only portable record — the case
// where losing it silently matters most.
func newDegradedWriter(
	t *testing.T,
	books *fakeBookWriter,
	side *recordingSidecarWriter,
	dispatch EmbedderDispatcher,
) *MetadataWriter {
	t.Helper()
	mw, _ := newPipelineWriter(t, books, side, dispatch)
	return mw
}

func degradedBook() model.Book {
	return model.Book{ID: "b1", LibraryID: "lib1", Path: "books/x.cbz", Format: "CBZ", Title: "X"}
}

// degradeOf runs the split every production caller runs: fatal means the
// call did not happen, and none of the cases below is about that. Returns
// nil when nothing degraded.
func degradeOf(t *testing.T, what string, err error) *Degraded {
	t.Helper()
	deg, fatal := Degradation(err)
	if fatal {
		t.Fatalf("%s: %v", what, err)
	}
	return deg
}

func assertWarns(t *testing.T, deg *Degraded, step string) {
	t.Helper()
	for _, w := range deg.Warnings() {
		if strings.Contains(w, step) {
			return
		}
	}
	t.Fatalf("warnings %v mention no %q failure", deg.Warnings(), step)
}

// A metadata edit is four steps and only the first one fails the call. It
// used to be the only one that said anything: Write returned nil the moment
// the books row landed, so a caller holding err == nil had learned "the DB
// was updated" and nothing else, while the sidecar may have failed, the
// in-file embed may have failed, and on an S3-backed library or a format
// with no in-file write target the sidecar IS the portable record, which is
// the whole point of ADR-0001. The degradation now travels on the error, so
// err == nil means the whole plan landed; these tests pin that.

func TestMetadataWriterReportsSidecarFailure(t *testing.T) {
	books := &fakeBookWriter{}
	side := &recordingSidecarWriter{err: errStepFailed}
	mw := newDegradedWriter(t, books, side, nil)

	out, err := mw.Write(context.Background(), degradedBook(), TriggerManualEdit)
	deg := degradeOf(t, "Write — the DB write succeeded", err)
	if out.SidecarWritten {
		t.Error("Outcome.SidecarWritten is true despite the write failing")
	}
	if deg == nil {
		t.Fatal("a failed sidecar write returned a nil error — the caller reads that as a clean save")
	}
	assertWarns(t, deg, "sidecar")
}

// TestMetadataWriterSidecarModeReflectsReality — SidecarMode used to be
// assigned from the plan before the write ran, so it named a mode nothing
// had been written in.
func TestMetadataWriterSidecarModeReflectsReality(t *testing.T) {
	books := &fakeBookWriter{}
	side := &recordingSidecarWriter{err: errStepFailed}
	mw := newDegradedWriter(t, books, side, nil)

	out, _ := mw.Write(context.Background(), degradedBook(), TriggerManualEdit)
	if out.SidecarMode != "" {
		t.Fatalf("SidecarMode = %q after a failed write, want empty", out.SidecarMode)
	}
}

func TestMetadataWriterSidecarSuccessIsNotDegraded(t *testing.T) {
	books := &fakeBookWriter{}
	side := &recordingSidecarWriter{}
	mw := newDegradedWriter(t, books, side, nil)

	// The nil check is the assertion: a clean write is the only thing that
	// returns one, so there is no separate "not degraded" question to ask.
	out, err := mw.Write(context.Background(), degradedBook(), TriggerManualEdit)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !out.SidecarWritten {
		t.Error("SidecarWritten is false after a successful write")
	}
	if out.SidecarMode != sidecar.ModeFull {
		t.Errorf("SidecarMode = %q, want full mirror on a backend library", out.SidecarMode)
	}
}

// TestMetadataWriterDBFailureStillErrors — the one step that must keep
// failing loudly. Losing the books row means the edit did not happen.
func TestMetadataWriterDBFailureStillErrors(t *testing.T) {
	books := &fakeBookWriter{err: errStepFailed}
	mw := newDegradedWriter(t, books, &recordingSidecarWriter{}, nil)

	if _, err := mw.Write(context.Background(), degradedBook(), TriggerManualEdit); err == nil {
		t.Fatal("Write returned nil despite the DB write failing")
	}
}

// TestMetadataWriterWarningsNameTheStep — the warnings reach a user, so they
// have to say which part of the save did not happen, not just "something".
func TestMetadataWriterWarningsNameTheStep(t *testing.T) {
	books := &fakeBookWriter{}
	side := &recordingSidecarWriter{err: errStepFailed}
	mw := newDegradedWriter(t, books, side, nil)

	_, err := mw.Write(context.Background(), degradedBook(), TriggerManualEdit)
	warns := degradeOf(t, "Write", err).Warnings()
	if len(warns) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warns)
	}
	if !strings.Contains(warns[0], errStepFailed.Error()) {
		t.Errorf("warning %q does not carry the cause", warns[0])
	}
}

// TestMetadataWriterReportsInFileFailure — a format the writer cannot embed
// at all is normal and silent (the sidecar carries a full mirror instead,
// ADR-0001 §3). An embed that was attempted and broke is not: the file on
// disk no longer matches the edit, and only the sidecar is keeping it.
func TestMetadataWriterReportsInFileFailure(t *testing.T) {
	books := &fakeBookWriter{}
	side := &recordingSidecarWriter{}
	emb := &fakeEmbedder{err: errStepFailed}
	mw := newDegradedWriter(t, books, side,
		func(string) (fileproc.Embedder, error) { return emb, nil })

	book := degradedBook()
	book.Format = "EPUB"
	out, err := mw.Write(context.Background(), book, TriggerManualEdit)
	deg := degradeOf(t, "Write — the DB write succeeded", err)
	if out.InFileWritten {
		t.Error("InFileWritten is true after the embed failed")
	}
	if deg == nil {
		t.Fatal("a failed in-file write returned a nil error — the caller reads that as a clean save")
	}
	assertWarns(t, deg, "in-file")

	// The compensating fallback still has to fire.
	if out.SidecarMode != sidecar.ModeFull {
		t.Errorf("SidecarMode = %q, want full mirror after a failed embed", out.SidecarMode)
	}
}

// TestMetadataWriterUnsupportedFormatIsNotDegraded — CBZ and friends have
// no in-file target. Reporting that on every edit would train users to
// ignore the warnings that matter.
func TestMetadataWriterUnsupportedFormatIsNotDegraded(t *testing.T) {
	books := &fakeBookWriter{}
	side := &recordingSidecarWriter{}
	mw := newDegradedWriter(t, books, side, nil) // dispatch refuses

	if _, err := mw.Write(context.Background(), degradedBook(), TriggerManualEdit); err != nil {
		t.Fatalf("an unsupported format was reported as degraded: %v", err)
	}
}

// --- the callers -------------------------------------------------------

// A degradation nothing reads is a log line with extra steps. Both
// edit-side services now pass it out on the error their caller already has
// to handle, so the handler can put the warnings on the response; ApplyMatch
// used to discard the Outcome at the service, which is how apply match
// became the one edit endpoint of three that could answer a lost Sidecar
// with an unqualified 200.

func TestUpdateBookMetadataReportsTheDegradeOnItsError(t *testing.T) {
	books := &fakeBookWriter{}
	mw := newDegradedWriter(t, books, &recordingSidecarWriter{err: errStepFailed}, nil)
	// Neither repo is reachable from UpdateBookMetadata: the edit goes to
	// the writer and nowhere else.
	svc := NewLibraryService(nil, nil, LibraryServiceDeps{}, mw)

	deg := degradeOf(t, "UpdateBookMetadata — the DB write succeeded",
		svc.UpdateBookMetadata(context.Background(), degradedBook()))
	if deg == nil {
		t.Fatal("a failed sidecar write returned a nil error — the caller reads that as a clean save")
	}
	assertWarns(t, deg, "sidecar")
}

func TestApplyMatchReportsTheDegradeOnItsError(t *testing.T) {
	books := &fakeBookStore{}
	mw, _ := newPipelineWriter(t, books, &recordingSidecarWriter{err: errStepFailed}, nil)
	svc := NewEnrichmentService(nil, newFakeProviderSettings(), books, &fakeCoverStore{}, mw)

	got, err := svc.ApplyMatch(context.Background(), degradedBook(),
		provider.Match{Title: "Provider Title"}, ApplyOptions{}, TriggerApplyEnrichment)
	deg := degradeOf(t, "ApplyMatch — the DB write succeeded", err)
	if got.Title != "Provider Title" {
		t.Errorf("Title = %q, want the applied match — a degrade accompanies the book, it does not replace it", got.Title)
	}
	if deg == nil {
		t.Fatal("a failed sidecar write returned a nil error — the caller reads that as a clean save")
	}
	assertWarns(t, deg, "sidecar")
}

// Auto-enrichment is DB-only by ADR-0001 §3, so there is no later step that
// could degrade — which is why AutoEnrich can hand its error straight to a
// caller that treats any error as a failed job.
func TestApplyMatchAutoEnrichmentIsNeverDegraded(t *testing.T) {
	books := &fakeBookStore{}
	mw, _ := newPipelineWriter(t, books, &recordingSidecarWriter{err: errStepFailed}, nil)
	svc := NewEnrichmentService(nil, newFakeProviderSettings(), books, &fakeCoverStore{}, mw)

	if _, err := svc.ApplyMatch(context.Background(), degradedBook(),
		provider.Match{Title: "Provider Title"}, ApplyOptions{}, TriggerAutoEnrichment); err != nil {
		t.Fatalf("auto-enrichment reported a write step it never attempted: %v", err)
	}
}
