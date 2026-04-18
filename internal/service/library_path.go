package service

import (
	"context"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// LibraryPathService exposes CRUD + scan-state updates for the filesystem
// roots each library tracks. Scans themselves run as a river job —
// triggering one is the caller's responsibility (service stays I/O-free).
type LibraryPathService struct {
	repo *repo.LibraryPathRepo
}

func NewLibraryPathService(r *repo.LibraryPathRepo) *LibraryPathService {
	return &LibraryPathService{repo: r}
}

func (s *LibraryPathService) List(ctx context.Context) ([]model.LibraryPath, error) {
	return s.repo.List(ctx)
}

func (s *LibraryPathService) ListForLibrary(ctx context.Context, libraryID string) ([]model.LibraryPath, error) {
	return s.repo.ListForLibrary(ctx, libraryID)
}

func (s *LibraryPathService) Get(ctx context.Context, id string) (model.LibraryPath, error) {
	return s.repo.Get(ctx, id)
}

func (s *LibraryPathService) Create(ctx context.Context, libraryID, path string) (model.LibraryPath, error) {
	return s.repo.Create(ctx, libraryID, path)
}

func (s *LibraryPathService) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

func (s *LibraryPathService) TouchScan(ctx context.Context, id string, fileCount, discovered int) error {
	return s.repo.TouchScan(ctx, id, fileCount, discovered)
}
