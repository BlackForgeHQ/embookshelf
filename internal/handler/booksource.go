package handler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/storage"
	s3backend "github.com/blackforge/embookshelf/internal/storage/s3"
)

// BookSource describes how the handler should serve a book file.
type BookSource struct {
	Kind string // "local" or "presign"
	Path string // populated when Kind == "local"
	URL  string // populated when Kind == "presign"
	TTL  time.Duration
}

// Presigner is the capability-gated interface that any backend with
// CapPresign must satisfy. Defined here (not in storage) so the
// handler can probe via type assertion without leaking aws-sdk types.
type Presigner interface {
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// Compile-time assertion that S3 Backend satisfies Presigner.
var _ Presigner = (*s3backend.Backend)(nil)

// ResolveBookSource determines whether to redirect or serve locally.
// Lookup chain: book -> files row(s) -> library.backend_id ->
// Resolver.Resolve -> Capabilities() -> presign or local.
func ResolveBookSource(
	ctx context.Context,
	book model.Book,
	files *repo.FileRepo,
	libs *repo.LibraryRepo,
	resolver storage.Resolver,
	presignTTL time.Duration,
	fallback string, // "" or "stream"
) (BookSource, error) {
	if book.Path == "" {
		return BookSource{}, errors.New("book has no path")
	}
	// Default outcome: local, using book.Path. Plan B kept book.Path
	// populated alongside files.location, so single-backend installs
	// continue to work without files-table backfill.
	src := BookSource{Kind: "local", Path: book.Path}

	if resolver == nil || files == nil || libs == nil {
		return src, nil
	}

	// Resolve the library's backend.
	lib, err := libs.GetByID(ctx, book.LibraryID)
	if err != nil {
		return src, nil // can't resolve → fall back to local
	}
	backendID := ""
	if lib.BackendID != nil {
		backendID = *lib.BackendID
	}
	backend, err := resolver.Resolve(backendID)
	if err != nil {
		return src, nil
	}

	// If the backend can presign and we're not forcing stream,
	// look up the file's location and presign it.
	if backend.Capabilities()&storage.CapPresign != 0 && fallback != "stream" {
		ps, ok := backend.(Presigner)
		if !ok {
			return src, nil // unexpected; fall back to local
		}
		// Find the canonical files row for this book. Pick the
		// "primary" file by matching books.format → files.format.
		// If no row exists yet (pre-files-backfill), fall back.
		f, err := primaryFile(ctx, files, book)
		if err != nil {
			return src, nil
		}
		url, err := ps.PresignGet(ctx, f.Location, presignTTL)
		if err != nil {
			return src, nil
		}
		return BookSource{Kind: "presign", URL: url, TTL: presignTTL}, nil
	}
	return src, nil
}

func primaryFile(ctx context.Context, files *repo.FileRepo, book model.Book) (model.File, error) {
	// Plan B's FileRepo didn't have a "by book id" lookup yet —
	// ListByBook was added as part of Plan G. It returns all
	// files for a book; we pick the one matching books.format.
	list, err := files.ListByBook(ctx, book.ID)
	if err != nil {
		return model.File{}, err
	}
	for _, f := range list {
		if f.Format == book.Format {
			return f, nil
		}
	}
	if len(list) > 0 {
		return list[0], nil
	}
	return model.File{}, fmt.Errorf("no files row for book %s", book.ID)
}
