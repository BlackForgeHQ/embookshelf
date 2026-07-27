// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/storage"
)

// LibraryKind selects the storage flavour for a new library.
type LibraryKind string

const (
	LibraryKindLocal LibraryKind = "local"
	LibraryKindS3    LibraryKind = "s3"
)

// ErrS3NotConfigured is returned when the caller requests kind=s3 but
// EMBOOKSHELF_S3_BUCKET is not set in the environment.
var ErrS3NotConfigured = errors.New("s3 libraries require EMBOOKSHELF_S3_BUCKET to be set")

// ErrDataPathNotConfigured is returned when the caller requests
// kind=local but cfg.DataPath is empty in deps.
var ErrDataPathNotConfigured = errors.New("local libraries require DATA_PATH to be set")

// CoverDeleter is the slice of *coverstore.Store book deletion needs:
// drop the cover art belonging to a book id. Narrow so the deletion
// sequence is testable without a cover directory on disk.
type CoverDeleter interface {
	DeleteBook(id string) error
}

// LibraryServiceDeps groups the optional deps the service needs beyond its
// repo. Used to keep the constructor stable across callers.
type LibraryServiceDeps struct {
	Backends *repo.StorageBackendRepo
	SharedS3 config.SharedS3Config
	// Resolver is used during purge to iterate and delete objects under the
	// library's backend prefix. May be nil — purge is skipped when nil.
	Resolver storage.Resolver
	// DataPath is the root under which managed local-library folders
	// live. Per ADR 0002, kind=local libraries derive their filesystem
	// path as `${DataPath}/libraries/{slug}/`. Required for local
	// library creation; empty DataPath returns ErrDataPathNotConfigured.
	DataPath string
	// LibStore resolves a book's Library into the LibraryHandle that
	// knows where its bytes live. Nil degrades DeleteBook to a row-only
	// delete — the catalog is still correct, the bytes are left for a
	// human, and the outcome says so.
	LibStore LibraryStore
	// Covers removes a deleted book's cover art. Nil skips the step.
	Covers CoverDeleter
	// BookDropPath is the staging area, and with the registered library
	// paths makes up the Book file sandbox roots that bound the legacy
	// books.path unlink. Empty just drops one root; the sandbox fails
	// closed either way.
	BookDropPath string
}

// LibraryService is the Library lifecycle module: create a Library and
// its storage (ADR-0002's managed local folder, or a Backend row and its
// prefix), delete one and optionally purge what it held, and the two
// book-scoped operations that are more than a query — the metadata write
// pipeline and book deletion, each of which owns a sequence no caller
// should have to reassemble.
//
// It is deliberately not a front door to the book catalog. Reading books
// is the book repo's job and callers hold it directly; a delegating
// method here would only be a second seam in front of the first, wide
// enough to look like the interface but with nothing behind it.
type LibraryService struct {
	repo   *repo.LibraryRepo
	books  *repo.BookRepo
	deps   LibraryServiceDeps
	writer *MetadataWriter
}

// NewLibraryService builds the service. The MetadataWriter is a
// positional argument, not an optional setter: an edit that reaches the
// books row and nothing else is a half-written edit, so there is no
// configuration in which this service should be constructed without the
// ADR-0001 pipeline behind it. It used to be installed post-construction
// by WithMetadataWriter, which only the composition root ever called —
// every test therefore drove a direct-repo fallback that never ran in
// production.
func NewLibraryService(
	r *repo.LibraryRepo,
	b *repo.BookRepo,
	deps LibraryServiceDeps,
	w *MetadataWriter,
) *LibraryService {
	return &LibraryService{repo: r, books: b, deps: deps, writer: w}
}

func (s *LibraryService) List(ctx context.Context) ([]model.Library, error) {
	return s.repo.List(ctx)
}

// Create inserts a new library. Kind selects the storage flavour:
//
//   - local (or ""): the service derives path = ${DataPath}/libraries/{slug}/
//     and mkdirs it. No backend row is created; backend_id stays NULL and
//     the loader falls back to the LocalFS-at-/ default. Returns
//     ErrDataPathNotConfigured when DataPath is empty.
//   - s3: the service derives prefix = libraries/{slug}/, INSERTs a
//     storage_backends row from cfg.SharedS3, and points the library at
//     that backend.
func (s *LibraryService) Create(ctx context.Context, name string, kind LibraryKind) (model.Library, error) {
	name = strings.TrimSpace(name)
	slug := slugify(name)

	switch kind {
	case "", LibraryKindLocal:
		if s.deps.DataPath == "" {
			return model.Library{}, ErrDataPathNotConfigured
		}
		path := filepath.Join(s.deps.DataPath, "libraries", slug)
		if err := os.MkdirAll(path, 0o755); err != nil {
			return model.Library{}, fmt.Errorf("create library directory: %w", err)
		}
		return s.repo.CreateLibrary(ctx, name, slug, path, nil)

	case LibraryKindS3:
		if !s.deps.SharedS3.Configured() {
			return model.Library{}, ErrS3NotConfigured
		}
		prefix := "libraries/" + slug + "/"
		cfg := map[string]any{
			"bucket":            s.deps.SharedS3.Bucket,
			"region":            s.deps.SharedS3.Region,
			"endpoint":          s.deps.SharedS3.Endpoint,
			"prefix":            prefix,
			"access_key_id":     s.deps.SharedS3.AccessKeyID,
			"secret_access_key": s.deps.SharedS3.SecretAccessKey,
			"force_path_style":  s.deps.SharedS3.ForcePathStyle,
		}
		backend, err := s.deps.Backends.Create(ctx, "s3", cfg)
		if err != nil {
			return model.Library{}, fmt.Errorf("create s3 backend row: %w", err)
		}
		lib, err := s.repo.CreateLibrary(ctx, name, slug, "", &backend.ID)
		if err != nil {
			// Best-effort cleanup of the orphaned backend row.
			_ = s.deps.Backends.Delete(ctx, backend.ID)
			return model.Library{}, err
		}
		return lib, nil

	default:
		return model.Library{}, fmt.Errorf("unknown library kind %q", kind)
	}
}

// TouchScan stamps the library row with scan-completion aggregates.
func (s *LibraryService) TouchScan(ctx context.Context, id string, fileCount, discovered int) error {
	return s.repo.TouchScan(ctx, id, fileCount, discovered)
}

// GetByID returns a single library by id.
func (s *LibraryService) GetByID(ctx context.Context, id string) (model.Library, error) {
	return s.repo.GetByID(ctx, id)
}

// DeleteLibrary removes a library row. When purge is true and the library
// is backed by an s3 backend, also strips every object under the backend's
// prefix. The backend row is deleted last; failures during purge are logged
// but the response still succeeds. Purge errors are non-fatal because the
// DB row is already gone and the operator can clean the bucket manually.
func (s *LibraryService) DeleteLibrary(ctx context.Context, id string, purge bool) ([]string, error) {
	lib, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	bookIDs, err := s.repo.DeleteLibrary(ctx, id)
	if err != nil {
		return nil, err
	}
	if purge && lib.BackendID != nil {
		s.purgeBackend(ctx, *lib.BackendID)
	}
	return bookIDs, nil
}

// purgeBackend iterates the s3 backend prefix and deletes every object,
// then drops the backend row. Errors are logged and not returned — the
// library row is already gone, so the prefix may need manual cleanup.
func (s *LibraryService) purgeBackend(ctx context.Context, backendID string) {
	if s.deps.Resolver == nil {
		return
	}
	store, err := s.deps.Resolver.Resolve(backendID)
	if err != nil {
		slog.Warn("library purge: resolve backend", "id", backendID, "err", err)
		return
	}
	it, err := store.List(ctx, "")
	if err != nil {
		slog.Warn("library purge: list", "id", backendID, "err", err)
		return
	}
	defer func() { _ = it.Close() }()
	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			slog.Warn("library purge: iterate", "id", backendID, "err", err)
			break
		}
		if err := store.Delete(ctx, obj.Key); err != nil {
			slog.Warn("library purge: delete", "key", obj.Key, "err", err)
			continue
		}
	}
	// Drop the backend row so future reads through Resolver don't
	// hit a stale config. ErrStorageBackendInUse should not fire here
	// because the library row is already gone.
	if err := s.deps.Backends.Delete(ctx, backendID); err != nil {
		slog.Warn("library purge: delete backend row", "id", backendID, "err", err)
	}
}

// slugify collapses a human-readable name into a URL-safe slug:
// lowercase ASCII alphanumerics pass through, everything else (spaces,
// punctuation, non-ASCII) becomes a single '-'. Leading/trailing dashes are
// trimmed. Not perfect for non-Latin scripts — admins picking those names
// will see an empty slug and can retry with something portable.
func slugify(name string) string {
	lower := strings.ToLower(name)
	var b strings.Builder
	b.Grow(len(lower))
	dash := true
	for _, r := range lower {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// UpdateBookMetadata persists an edit and reports what actually landed.
// A nil error means the books row was updated; the Outcome says whether
// the sidecar and in-file copies kept up, so the caller can tell the user
// their edit did not reach the file instead of only logging it.
func (s *LibraryService) UpdateBookMetadata(ctx context.Context, b model.Book) (Outcome, error) {
	return s.writer.Write(ctx, b, TriggerManualEdit)
}

// DeleteOutcome reports what a book deletion accomplished beyond the
// row. Same shape and same reason as Outcome in metadata_writer.go —
// one authoritative step that fails the call, a tail of best-effort
// steps that degrade and must still be visible rather than vanishing
// into a log line.
//
// A separate type rather than a reuse of Outcome: every field Outcome
// carries beyond Failures (InFileWritten, SidecarMode, FolderRenamed,
// NewFolderPath) is a write-pipeline fact with no meaning for a delete,
// and merging the two would make a union nobody can read. StepFailure
// is shared, because that part genuinely is the same idea.
type DeleteOutcome struct {
	// Failures records the cleanup steps that were attempted and did not
	// complete: the book's bytes, its cover art, its legacy on-disk file.
	// The books row is never here — it fails the call.
	Failures []StepFailure
}

// Degraded reports whether any cleanup step after the row delete failed.
// A degraded delete still removed the book: the row is canonical, and
// what is left behind is bytes nobody references.
func (o DeleteOutcome) Degraded() bool { return len(o.Failures) > 0 }

// Warnings renders the failures for a human, so an operator learns half
// a gigabyte of narration outlived its book from the response or the
// log rather than from a disk-usage graph a month later.
func (o DeleteOutcome) Warnings() []string {
	if len(o.Failures) == 0 {
		return nil
	}
	out := make([]string, 0, len(o.Failures))
	for _, f := range o.Failures {
		out = append(out, fmt.Sprintf("%s not removed: %v", f.Step, f.Err))
	}
	return out
}

func (o *DeleteOutcome) fail(step string, err error) {
	o.Failures = append(o.Failures, StepFailure{Step: step, Err: err})
}

// DeleteBook hard-deletes a book and everything that belonged to it.
//
// The order is the whole point of this method, and it is not free to
// choose. Deleting the books row cascades its files rows, so the storage
// keys have to be snapshotted *before* the row goes and the bytes
// removed *after* it has. That invariant used to live in an HTTP
// handler with no test over it, sitting between two well-tested halves
// (LibraryHandle.BookFileLocations, LibraryHandle.DeleteBookBytes)
// neither of which can enforce it alone.
//
// The row is authoritative and the only step that fails the call: FKs on
// shelf_books, annotations, user_book_progress and reading_sessions
// cascade with it. Everything after it — the bytes, the cover art, the
// legacy books.path file — is best-effort and lands in DeleteOutcome
// instead. Failing the request over a stranded object would tell the
// user their delete did not happen when it did, and the row is already
// gone by then; leaving the byte loss unreported is the other half of
// the mistake, which is what DeleteOutcome exists to prevent.
//
// Takes the Book rather than an id because the caller has already loaded
// it to authorize the request, and both the library (hence which
// Storage) and the legacy path come off the row.
func (s *LibraryService) DeleteBook(ctx context.Context, book model.Book) (DeleteOutcome, error) {
	var out DeleteOutcome

	// Snapshot first, while the files rows still exist. Every failure
	// here is a degraded cleanup, never a blocked delete: this leg is
	// read-only, and a library whose backend is briefly unreachable must
	// not pin a book in the catalog.
	handle, locations := s.bookBytes(ctx, book, &out)

	if err := s.books.Delete(ctx, book.ID); err != nil {
		return DeleteOutcome{}, err
	}

	// Past this line the row is gone and nothing below can be retried by
	// re-issuing the request, which is why each step reports itself.
	if handle != nil {
		if err := handle.DeleteBookBytes(ctx, book.ID, locations); err != nil {
			out.fail("book files", err)
		}
	}
	if s.deps.Covers != nil {
		if err := s.deps.Covers.DeleteBook(book.ID); err != nil {
			out.fail("cover art", err)
		}
	}
	if err := s.deleteLegacyFile(ctx, book); err != nil {
		out.fail("book file on disk", err)
	}
	return out, nil
}

// bookBytes resolves the book's library and lists the storage keys it
// owns. Split out so DeleteBook reads as the sequence it is; both
// failures are recorded and both leave the delete itself untouched.
//
// A nil handle means "this install could not tell us where the bytes
// live" — no LibraryStore wired, or a library whose backend would not
// resolve. The delete proceeds; the bytes wait for an operator.
func (s *LibraryService) bookBytes(ctx context.Context, book model.Book, out *DeleteOutcome) (*LibraryHandle, []string) {
	if s.deps.LibStore == nil {
		return nil, nil
	}
	handle, err := s.deps.LibStore.For(ctx, book.LibraryID)
	if err != nil {
		out.fail("book files", fmt.Errorf("library handle: %w", err))
		return nil, nil
	}
	locations, err := handle.BookFileLocations(ctx, book.ID)
	if err != nil {
		// The handle is still returned: DeleteBookBytes with no keys is a
		// no-op, so this only means we lost the list, not that the caller
		// should stop.
		out.fail("book files", err)
		return handle, nil
	}
	return handle, locations
}

// deleteLegacyFile unlinks books.path — the single-path field that
// predates the files table, and the only record of a book's bytes on an
// install whose files rows were never backfilled. Books that do have
// files rows had the same file removed by DeleteBookBytes a moment ago,
// so this is a no-op for them; os.Remove on an already-gone file is not
// an error.
//
// Gated by the Book file sandbox: a books.path that does not resolve
// inside BOOKDROP_PATH or a registered library root is refused, so a
// malformed row cannot aim the unlink at something unrelated. Serving
// and deleting share the one SandboxPath implementation, so a change to
// the rule cannot apply to one and miss the other.
func (s *LibraryService) deleteLegacyFile(ctx context.Context, book model.Book) error {
	if book.Path == "" {
		return nil
	}
	abs, err := SandboxPath(book.Path, s.bookFileRoots(ctx))
	if err != nil {
		return err
	}
	if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// bookFileRoots is the allow-list of directories a book file may be
// deleted from: the BookDrop staging area plus every registered library
// root. A library with no local path (backend-backed) contributes
// nothing. A failed library listing yields the roots we do have — the
// sandbox fails closed, so the worst case is a skipped unlink.
func (s *LibraryService) bookFileRoots(ctx context.Context) []string {
	roots := make([]string, 0, 4)
	if s.deps.BookDropPath != "" {
		roots = append(roots, s.deps.BookDropPath)
	}
	libs, err := s.repo.List(ctx)
	if err != nil {
		slog.Warn("book delete: list libraries for sandbox roots", "err", err)
		return roots
	}
	for _, l := range libs {
		if l.Path != "" {
			roots = append(roots, l.Path)
		}
	}
	return roots
}
