// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
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

func assertWarns(t *testing.T, out Outcome, step string) {
	t.Helper()
	for _, w := range out.Warnings() {
		if strings.Contains(w, step) {
			return
		}
	}
	t.Fatalf("warnings %v mention no %q failure", out.Warnings(), step)
}

// A metadata edit is four steps and only the first one can fail loudly.
// Write returns nil the moment the books row lands, so a caller holding
// err == nil has learned "the DB was updated" and nothing else. The sidecar
// may have failed, the in-file embed may have failed, the folder may still
// be at its old name — and on an S3-backed library or a format with no
// in-file write target the sidecar IS the portable record, which is the
// whole point of ADR-0001. Outcome was the only channel for that, no
// production caller read it, and it was not even accurate: SidecarMode was
// set from the plan before the write was attempted.

func TestMetadataWriterReportsSidecarFailure(t *testing.T) {
	books := &fakeBookWriter{}
	side := &recordingSidecarWriter{err: errStepFailed}
	mw := newDegradedWriter(t, books, side, nil)

	out, err := mw.Write(context.Background(), degradedBook(), TriggerManualEdit)
	if err != nil {
		t.Fatalf("Write = %v, want nil — the DB write succeeded", err)
	}
	if out.SidecarWritten {
		t.Error("Outcome.SidecarWritten is true despite the write failing")
	}
	if !out.Degraded() {
		t.Fatal("Outcome.Degraded() is false after a failed sidecar write")
	}
	assertWarns(t, out, "sidecar")
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
	if out.Degraded() {
		t.Errorf("Degraded() is true on a clean write: %v", out.Warnings())
	}
	if len(out.Warnings()) != 0 {
		t.Errorf("warnings on a clean write: %v", out.Warnings())
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

	out, _ := mw.Write(context.Background(), degradedBook(), TriggerManualEdit)
	warns := out.Warnings()
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
	if err != nil {
		t.Fatalf("Write = %v, want nil — the DB write succeeded", err)
	}
	if out.InFileWritten {
		t.Error("InFileWritten is true after the embed failed")
	}
	if !out.Degraded() {
		t.Fatal("Degraded() is false after a failed in-file write")
	}
	assertWarns(t, out, "in-file")

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

	out, err := mw.Write(context.Background(), degradedBook(), TriggerManualEdit)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if out.Degraded() {
		t.Fatalf("an unsupported format was reported as degraded: %v", out.Warnings())
	}
}
