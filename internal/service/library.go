package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

// LibraryServiceDeps groups the optional deps the service needs beyond its
// repo. Used to keep the constructor stable across callers.
type LibraryServiceDeps struct {
	Backends *repo.StorageBackendRepo
	SharedS3 config.SharedS3Config
	// Resolver is used during purge to iterate and delete objects under the
	// library's backend prefix. May be nil — purge is skipped when nil.
	Resolver storage.Resolver
	// Dialect is the DB dialect (postgres | sqlite). Used to guard s3
	// library creation on SQLite installs where storageloader refuses to
	// build s3 backends.
	Dialect config.Dialect
}

type LibraryService struct {
	repo *repo.LibraryRepo
	deps LibraryServiceDeps
}

func NewLibraryService(r *repo.LibraryRepo, deps LibraryServiceDeps) *LibraryService {
	return &LibraryService{repo: r, deps: deps}
}

func (s *LibraryService) List(ctx context.Context) ([]model.Library, error) {
	return s.repo.List(ctx)
}

// Create inserts a new library. Kind selects the storage flavour:
//
//   - local (or ""): path is required, names a filesystem directory. No
//     backend row is created; backend_id stays NULL and the loader falls
//     back to the LocalFS-at-/ default.
//   - s3: path is ignored; the service derives prefix = libraries/{slug}/,
//     INSERTs a storage_backends row from cfg.SharedS3, and points the
//     library at that backend.
func (s *LibraryService) Create(ctx context.Context, name string, kind LibraryKind, path string) (model.Library, error) {
	name = strings.TrimSpace(name)
	slug := slugify(name)

	switch kind {
	case "", LibraryKindLocal:
		path = strings.TrimRight(strings.TrimSpace(path), "/")
		return s.repo.CreateLibrary(ctx, name, slug, path, nil)

	case LibraryKindS3:
		if s.deps.Dialect == config.DialectSQLite {
			return model.Library{}, errors.New("s3 libraries are not supported on SQLite installs")
		}
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

// GetByID returns a single library (with its naming pattern) by id.
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

// SetFileNamingPattern stores (or clears) the per-library template used by
// the bookdrop approval flow. An empty trimmed string clears the pattern so
// the library falls back to keeping the original filename on disk.
func (s *LibraryService) SetFileNamingPattern(ctx context.Context, id string, pattern *string) error {
	if pattern != nil {
		trimmed := strings.TrimSpace(*pattern)
		if trimmed == "" {
			pattern = nil
		} else {
			pattern = &trimmed
		}
	}
	return s.repo.SetFileNamingPattern(ctx, id, pattern)
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

func (s *LibraryService) Books(ctx context.Context, userID, librarySlug string) ([]model.Book, error) {
	return s.repo.BooksByLibrarySlug(ctx, userID, librarySlug)
}

func (s *LibraryService) Search(ctx context.Context, userID, librarySlug string, p model.SearchParams) ([]model.Book, error) {
	return s.repo.Search(ctx, userID, librarySlug, p)
}

func (s *LibraryService) GetBook(ctx context.Context, userID, id string) (model.Book, error) {
	return s.repo.GetBookByID(ctx, userID, id)
}

func (s *LibraryService) UpdateBookMetadata(ctx context.Context, b model.Book) error {
	return s.repo.UpdateMetadata(ctx, b)
}

// DeleteBook hard-deletes a book. FKs on shelf_books, annotations,
// user_book_progress, and reading_sessions cascade in the DB; cover art
// and the source file on disk are the caller's responsibility — the
// service stays out of the filesystem to keep this layer testable
// without a mounted library root.
func (s *LibraryService) DeleteBook(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

// BookExistsByPath reports whether any non-deleted book already references
// this on-disk path. Used by the library scanner to avoid re-queuing files
// that are already in the library.
func (s *LibraryService) BookExistsByPath(ctx context.Context, path string) (bool, error) {
	return s.repo.BookExistsByPath(ctx, path)
}
