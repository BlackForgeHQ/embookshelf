package repo

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blackforge/embookshelf/internal/model"
)

// ErrLibraryPathTaken is returned by Create when the path is already
// registered for that library.
var ErrLibraryPathTaken = errors.New("library path already registered")

type LibraryPathRepo struct {
	pool *pgxpool.Pool
}

func NewLibraryPathRepo(pool *pgxpool.Pool) *LibraryPathRepo {
	return &LibraryPathRepo{pool: pool}
}

const libraryPathCols = `id, library_id, path, last_scanned_at, file_count, discovered_count, created_at`

func (r *LibraryPathRepo) List(ctx context.Context) ([]model.LibraryPath, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+libraryPathCols+` FROM library_paths ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectLibraryPaths(rows)
}

func (r *LibraryPathRepo) ListForLibrary(ctx context.Context, libraryID string) ([]model.LibraryPath, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+libraryPathCols+`
		FROM library_paths
		WHERE library_id = $1
		ORDER BY created_at ASC
	`, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectLibraryPaths(rows)
}

func (r *LibraryPathRepo) Get(ctx context.Context, id string) (model.LibraryPath, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+libraryPathCols+` FROM library_paths WHERE id = $1`, id)
	return scanLibraryPath(row)
}

func (r *LibraryPathRepo) Create(ctx context.Context, libraryID, path string) (model.LibraryPath, error) {
	path = strings.TrimRight(strings.TrimSpace(path), "/")
	if path == "" {
		return model.LibraryPath{}, errors.New("path required")
	}
	row := r.pool.QueryRow(ctx, `
		INSERT INTO library_paths (library_id, path)
		VALUES ($1, $2)
		ON CONFLICT (library_id, path) DO NOTHING
		RETURNING `+libraryPathCols, libraryID, path)
	lp, err := scanLibraryPath(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.LibraryPath{}, ErrLibraryPathTaken
	}
	return lp, err
}

func (r *LibraryPathRepo) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM library_paths WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchScan records the result of a scan: the total number of supported
// files on disk and how many new ones were discovered (staged into bookdrop).
func (r *LibraryPathRepo) TouchScan(ctx context.Context, id string, fileCount, discovered int) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE library_paths
		SET last_scanned_at = now(), file_count = $2, discovered_count = $3
		WHERE id = $1
	`, id, fileCount, discovered)
	return err
}

func scanLibraryPath(s scanner) (model.LibraryPath, error) {
	var lp model.LibraryPath
	err := s.Scan(
		&lp.ID, &lp.LibraryID, &lp.Path, &lp.LastScannedAt,
		&lp.FileCount, &lp.DiscoveredCount, &lp.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return lp, ErrNotFound
	}
	return lp, err
}

func collectLibraryPaths(rows pgx.Rows) ([]model.LibraryPath, error) {
	var out []model.LibraryPath
	for rows.Next() {
		lp, err := scanLibraryPath(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, lp)
	}
	return out, rows.Err()
}
