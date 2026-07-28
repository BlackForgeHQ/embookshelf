// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// --- fakes -------------------------------------------------------------

type fakeBookDropInserter struct {
	mu       sync.Mutex
	calls    []insertCall
	err      error
	nextID   int
	blockOn  chan struct{} // when non-nil, Insert waits on it
	released chan struct{}
}

type insertCall struct {
	path   string
	format string
	size   int64
}

func (f *fakeBookDropInserter) Insert(_ context.Context, path, format string, size int64) (model.BookDropItem, error) {
	if f.blockOn != nil {
		close(f.released)
		<-f.blockOn
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return model.BookDropItem{}, f.err
	}
	f.calls = append(f.calls, insertCall{path: path, format: format, size: size})
	f.nextID++
	return model.BookDropItem{ID: "item-" + string(rune('0'+f.nextID)), Path: path, Format: format, FileSize: size}, nil
}

func (f *fakeBookDropInserter) sole(t *testing.T) insertCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 1 {
		t.Fatalf("Insert calls = %d, want 1 (%+v)", len(f.calls), f.calls)
	}
	return f.calls[0]
}

// fakeDispatcher records the item ids Intake/Accept handed to the worker
// pool. It never runs a processor — that is the point: the request
// goroutine only enqueues.
type fakeDispatcher struct {
	mu  sync.Mutex
	ids []string
	err error
}

func (f *fakeDispatcher) Enqueue(_ context.Context, a jobs.Args) error {
	args, ok := a.(jobs.BookDropIngestArgs)
	if !ok {
		return fmt.Errorf("unexpected job args %T", a)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.ids = append(f.ids, args.ItemID)
	return nil
}

// --- harness -----------------------------------------------------------

type intakeHarness struct {
	svc      *BookDropService
	inserter *fakeBookDropInserter
	disp     *fakeDispatcher
	dir      string
}

func newIntakeHarness(t *testing.T) *intakeHarness {
	t.Helper()
	h := &intakeHarness{
		inserter: &fakeBookDropInserter{},
		disp:     &fakeDispatcher{},
		dir:      t.TempDir(),
	}
	h.svc = &BookDropService{bookdropPath: h.dir, enq: h.disp}
	h.svc.intake = h.inserter
	return h
}

// writeStaged drops a file straight into the staging dir, standing in for
// something the watcher would find.
func (h *intakeHarness) writeStaged(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(h.dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// --- Intake (watcher path) ---------------------------------------------

func TestBookDropIntakeRegistersFile(t *testing.T) {
	h := newIntakeHarness(t)
	p := h.writeStaged(t, "dune.epub", "epub bytes")

	item, created, err := h.svc.Intake(context.Background(), p)
	if err != nil {
		t.Fatalf("Intake: %v", err)
	}
	if !created {
		t.Fatal("created = false, want true")
	}
	got := h.inserter.sole(t)
	if got.path != p {
		t.Errorf("path = %q, want %q", got.path, p)
	}
	if got.format != "EPUB" {
		t.Errorf("format = %q, want EPUB", got.format)
	}
	if got.size != int64(len("epub bytes")) {
		t.Errorf("size = %d, want %d", got.size, len("epub bytes"))
	}
	if len(h.disp.ids) != 1 || h.disp.ids[0] != item.ID {
		t.Errorf("dispatched = %v, want [%s]", h.disp.ids, item.ID)
	}
}

// TestBookDropIntakeSizesFromDisk — the size must come from a stat taken
// under the lock, not from whatever the caller measured earlier. A file that
// grew or shrank since the caller looked would otherwise be recorded wrong.
func TestBookDropIntakeSizesFromDisk(t *testing.T) {
	h := newIntakeHarness(t)
	p := h.writeStaged(t, "grown.epub", "short")
	if err := os.WriteFile(p, []byte("much longer content now"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if _, _, err := h.svc.Intake(context.Background(), p); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	if got := h.inserter.sole(t).size; got != int64(len("much longer content now")) {
		t.Fatalf("size = %d, want %d", got, len("much longer content now"))
	}
}

func TestBookDropIntakeRejectsUnsupported(t *testing.T) {
	h := newIntakeHarness(t)
	p := h.writeStaged(t, "notes.txt", "hello")

	_, created, err := h.svc.Intake(context.Background(), p)
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err = %v, want ErrUnsupportedFormat", err)
	}
	if created {
		t.Error("created = true for an unsupported file")
	}
	if len(h.inserter.calls) != 0 {
		t.Errorf("inserted an unsupported file: %+v", h.inserter.calls)
	}
}

// TestBookDropIntakeRejectsVanishedFile — the file may be deleted between
// discovery and intake (a wipe, or the user pulling it back out). Inserting a
// row for it would create an item that can never process.
func TestBookDropIntakeRejectsVanishedFile(t *testing.T) {
	h := newIntakeHarness(t)
	p := filepath.Join(h.dir, "ghost.epub")

	if _, _, err := h.svc.Intake(context.Background(), p); err == nil {
		t.Fatal("Intake accepted a file that does not exist")
	}
	if len(h.inserter.calls) != 0 {
		t.Errorf("inserted a vanished file: %+v", h.inserter.calls)
	}
}

func TestBookDropIntakeSkipsDuplicate(t *testing.T) {
	h := newIntakeHarness(t)
	h.inserter.err = repo.ErrAlreadyExists
	p := h.writeStaged(t, "dune.epub", "bytes")

	_, created, err := h.svc.Intake(context.Background(), p)
	if err != nil {
		t.Fatalf("Intake: %v", err)
	}
	if created {
		t.Error("created = true for an already-tracked file")
	}
	if len(h.disp.ids) != 0 {
		t.Errorf("dispatched a duplicate: %v", h.disp.ids)
	}
}

func TestBookDropIntakeSkipsDispatchWhenInsertFails(t *testing.T) {
	h := newIntakeHarness(t)
	h.inserter.err = errors.New("db down")
	p := h.writeStaged(t, "dune.epub", "bytes")

	if _, _, err := h.svc.Intake(context.Background(), p); err == nil {
		t.Fatal("Intake returned nil despite an insert failure")
	}
	if len(h.disp.ids) != 0 {
		t.Errorf("dispatched despite failed insert: %v", h.disp.ids)
	}
}

// TestBookDropIntakeSurvivesDispatchFailure — the row is already committed;
// the watcher's next tick re-dispatches, so a queue hiccup is not the
// caller's problem.
func TestBookDropIntakeSurvivesDispatchFailure(t *testing.T) {
	h := newIntakeHarness(t)
	h.disp.err = errors.New("river down")
	p := h.writeStaged(t, "dune.epub", "bytes")

	_, created, err := h.svc.Intake(context.Background(), p)
	if err != nil {
		t.Fatalf("Intake = %v, want nil — the row was written", err)
	}
	if !created {
		t.Error("created = false despite a successful insert")
	}
}

// --- Accept (upload path) ----------------------------------------------

func TestBookDropAcceptSavesAndRegisters(t *testing.T) {
	h := newIntakeHarness(t)
	item, err := h.svc.Accept(context.Background(), "Dune.epub", strings.NewReader("epub bytes"))
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	got := h.inserter.sole(t)
	if filepath.Dir(got.path) != h.dir {
		t.Fatalf("saved to %q, want inside %q", got.path, h.dir)
	}
	b, err := os.ReadFile(got.path)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if string(b) != "epub bytes" {
		t.Errorf("content = %q", b)
	}
	if got.size != int64(len("epub bytes")) {
		t.Errorf("size = %d, want %d", got.size, len("epub bytes"))
	}
	if len(h.disp.ids) != 1 || h.disp.ids[0] != item.ID {
		t.Errorf("dispatched = %v, want [%s]", h.disp.ids, item.ID)
	}
}

func TestBookDropAcceptRejectsUnsupported(t *testing.T) {
	h := newIntakeHarness(t)
	if _, err := h.svc.Accept(context.Background(), "notes.txt", strings.NewReader("x")); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err = %v, want ErrUnsupportedFormat", err)
	}
	entries, _ := os.ReadDir(h.dir)
	if len(entries) != 0 {
		t.Fatalf("wrote %d files for a rejected upload", len(entries))
	}
}

func TestBookDropAcceptRequiresConfiguredPath(t *testing.T) {
	h := newIntakeHarness(t)
	h.svc.bookdropPath = ""
	if _, err := h.svc.Accept(context.Background(), "dune.epub", strings.NewReader("x")); !errors.Is(err, ErrBookDropDisabled) {
		t.Fatalf("err = %v, want ErrBookDropDisabled", err)
	}
}

// TestBookDropAcceptContainsTraversal — the filename is attacker-controlled.
// Whatever it contains, the bytes must land inside the staging directory.
func TestBookDropAcceptContainsTraversal(t *testing.T) {
	for _, name := range []string{
		"../../etc/passwd.epub",
		"..%2F..%2Fx.epub",
		"/absolute/path/x.epub",
		"sub/dir/x.epub",
	} {
		t.Run(name, func(t *testing.T) {
			h := newIntakeHarness(t)
			if _, err := h.svc.Accept(context.Background(), name, strings.NewReader("x")); err != nil {
				t.Fatalf("Accept(%q): %v", name, err)
			}
			got := h.inserter.sole(t).path
			if filepath.Dir(got) != h.dir {
				t.Fatalf("saved to %q, escaped %q", got, h.dir)
			}
		})
	}
}

// TestBookDropAcceptDoesNotHideFile — a name of ".epub" would otherwise
// produce a dotfile the operator never sees in the staging directory.
func TestBookDropAcceptDoesNotHideFile(t *testing.T) {
	h := newIntakeHarness(t)
	if _, err := h.svc.Accept(context.Background(), ".epub", strings.NewReader("x")); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if base := filepath.Base(h.inserter.sole(t).path); strings.HasPrefix(base, ".") {
		t.Fatalf("saved as a hidden file: %q", base)
	}
}

// TestBookDropAcceptSameNameTwice — uploading the same title twice must not
// have the second overwrite the first.
func TestBookDropAcceptSameNameTwice(t *testing.T) {
	h := newIntakeHarness(t)
	ctx := context.Background()
	if _, err := h.svc.Accept(ctx, "dune.epub", strings.NewReader("first")); err != nil {
		t.Fatalf("first Accept: %v", err)
	}
	if _, err := h.svc.Accept(ctx, "dune.epub", strings.NewReader("second")); err != nil {
		t.Fatalf("second Accept: %v", err)
	}
	if len(h.inserter.calls) != 2 {
		t.Fatalf("insert calls = %d, want 2", len(h.inserter.calls))
	}
	if h.inserter.calls[0].path == h.inserter.calls[1].path {
		t.Fatalf("both uploads landed on %q", h.inserter.calls[0].path)
	}
	for i, want := range []string{"first", "second"} {
		b, err := os.ReadFile(h.inserter.calls[i].path)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if string(b) != want {
			t.Errorf("file %d = %q, want %q", i, b, want)
		}
	}
}

// TestBookDropAcceptRemovesFileWhenInsertFails — bytes with no row are
// invisible to the UI but still occupy the staging directory, and the
// watcher would later adopt them as a surprise item.
func TestBookDropAcceptRemovesFileWhenInsertFails(t *testing.T) {
	h := newIntakeHarness(t)
	h.inserter.err = errors.New("db down")

	if _, err := h.svc.Accept(context.Background(), "dune.epub", strings.NewReader("x")); err == nil {
		t.Fatal("Accept returned nil despite an insert failure")
	}
	entries, err := os.ReadDir(h.dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("left %d orphan file(s) behind: %v", len(entries), entries)
	}
}

// --- the wipe race ------------------------------------------------------

// TestBookDropAcceptWaitsForWipe is the defect that motivated this pass: the
// upload path did not take the wipe lock, so a wipe could delete an uploaded
// file in the window between writing it and inserting its row, leaving a row
// pointing at nothing. Accept must block while a wipe holds the write lock.
func TestBookDropAcceptWaitsForWipe(t *testing.T) {
	h := newIntakeHarness(t)
	h.svc.wipeMu.Lock() // stand in for a wipe in progress

	done := make(chan error, 1)
	go func() {
		_, err := h.svc.Accept(context.Background(), "dune.epub", strings.NewReader("x"))
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("Accept completed during a wipe (err=%v) — it did not take the lock", err)
	case <-time.After(100 * time.Millisecond):
	}

	h.svc.wipeMu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Accept after wipe released: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Accept never completed after the wipe released the lock")
	}
}

// TestBookDropIntakeWaitsForWipe — same guarantee for the watcher path, now
// that the lock is taken per file inside Intake rather than around the whole
// scan.
func TestBookDropIntakeWaitsForWipe(t *testing.T) {
	h := newIntakeHarness(t)
	p := h.writeStaged(t, "dune.epub", "bytes")
	h.svc.wipeMu.Lock()

	done := make(chan error, 1)
	go func() {
		_, _, err := h.svc.Intake(context.Background(), p)
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("Intake completed during a wipe (err=%v) — it did not take the lock", err)
	case <-time.After(100 * time.Millisecond):
	}

	h.svc.wipeMu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Intake after wipe released: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Intake never completed after the wipe released the lock")
	}
}

// TestBookDropWipeWaitsForInFlightIntake — the other direction: a wipe must
// not start deleting while an intake is between writing bytes and committing
// its row.
func TestBookDropWipeWaitsForInFlightIntake(t *testing.T) {
	h := newIntakeHarness(t)
	h.inserter.blockOn = make(chan struct{})
	h.inserter.released = make(chan struct{})

	go func() {
		_, _ = h.svc.Accept(context.Background(), "dune.epub", strings.NewReader("x"))
	}()
	<-h.inserter.released // Accept is now inside Insert, holding the read lock

	locked := make(chan struct{})
	go func() {
		h.svc.wipeMu.Lock()
		close(locked)
		h.svc.wipeMu.Unlock()
	}()

	select {
	case <-locked:
		t.Fatal("wipe acquired the write lock while an intake was in flight")
	case <-time.After(100 * time.Millisecond):
	}

	close(h.inserter.blockOn)
	select {
	case <-locked:
	case <-time.After(2 * time.Second):
		t.Fatal("wipe never acquired the lock after the intake finished")
	}
}
