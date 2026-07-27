// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/storage"
)

// These tests run against a real database on purpose. The invariant under
// test is a consequence of the FK cascade — deleting a books row takes its
// files rows with it — so a mock of the row delete would be asserting on
// the test's own idea of the schema rather than the schema's behaviour.
// The rest of the sequence is faked so the assertions stay about ordering.

// ---------------------------------------------------------------------------
// Probes
// ---------------------------------------------------------------------------

// rowProbe answers "is the books row still there?" straight from the
// database. Every fake below consults it at the moment it is called, so
// the ordering assertions are about the real cascade rather than a call
// log the fakes agreed on among themselves.
type rowProbe struct {
	t      *testing.T
	d      *db.DB
	bookID string
}

func (p *rowProbe) rowPresent() bool {
	p.t.Helper()
	var n int
	if err := p.d.SQL.QueryRowContext(context.Background(),
		`SELECT count(*) FROM books WHERE id = $1`, p.bookID).Scan(&n); err != nil {
		p.t.Fatalf("probe books row: %v", err)
	}
	return n > 0
}

// probingFiles is the files lister the handle reads its keys from. It
// records whether the row was still present when it ran — a snapshot
// taken after the delete would come back empty, which is the whole
// reason the order is fixed.
type probingFiles struct {
	probe     *rowProbe
	locations []string
	rowSeen   *bool
}

func (f *probingFiles) ListByBook(_ context.Context, bookID string) ([]model.File, error) {
	seen := f.probe.rowPresent()
	f.rowSeen = &seen
	out := make([]model.File, 0, len(f.locations))
	for _, loc := range f.locations {
		out = append(out, model.File{Location: loc, Format: "EPUB", BookID: bookID})
	}
	return out, nil
}

// probingStorage records the keys it was asked to delete and whether the
// row had already gone by then. Deleting bytes before the row would leave
// a live catalog entry pointing at nothing if the row delete then failed.
type probingStorage struct {
	storage.Storage
	probe    *rowProbe
	deleted  []string
	rowSeen  []bool
	failKey  string
	failWith error
}

func (s *probingStorage) Delete(_ context.Context, key string, _ ...storage.DeleteOption) error {
	s.rowSeen = append(s.rowSeen, s.probe.rowPresent())
	if s.failKey != "" && key == s.failKey {
		return s.failWith
	}
	s.deleted = append(s.deleted, key)
	return nil
}

// probingOrphans is the backend-backed arm's sink (ADR-0005): the keys
// are queued for the sweeper instead of deleted inline.
type probingOrphans struct {
	probe   *rowProbe
	rows    []repo.PendingOrphanInsert
	rowSeen []bool
}

func (o *probingOrphans) Insert(_ context.Context, rows []repo.PendingOrphanInsert) error {
	o.rowSeen = append(o.rowSeen, o.probe.rowPresent())
	o.rows = append(o.rows, rows...)
	return nil
}

// stubLibStore hands out one prepared handle, so a test can construct a
// LibraryHandle with fakes behind its unexported seams.
type stubLibStore struct {
	handle *LibraryHandle
	err    error
}

func (s *stubLibStore) For(_ context.Context, _ string) (*LibraryHandle, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.handle, nil
}

// failingCovers stands in for the cover store when the point of the test
// is that a cover that will not delete does not fail the book delete.
type failingCovers struct {
	calls int
	err   error
}

func (c *failingCovers) DeleteBook(string) error {
	c.calls++
	return c.err
}

// ---------------------------------------------------------------------------
// Fixture
// ---------------------------------------------------------------------------

// deleteFixture is one library with one book in it, plus the probe that
// watches the row.
type deleteFixture struct {
	libs  *repo.LibraryRepo
	books *repo.BookRepo
	lib   model.Library
	book  model.Book
	probe *rowProbe
	root  string
}

// newDeleteFixture creates a local library rooted at a temp dir holding
// one book. backendBacked instead points the library at a
// storage_backends row and gives it no local path — the shape Create
// produces for kind=s3.
func newDeleteFixture(t *testing.T, backendBacked bool) *deleteFixture {
	t.Helper()
	d := repotest.New(t)
	libs := repo.NewLibraryRepo(d)
	books := repo.NewBookRepo(d)
	ctx := context.Background()

	root := ""
	var backendID *string
	if backendBacked {
		id := newBackend(t, d)
		backendID = &id
	} else {
		root = t.TempDir()
	}
	lib, err := libs.CreateLibrary(ctx, "Deletable", "deletable", root, backendID)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	book, err := books.Create(ctx, model.Book{
		LibraryID: lib.ID,
		Title:     "Doomed",
		Author:    "A. Nonymous",
		Format:    "EPUB",
	})
	if err != nil {
		t.Fatalf("Create book: %v", err)
	}
	return &deleteFixture{
		libs: libs, books: books, lib: lib, book: book, root: root,
		probe: &rowProbe{t: t, d: d, bookID: book.ID},
	}
}

// svc builds the service under test with the given handle and deps.
func (f *deleteFixture) svc(handle *LibraryHandle, deps LibraryServiceDeps) *LibraryService {
	if handle != nil {
		deps.LibStore = &stubLibStore{handle: handle}
	}
	// nil writer: deletion never runs the edit-side pipeline.
	return NewLibraryService(f.libs, f.books, deps, nil)
}

// newBackend inserts a storage_backends row so a library can point at one.
func newBackend(t *testing.T, d *db.DB) string {
	t.Helper()
	id, err := repo.NewStorageBackendRepo(d).Create(context.Background(), "s3",
		map[string]any{"bucket": "b", "region": "r", "prefix": "libraries/deletable/"})
	if err != nil {
		t.Fatalf("Create backend: %v", err)
	}
	return id.ID
}

// ---------------------------------------------------------------------------
// The ordering invariant
// ---------------------------------------------------------------------------

// The one test the composition never had. Deleting the books row cascades
// its files rows, so the keys must be read while the row is still there
// and the bytes removed once it is gone. Both halves were well covered;
// nothing pinned the sequence, which lived in an HTTP handler.
func TestDeleteBookReadsLocationsBeforeTheRowGoesAndRemovesBytesAfter(t *testing.T) {
	fx := newDeleteFixture(t, false)
	files := &probingFiles{probe: fx.probe, locations: []string{
		"Author/Title/book.epub",
		"Author/Title/book.mp3",
	}}
	store := &probingStorage{probe: fx.probe}
	handle := &LibraryHandle{Library: fx.lib, Storage: store, files: files}

	out, err := fx.svc(handle, LibraryServiceDeps{}).DeleteBook(context.Background(), fx.book)
	if err != nil {
		t.Fatalf("DeleteBook: %v", err)
	}
	if out.Degraded() {
		t.Fatalf("clean delete reported %v", out.Warnings())
	}

	if files.rowSeen == nil {
		t.Fatal("the file locations were never read — nothing would know which bytes to remove")
	}
	if !*files.rowSeen {
		t.Error("locations were read after the row was deleted; the files rows have cascaded by then and the snapshot is empty")
	}
	if len(store.rowSeen) != 2 {
		t.Fatalf("storage saw %d deletes, want 2", len(store.rowSeen))
	}
	for i, present := range store.rowSeen {
		if present {
			t.Errorf("delete %d removed bytes while the books row still existed", i)
		}
	}
	if fx.probe.rowPresent() {
		t.Error("the books row survived the delete")
	}
}

// The keys handed to Storage are the ones it answers to. A local
// install's LocalFS is rooted at "/", so a library-relative
// files.location asked it to delete "/Author/Title/book.epub" and
// removed nothing — the legacy books.path unlink was quietly carrying
// the whole local delete path.
func TestDeleteBookRemovesLocalBytesAtTheirAbsoluteKeys(t *testing.T) {
	fx := newDeleteFixture(t, false)
	files := &probingFiles{probe: fx.probe, locations: []string{
		"Author/Title/book.epub",
		"Author/Title/book.mp3",
	}}
	store := &probingStorage{probe: fx.probe}
	handle := &LibraryHandle{Library: fx.lib, Storage: store, files: files}

	if _, err := fx.svc(handle, LibraryServiceDeps{}).DeleteBook(context.Background(), fx.book); err != nil {
		t.Fatalf("DeleteBook: %v", err)
	}

	sort.Strings(store.deleted)
	want := []string{
		filepath.Join(fx.root, "Author/Title/book.epub"),
		filepath.Join(fx.root, "Author/Title/book.mp3"),
	}
	sort.Strings(want)
	if len(store.deleted) != len(want) {
		t.Fatalf("deleted %v, want %v", store.deleted, want)
	}
	for i := range want {
		if store.deleted[i] != want[i] {
			t.Errorf("deleted[%d] = %q, want %q", i, store.deleted[i], want[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Backend-backed branch (ADR-0005)
// ---------------------------------------------------------------------------

// On a Backend-backed library the bytes may still be serving a presigned
// URL, so the keys become Pending orphans for the sweeper instead of
// inline deletes. Nothing reached this branch through the whole sequence
// before — the handler that composed it had no tests at all.
func TestDeleteBookEnqueuesPendingOrphansOnBackendBackedLibrary(t *testing.T) {
	fx := newDeleteFixture(t, true)
	files := &probingFiles{probe: fx.probe, locations: []string{
		"libraries/deletable/Author/Title/book.epub",
		"libraries/deletable/Author/Title/book.mp3",
	}}
	store := &probingStorage{probe: fx.probe}
	orphans := &probingOrphans{probe: fx.probe}
	handle := &LibraryHandle{Library: fx.lib, Storage: store, files: files, orphans: orphans}

	before := time.Now()
	out, err := fx.svc(handle, LibraryServiceDeps{}).DeleteBook(context.Background(), fx.book)
	if err != nil {
		t.Fatalf("DeleteBook: %v", err)
	}
	if out.Degraded() {
		t.Fatalf("clean backend-backed delete reported %v", out.Warnings())
	}

	if len(store.deleted) != 0 {
		t.Errorf("backend-backed delete removed %v inline; the grace window exists so an in-flight presigned download finishes", store.deleted)
	}
	if files.rowSeen == nil || !*files.rowSeen {
		t.Error("locations must be snapshotted before the row goes on backend-backed libraries too")
	}
	if len(orphans.rowSeen) != 1 || orphans.rowSeen[0] {
		t.Errorf("orphans were enqueued while the row still existed (%v)", orphans.rowSeen)
	}
	if len(orphans.rows) != 2 {
		t.Fatalf("enqueued %d orphans, want 2", len(orphans.rows))
	}
	for _, row := range orphans.rows {
		if row.LibraryID != fx.lib.ID {
			t.Errorf("orphan library = %q, want %q", row.LibraryID, fx.lib.ID)
		}
		if row.Reason != repo.ReasonOrphanBookDelete {
			t.Errorf("orphan reason = %q, want %q", row.Reason, repo.ReasonOrphanBookDelete)
		}
		if row.BookID == nil || *row.BookID != fx.book.ID {
			t.Errorf("orphan book id = %v, want %q", row.BookID, fx.book.ID)
		}
		if row.EligibleAt.Before(before.Add(BookDeleteGrace)) {
			t.Errorf("orphan eligible_at = %v, want at least %v of grace", row.EligibleAt, BookDeleteGrace)
		}
	}
	if fx.probe.rowPresent() {
		t.Error("the books row survived the delete")
	}
}

// ---------------------------------------------------------------------------
// Failure policy: the row is authoritative, cleanup degrades and reports
// ---------------------------------------------------------------------------

func TestDeleteBookReportsByteCleanupFailureInsteadOfFailingTheCall(t *testing.T) {
	fx := newDeleteFixture(t, false)
	files := &probingFiles{probe: fx.probe, locations: []string{
		"Author/Title/book.epub",
		"Author/Title/book.mp3",
	}}
	store := &probingStorage{
		probe:    fx.probe,
		failKey:  filepath.Join(fx.root, "Author/Title/book.epub"),
		failWith: errors.New("permission denied"),
	}
	handle := &LibraryHandle{Library: fx.lib, Storage: store, files: files}

	out, err := fx.svc(handle, LibraryServiceDeps{}).DeleteBook(context.Background(), fx.book)
	if err != nil {
		t.Fatalf("a stranded object must not fail the delete: %v", err)
	}
	if fx.probe.rowPresent() {
		t.Error("the books row survived a failure that is supposed to be best-effort")
	}
	if !out.Degraded() {
		t.Fatal("the failure was swallowed; nobody would ever learn the bytes are still there")
	}
	if got := out.Warnings(); len(got) != 1 || !strings.Contains(got[0], "book files") {
		t.Errorf("warnings = %v, want one naming the book files step", got)
	}
	// The other key still went: one unreachable object must not strand the rest.
	if len(store.deleted) != 1 || !strings.HasSuffix(store.deleted[0], "book.mp3") {
		t.Errorf("deleted = %v, want the mp3 removed anyway", store.deleted)
	}
}

func TestDeleteBookReportsCoverFailureInsteadOfFailingTheCall(t *testing.T) {
	fx := newDeleteFixture(t, false)
	covers := &failingCovers{err: errors.New("read-only filesystem")}
	handle := &LibraryHandle{Library: fx.lib, files: &probingFiles{probe: fx.probe}}

	out, err := fx.svc(handle, LibraryServiceDeps{Covers: covers}).DeleteBook(context.Background(), fx.book)
	if err != nil {
		t.Fatalf("a stuck cover must not fail the delete: %v", err)
	}
	if covers.calls != 1 {
		t.Errorf("cover store called %d times, want 1", covers.calls)
	}
	if got := out.Warnings(); len(got) != 1 || !strings.Contains(got[0], "cover art") {
		t.Errorf("warnings = %v, want one naming the cover art step", got)
	}
}

// A library whose backend will not resolve must not pin a book in the
// catalog. The delete goes through, the bytes wait for a human, and the
// outcome says which.
func TestDeleteBookDegradesWhenTheLibraryHandleIsUnavailable(t *testing.T) {
	fx := newDeleteFixture(t, false)
	svc := NewLibraryService(fx.libs, fx.books, LibraryServiceDeps{
		LibStore: &stubLibStore{err: errors.New("backend unreachable")},
	}, nil)

	out, err := svc.DeleteBook(context.Background(), fx.book)
	if err != nil {
		t.Fatalf("DeleteBook: %v", err)
	}
	if fx.probe.rowPresent() {
		t.Error("the books row survived")
	}
	if got := out.Warnings(); len(got) != 1 || !strings.Contains(got[0], "book files") {
		t.Errorf("warnings = %v, want one naming the book files step", got)
	}
}

// No LibraryStore wired at all (an install with no storage backend) is a
// documented degrade, not an error.
func TestDeleteBookWithoutALibraryStoreStillDeletesTheRow(t *testing.T) {
	fx := newDeleteFixture(t, false)
	out, err := NewLibraryService(fx.libs, fx.books, LibraryServiceDeps{}, nil).
		DeleteBook(context.Background(), fx.book)
	if err != nil {
		t.Fatalf("DeleteBook: %v", err)
	}
	if out.Degraded() {
		t.Errorf("an unconfigured install is not a degraded delete: %v", out.Warnings())
	}
	if fx.probe.rowPresent() {
		t.Error("the books row survived")
	}
}

// The row is the authoritative step: when it does not go, nothing else
// may run. Cleanup after a failed delete would strip the bytes out from
// under a book that is still in the catalog.
func TestDeleteBookRunsNoCleanupWhenTheRowDeleteFails(t *testing.T) {
	fx := newDeleteFixture(t, false)
	files := &probingFiles{probe: fx.probe, locations: []string{"Author/Title/book.epub"}}
	store := &probingStorage{probe: fx.probe}
	covers := &failingCovers{}
	handle := &LibraryHandle{Library: fx.lib, Storage: store, files: files}

	ghost := fx.book
	ghost.ID = db.NewID() // a book id the table has never seen

	_, err := fx.svc(handle, LibraryServiceDeps{Covers: covers}).DeleteBook(context.Background(), ghost)
	if !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("err = %v, want repo.ErrNotFound", err)
	}
	if len(store.deleted) != 0 {
		t.Errorf("removed %v for a book that was never deleted", store.deleted)
	}
	if covers.calls != 0 {
		t.Errorf("cover store called %d times after a failed row delete", covers.calls)
	}
	if !fx.probe.rowPresent() {
		t.Error("the real book row disappeared")
	}
}

// ---------------------------------------------------------------------------
// Legacy books.path and the Book file sandbox
// ---------------------------------------------------------------------------

// Installs whose files rows were never backfilled have only books.path.
// It is the last record of the bytes, so the module unlinks it too.
func TestDeleteBookUnlinksTheLegacyPathInsideTheSandbox(t *testing.T) {
	fx := newDeleteFixture(t, false)
	path := filepath.Join(fx.root, "legacy.epub")
	if err := os.WriteFile(path, []byte("epub"), 0o600); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}
	book := fx.book
	book.Path = path

	out, err := NewLibraryService(fx.libs, fx.books, LibraryServiceDeps{}, nil).
		DeleteBook(context.Background(), book)
	if err != nil {
		t.Fatalf("DeleteBook: %v", err)
	}
	if out.Degraded() {
		t.Fatalf("clean delete reported %v", out.Warnings())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("legacy file survived: stat err = %v", err)
	}
}

// A books.path pointing outside every registered library root is refused
// and reported, rather than followed. Fails closed: the row still goes.
func TestDeleteBookRefusesALegacyPathOutsideTheSandbox(t *testing.T) {
	fx := newDeleteFixture(t, false)
	outside := filepath.Join(t.TempDir(), "elsewhere.epub")
	if err := os.WriteFile(outside, []byte("epub"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	book := fx.book
	book.Path = outside

	out, err := NewLibraryService(fx.libs, fx.books, LibraryServiceDeps{}, nil).
		DeleteBook(context.Background(), book)
	if err != nil {
		t.Fatalf("DeleteBook: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("a path outside the sandbox was unlinked anyway: %v", err)
	}
	if got := out.Warnings(); len(got) != 1 || !strings.Contains(got[0], "book file on disk") {
		t.Errorf("warnings = %v, want one naming the on-disk file step", got)
	}
	if fx.probe.rowPresent() {
		t.Error("the books row survived")
	}
}

// The BookDrop staging area is part of the sandbox: a book approved from
// a watched folder can still carry a path under it.
func TestDeleteBookUnlinksALegacyPathUnderBookDrop(t *testing.T) {
	fx := newDeleteFixture(t, false)
	drop := t.TempDir()
	path := filepath.Join(drop, "staged.epub")
	if err := os.WriteFile(path, []byte("epub"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	book := fx.book
	book.Path = path

	out, err := NewLibraryService(fx.libs, fx.books, LibraryServiceDeps{BookDropPath: drop}, nil).
		DeleteBook(context.Background(), book)
	if err != nil {
		t.Fatalf("DeleteBook: %v", err)
	}
	if out.Degraded() {
		t.Fatalf("clean delete reported %v", out.Warnings())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("staged file survived: stat err = %v", err)
	}
}
