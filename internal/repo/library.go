package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blackforge/embookshelf/internal/model"
)

// bookCols is the SELECT column list shared by every book-returning query.
// The trailing `COALESCE(ubp.progress, 0)` comes from a LEFT JOIN on
// user_book_progress; callers must alias that join as `ubp` and bind the
// user id as $1.
const bookCols = `
	b.id, b.library_id, b.title, b.author, b.format, b.year,
	COALESCE(ubp.progress, 0) AS progress,
	b.rating, b.cover_palette,
	b.description, b.isbn, b.publisher, b.series, b.series_index, b.tags,
	b.created_at, b.path,
	b.has_cover, b.cover_mime,
	COALESCE(ubp.resume_cfi, '') AS resume_cfi
`

const bookFrom = `
	FROM books b
	LEFT JOIN user_book_progress ubp ON ubp.book_id = b.id AND ubp.user_id = $1
`

// ErrNotFound is returned when a lookup by id/slug returns no rows.
var ErrNotFound = errors.New("not found")

// ErrLibraryNameTaken is returned by CreateLibrary when the derived slug
// collides with an existing library. Callers should surface this as a 409
// so the UI can prompt the user to pick a different name.
var ErrLibraryNameTaken = errors.New("library name already taken")

// ErrLibraryPathTaken is returned when the supplied filesystem root is
// already bound to another library. Two libraries sharing one path would
// race on scans and naming collisions.
var ErrLibraryPathTaken = errors.New("library path already in use")

type LibraryRepo struct {
	pool *pgxpool.Pool
}

func NewLibraryRepo(pool *pgxpool.Pool) *LibraryRepo {
	return &LibraryRepo{pool: pool}
}

// libCols is the shared SELECT list for library rows. Keep the scan
// order in scanLibrary() in sync if you add columns here.
const libCols = `
	l.id, l.name, l.slug, l.path,
	l.last_scanned_at, l.file_count, l.discovered_count,
	l.file_naming_pattern, l.created_at,
	COALESCE(
		(SELECT COUNT(*) FROM books b
		 WHERE b.library_id = l.id AND b.deleted_at IS NULL),
		0
	) AS book_count
`

// CreateLibrary inserts a new library row and returns the persisted
// record. `slug` is UNIQUE (000001) and `path` is UNIQUE (000018) — a
// collision on either surfaces as a typed sentinel (ErrLibraryNameTaken
// or ErrLibraryPathTaken) so the handler can map it to a 409.
//
// RETURNING selects every column scanLibrary expects, with a literal
// 0 for book_count (a brand-new library has no books, and referencing
// `libraries l` alongside a modifying CTE wouldn't see the insert —
// both share one snapshot per the PG manual).
func (r *LibraryRepo) CreateLibrary(ctx context.Context, name, slug, path string) (model.Library, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO libraries (name, slug, path)
		VALUES ($1, $2, $3)
		RETURNING
			id, name, slug, path,
			last_scanned_at, file_count, discovered_count,
			file_naming_pattern, created_at,
			0 AS book_count
	`, name, slug, path)
	l, err := scanLibrary(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			switch pgErr.ConstraintName {
			case "libraries_slug_key":
				return model.Library{}, ErrLibraryNameTaken
			case "libraries_path_key":
				return model.Library{}, ErrLibraryPathTaken
			}
		}
		return model.Library{}, err
	}
	return l, nil
}

func (r *LibraryRepo) List(ctx context.Context) ([]model.Library, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+libCols+`
		FROM libraries l
		ORDER BY l.created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var libs []model.Library
	for rows.Next() {
		l, err := scanLibrary(rows)
		if err != nil {
			return nil, err
		}
		libs = append(libs, l)
	}
	return libs, rows.Err()
}

// GetByID returns a single library row. Used by pattern-preview + scan
// flows that need the current path/pattern without a full listing.
func (r *LibraryRepo) GetByID(ctx context.Context, id string) (model.Library, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT `+libCols+`
		FROM libraries l
		WHERE l.id = $1
	`, id)
	l, err := scanLibrary(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.Library{}, ErrNotFound
		}
		return model.Library{}, err
	}
	return l, nil
}

// TouchScan stamps the last-scan aggregate on a library row after a
// filesystem walk completes. Used by the library-scan worker.
func (r *LibraryRepo) TouchScan(ctx context.Context, id string, fileCount, discovered int) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE libraries
		SET last_scanned_at = now(),
		    file_count       = $2,
		    discovered_count = $3
		WHERE id = $1
	`, id, fileCount, discovered)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanLibrary(s scanner) (model.Library, error) {
	var l model.Library
	err := s.Scan(
		&l.ID, &l.Name, &l.Slug, &l.Path,
		&l.LastScannedAt, &l.FileCount, &l.DiscoveredCount,
		&l.FileNamingPattern, &l.CreatedAt, &l.BookCount,
	)
	return l, err
}

// DeleteLibrary removes a library row and cascades the deletion through
// books, library_paths, shelf_books, annotations, reading_sessions, and
// per-user progress via the existing FK ON DELETE CASCADE chain. The
// returned []bookIDs lets the caller clean up cover-image files on disk
// — those aren't owned by the DB so the cascade can't reach them.
//
// Book source files are deliberately left alone: library paths point at
// user-managed filesystem roots, so "unregister this library" is not the
// same as "wipe the bytes". Admin-driven cleanup belongs outside this
// path.
func (r *LibraryRepo) DeleteLibrary(ctx context.Context, id string) ([]string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `SELECT id FROM books WHERE library_id = $1`, id)
	if err != nil {
		return nil, err
	}
	var bookIDs []string
	for rows.Next() {
		var bookID string
		if err := rows.Scan(&bookID); err != nil {
			rows.Close()
			return nil, err
		}
		bookIDs = append(bookIDs, bookID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	tag, err := tx.Exec(ctx, `DELETE FROM libraries WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return bookIDs, nil
}

// SetFileNamingPattern writes (or clears) the per-library naming pattern.
// Pass nil to revert to the fallback (keep original filename on approval).
func (r *LibraryRepo) SetFileNamingPattern(ctx context.Context, id string, pattern *string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE libraries SET file_naming_pattern = $2 WHERE id = $1
	`, id, pattern)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Search lists books scoped to a specific user's progress. An empty
// librarySlug means "across all libraries"; passing a slug filters down.
// Always capped at 500 rows today — the Library UI renders them all
// client-side. Server-side pagination is a future slice when library
// sizes demand it.
func (r *LibraryRepo) Search(ctx context.Context, userID, librarySlug string, p model.SearchParams) ([]model.Book, error) {
	// $1 is always the user id (driven by the LEFT JOIN on user_book_progress).
	var (
		where = []string{"b.deleted_at IS NULL"}
		args  = []any{userID}
	)

	if librarySlug != "" {
		args = append(args, librarySlug)
		where = append(where, fmt.Sprintf("l.slug = $%d", len(args)))
	}
	if q := strings.TrimSpace(p.Query); q != "" {
		args = append(args, q)
		where = append(where, fmt.Sprintf("b.tsv @@ websearch_to_tsquery('english', $%d)", len(args)))
	}
	if len(p.Format) > 0 {
		args = append(args, p.Format)
		where = append(where, fmt.Sprintf("b.format = ANY($%d::text[])", len(args)))
	}

	orderBy := "b.title ASC"
	switch p.Sort {
	case "author":
		orderBy = "b.author ASC, b.title ASC"
	case "recent":
		orderBy = "b.created_at DESC"
	case "year":
		orderBy = "b.year DESC, b.title ASC"
	case "rating":
		orderBy = "b.rating DESC, b.title ASC"
	}

	query := `
		SELECT ` + bookCols + `
		` + bookFrom + `
		JOIN libraries l ON l.id = b.library_id
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY ` + orderBy + `
		LIMIT 500
	`
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectBooks(rows)
}

// BooksByLibrarySlug is retained for the home dashboard's simple count.
func (r *LibraryRepo) BooksByLibrarySlug(ctx context.Context, userID, slug string) ([]model.Book, error) {
	return r.Search(ctx, userID, slug, model.SearchParams{})
}

// Create inserts a new book row. Progress is not a column anymore — callers
// that want to record progress for the creator should call ProgressRepo.Set.
func (r *LibraryRepo) Create(ctx context.Context, b model.Book) (model.Book, error) {
	if b.Tags == nil {
		b.Tags = []string{}
	}
	if b.CoverPalette == "" {
		b.CoverPalette = "navy"
	}
	if b.Format == "" {
		b.Format = "EPUB"
	}
	// We don't have a user context on book creation itself — return with
	// Progress=0 and no resume CFI. The next user-scoped query will re-
	// populate those via the LEFT JOIN.
	row := r.pool.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO books (library_id, title, author, format, year, rating, cover_palette,
			                   description, isbn, publisher, series, series_index, tags, path,
			                   has_cover, cover_mime)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
			RETURNING *
		)
		SELECT b.id, b.library_id, b.title, b.author, b.format, b.year,
		       0 AS progress,
		       b.rating, b.cover_palette,
		       b.description, b.isbn, b.publisher, b.series, b.series_index, b.tags,
		       b.created_at, b.path,
		       b.has_cover, b.cover_mime,
		       '' AS resume_cfi
		FROM inserted b
	`,
		b.LibraryID, b.Title, b.Author, b.Format, b.Year, b.Rating, b.CoverPalette,
		b.Description, b.ISBN, b.Publisher, b.Series, b.SeriesIndex, b.Tags, b.Path,
		b.HasCover, b.CoverMime,
	)
	return scanBook(row)
}

func (r *LibraryRepo) GetBookByID(ctx context.Context, userID, id string) (model.Book, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT `+bookCols+`
		`+bookFrom+`
		WHERE b.id = $2 AND b.deleted_at IS NULL
	`, userID, id)
	b, err := scanBook(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.Book{}, ErrNotFound
		}
		return model.Book{}, err
	}
	return b, nil
}

// BookExistsByPath reports whether a non-deleted book already points at
// this file. Used by the library scanner to skip files we've already
// imported.
func (r *LibraryRepo) BookExistsByPath(ctx context.Context, path string) (bool, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM books WHERE path = $1 AND deleted_at IS NULL`,
		path,
	).Scan(&n)
	return n > 0, err
}

// SetCover flips the cover flags on a book. The coverstore is expected to
// have the bytes on disk already (SaveBook); this just records that fact.
func (r *LibraryRepo) SetCover(ctx context.Context, bookID string, hasCover bool, mime string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE books SET has_cover = $2, cover_mime = $3, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
	`, bookID, hasCover, mime)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete hard-deletes a book row by id. FKs on shelf_books, annotations,
// user_book_progress, and reading_sessions are ON DELETE CASCADE so those
// children disappear in the same statement; bookdrop_items.book_id is
// ON DELETE SET NULL so the import history survives the book going away.
// Returns ErrNotFound when the id is unknown (or was already deleted).
func (r *LibraryRepo) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM books WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateMetadata applies the user-editable metadata fields for a book.
func (r *LibraryRepo) UpdateMetadata(ctx context.Context, b model.Book) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE books SET
			title         = $1,
			author        = $2,
			format        = $3,
			year          = $4,
			rating        = $5,
			cover_palette = $6,
			description   = $7,
			isbn          = $8,
			publisher     = $9,
			series        = $10,
			series_index  = $11,
			tags          = $12,
			updated_at    = now()
		WHERE id = $13 AND deleted_at IS NULL
	`,
		b.Title, b.Author, b.Format, b.Year, b.Rating, b.CoverPalette,
		b.Description, b.ISBN, b.Publisher, b.Series, b.SeriesIndex, b.Tags,
		b.ID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// scanner lets us reuse scanBook for both Row and Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanBook(s scanner) (model.Book, error) {
	var b model.Book
	err := s.Scan(
		&b.ID, &b.LibraryID, &b.Title, &b.Author, &b.Format, &b.Year,
		&b.Progress, &b.Rating, &b.CoverPalette,
		&b.Description, &b.ISBN, &b.Publisher, &b.Series, &b.SeriesIndex, &b.Tags,
		&b.CreatedAt, &b.Path,
		&b.HasCover, &b.CoverMime,
		&b.ResumeCFI,
	)
	return b, err
}

func collectBooks(rows pgx.Rows) ([]model.Book, error) {
	var books []model.Book
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			return nil, err
		}
		books = append(books, b)
	}
	return books, rows.Err()
}
