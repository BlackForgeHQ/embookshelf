package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/db/dberr"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/search"
)

// bookCols is the SELECT column list shared by every book-returning query.
// The trailing `COALESCE(ubp.progress, 0)` comes from a LEFT JOIN on
// user_book_progress; callers must alias that join as `ubp` and bind the
// user id as the first parameter ($1 for PG, ?1 for SQLite).
const bookCols = `
	b.id, b.library_id, b.title, b.subtitle, b.author, b.format, b.year,
	b.publish_date, b.language,
	COALESCE(ubp.progress, 0) AS progress,
	b.rating, b.cover_palette,
	b.description, b.isbn, b.isbn10, b.publisher,
	b.series, b.series_index, b.series_total,
	b.genres, b.moods, b.tags,
	b.age_rating, b.content_rating, b.pages, b.public_reviews,
	b.created_at, b.path,
	b.has_cover, b.cover_mime,
	COALESCE(ubp.resume_cfi, '') AS resume_cfi,
	b.title_locked, b.subtitle_locked, b.author_locked,
	b.description_locked, b.publisher_locked, b.series_locked,
	b.isbn_locked, b.isbn10_locked, b.language_locked,
	b.publish_date_locked, b.genres_locked, b.moods_locked,
	b.tags_locked, b.pages_locked, b.cover_locked,
	b.duration_seconds, b.narrator, b.chapters,
	b.uuid, b.folder_path
`

// bookFromPG is the FROM + LEFT JOIN clause for Postgres queries, where
// the user_id parameter is $1 (pgx-style positional placeholder).
const bookFromPG = `
	FROM books b
	LEFT JOIN user_book_progress ubp ON ubp.book_id = b.id AND ubp.user_id = $1
`

// bookFromSQLite is the FROM + LEFT JOIN clause for SQLite queries, where
// the user_id parameter is ?1 (SQLite positional placeholder).
const bookFromSQLite = `
	FROM books b
	LEFT JOIN user_book_progress ubp ON ubp.book_id = b.id AND ubp.user_id = ?1
`

// bookFrom returns the dialect-appropriate FROM clause. Call sites that
// embed bookFrom in a dynamic query string should use this function rather
// than the old bookFrom constant so both backends get the right placeholder.
func bookFromQ(d db.Dialect) string {
	return db.SelectQ(d, bookFromPG, bookFromSQLite)
}

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
	db *db.DB
}

func NewLibraryRepo(d *db.DB) *LibraryRepo {
	return &LibraryRepo{db: d}
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
	) AS book_count,
	l.backend_id, l.root, l.org_mode
`

// libColsReturning is the same projection for RETURNING clauses where
// no table alias is available.
const libColsReturning = `
	id, name, slug, path,
	last_scanned_at, file_count, discovered_count,
	file_naming_pattern, created_at,
	COALESCE(
		(SELECT COUNT(*) FROM books b
		 WHERE b.library_id = libraries.id AND b.deleted_at IS NULL),
		0
	) AS book_count,
	backend_id, root, org_mode
`

// CreateLibrary inserts a new library row and returns the persisted
// record. `slug` is UNIQUE (000001) and `path` is UNIQUE (000018) — a
// collision on either surfaces as a typed sentinel (ErrLibraryNameTaken
// or ErrLibraryPathTaken) so the handler can map it to a 409.
//
// UUID is generated app-side via db.NewID() so the same INSERT works on
// both Postgres (UUID column) and SQLite (TEXT column).
func (r *LibraryRepo) CreateLibrary(ctx context.Context, name, slug, path string) (model.Library, error) {
	id := db.NewID()
	const qPG = `
		INSERT INTO libraries (id, name, slug, path)
		VALUES ($1, $2, $3, $4)
		RETURNING ` + libColsReturning

	const qSQLite = `
		INSERT INTO libraries (id, name, slug, path)
		VALUES (?, ?, ?, ?)
		RETURNING ` + libColsReturning

	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite),
		id, name, slug, path)
	l, err := r.scanLibrary(row)
	if err != nil {
		if ok, constraint := dberr.IsUniqueViolation(err); ok {
			switch constraint {
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
	rows, err := r.db.SQL.QueryContext(ctx, `
		SELECT `+libCols+`
		FROM libraries l
		ORDER BY l.created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var libs []model.Library
	for rows.Next() {
		l, err := r.scanLibrary(rows)
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
	const qPG = `
		SELECT ` + libCols + `
		FROM libraries l
		WHERE l.id = $1
	`
	const qSQLite = `
		SELECT ` + libCols + `
		FROM libraries l
		WHERE l.id = ?
	`
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), id)
	l, err := r.scanLibrary(row)
	if err != nil {
		if dberr.IsNotFound(err) {
			return model.Library{}, ErrNotFound
		}
		return model.Library{}, err
	}
	return l, nil
}

// TouchScan stamps the last-scan aggregate on a library row after a
// filesystem walk completes. Used by the library-scan worker.
func (r *LibraryRepo) TouchScan(ctx context.Context, id string, fileCount, discovered int) error {
	const qPG = `
		UPDATE libraries
		SET last_scanned_at = now(),
		    file_count       = $2,
		    discovered_count = $3
		WHERE id = $1
	`
	const qSQLite = `
		UPDATE libraries
		SET last_scanned_at = CURRENT_TIMESTAMP,
		    file_count       = ?,
		    discovered_count = ?
		WHERE id = ?
	`
	var res sql.Result
	var err error
	if r.db.Dialect == db.DialectSQLite {
		res, err = r.db.SQL.ExecContext(ctx, qSQLite, fileCount, discovered, id)
	} else {
		res, err = r.db.SQL.ExecContext(ctx, qPG, id, fileCount, discovered)
	}
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *LibraryRepo) scanLibrary(s scanner) (model.Library, error) {
	var l model.Library
	var lastScannedAny, createdAny any
	var backendID, root sql.NullString
	err := s.Scan(
		&l.ID, &l.Name, &l.Slug, &l.Path,
		&lastScannedAny, &l.FileCount, &l.DiscoveredCount,
		&l.FileNamingPattern, &createdAny, &l.BookCount,
		&backendID, &root, &l.OrgMode,
	)
	if err != nil {
		return l, err
	}
	if err := db.ScanNullTime(r.db.Dialect, lastScannedAny, &l.LastScannedAt); err != nil {
		return l, fmt.Errorf("scan last_scanned_at: %w", err)
	}
	if err := db.ScanTime(r.db.Dialect, createdAny, &l.CreatedAt); err != nil {
		return l, fmt.Errorf("scan created_at: %w", err)
	}
	if backendID.Valid {
		s := backendID.String
		l.BackendID = &s
	}
	if root.Valid {
		s := root.String
		l.Root = &s
	}
	return l, nil
}

// DeleteLibrary removes a library row and cascades the deletion through
// books, library_paths, shelf_books, annotations, reading_sessions, and
// per-user progress via the existing FK ON DELETE CASCADE chain. The
// returned []bookIDs lets the caller clean up cover-image files on disk
// — those aren't owned by the DB so the cascade can't reach them.
func (r *LibraryRepo) DeleteLibrary(ctx context.Context, id string) ([]string, error) {
	tx, err := r.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	const qBooksPG = `SELECT id FROM books WHERE library_id = $1`
	const qBooksSQLite = `SELECT id FROM books WHERE library_id = ?`
	rows, err := tx.QueryContext(ctx, db.SelectQ(r.db.Dialect, qBooksPG, qBooksSQLite), id)
	if err != nil {
		return nil, err
	}
	var bookIDs []string
	for rows.Next() {
		var bookID string
		if err := rows.Scan(&bookID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		bookIDs = append(bookIDs, bookID)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	const qDelPG = `DELETE FROM libraries WHERE id = $1`
	const qDelSQLite = `DELETE FROM libraries WHERE id = ?`
	res, err := tx.ExecContext(ctx, db.SelectQ(r.db.Dialect, qDelPG, qDelSQLite), id)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return bookIDs, nil
}

// SetFileNamingPattern writes (or clears) the per-library naming pattern.
// Pass nil to revert to the fallback (keep original filename on approval).
func (r *LibraryRepo) SetFileNamingPattern(ctx context.Context, id string, pattern *string) error {
	const qPG = `UPDATE libraries SET file_naming_pattern = $2 WHERE id = $1`
	const qSQLite = `UPDATE libraries SET file_naming_pattern = ? WHERE id = ?`
	var res sql.Result
	var err error
	if r.db.Dialect == db.DialectSQLite {
		res, err = r.db.SQL.ExecContext(ctx, qSQLite, pattern, id)
	} else {
		res, err = r.db.SQL.ExecContext(ctx, qPG, id, pattern)
	}
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
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
	// arg[0] is always the user id (driven by the LEFT JOIN on user_book_progress).
	var (
		where = []string{"b.deleted_at IS NULL"}
		args  = []any{userID}
	)

	if r.db.Dialect == db.DialectSQLite {
		if librarySlug != "" {
			args = append(args, librarySlug)
			where = append(where, fmt.Sprintf("l.slug = ?%d", len(args)))
		}
		if q := strings.TrimSpace(p.Query); q != "" {
			fts := search.EscapeFTS5Query(q)
			if fts != "" {
				args = append(args, fts)
				where = append(where, fmt.Sprintf("b.rowid IN (SELECT rowid FROM books_fts WHERE books_fts MATCH ?%d)", len(args)))
			}
		}
		if len(p.Format) > 0 {
			placeholders := make([]string, len(p.Format))
			for i, f := range p.Format {
				args = append(args, f)
				placeholders[i] = fmt.Sprintf("?%d", len(args))
			}
			where = append(where, "b.format IN ("+strings.Join(placeholders, ",")+")")
		}
	} else {
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
		` + bookFromQ(r.db.Dialect) + `
		JOIN libraries l ON l.id = b.library_id
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY ` + orderBy + `
		LIMIT 500
	`
	rows, err := r.db.SQL.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectBooks(r.db.Dialect, rows)
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
	if b.Genres == nil {
		b.Genres = []string{}
	}
	if b.Moods == nil {
		b.Moods = []string{}
	}

	tagsVal, err := db.ValueStringSlice(r.db.Dialect, b.Tags)
	if err != nil {
		return model.Book{}, fmt.Errorf("encode tags: %w", err)
	}
	genresVal, err := db.ValueStringSlice(r.db.Dialect, b.Genres)
	if err != nil {
		return model.Book{}, fmt.Errorf("encode genres: %w", err)
	}
	moodsVal, err := db.ValueStringSlice(r.db.Dialect, b.Moods)
	if err != nil {
		return model.Book{}, fmt.Errorf("encode moods: %w", err)
	}

	id := db.NewID()

	args := []any{
		id, b.LibraryID, b.Title, b.Subtitle, b.Author, b.Format, b.Year,
		b.PublishDate, b.Language,
		b.Rating, b.CoverPalette,
		b.Description, b.ISBN, b.ISBN10, b.Publisher,
		b.Series, b.SeriesIndex, b.SeriesTotal,
		genresVal, moodsVal, tagsVal,
		b.AgeRating, b.ContentRating, b.Pages, b.PublicReviews,
		b.Path, b.HasCover, b.CoverMime,
	}

	// Postgres: use a CTE so we can SELECT from the inserted row using the
	// bookCols shape (literal 0 for progress, '' for resume_cfi, and all
	// lock columns at their DEFAULT).
	const qPG = `
		WITH inserted AS (
			INSERT INTO books (id, library_id, title, subtitle, author, format, year,
			                   publish_date, language,
			                   rating, cover_palette,
			                   description, isbn, isbn10, publisher,
			                   series, series_index, series_total,
			                   genres, moods, tags,
			                   age_rating, content_rating, pages, public_reviews,
			                   path, has_cover, cover_mime)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,
			        $19,$20,$21,$22,$23,$24,$25,$26,$27,$28)
			RETURNING *
		)
		SELECT b.id, b.library_id, b.title, b.subtitle, b.author, b.format, b.year,
		       b.publish_date, b.language,
		       0 AS progress,
		       b.rating, b.cover_palette,
		       b.description, b.isbn, b.isbn10, b.publisher,
		       b.series, b.series_index, b.series_total,
		       b.genres, b.moods, b.tags,
		       b.age_rating, b.content_rating, b.pages, b.public_reviews,
		       b.created_at, b.path,
		       b.has_cover, b.cover_mime,
		       '' AS resume_cfi,
		       b.title_locked, b.subtitle_locked, b.author_locked,
		       b.description_locked, b.publisher_locked, b.series_locked,
		       b.isbn_locked, b.isbn10_locked, b.language_locked,
		       b.publish_date_locked, b.genres_locked, b.moods_locked,
		       b.tags_locked, b.pages_locked, b.cover_locked,
		       b.duration_seconds, b.narrator, b.chapters,
		       b.uuid, b.folder_path
		FROM inserted b
	`

	// SQLite: writeable CTEs are not supported. Use a plain INSERT … RETURNING
	// with an explicit column list that mirrors bookCols (literal 0/'' for
	// the derived columns that don't exist in the table).
	const qSQLite = `
		INSERT INTO books (id, library_id, title, subtitle, author, format, year,
		                   publish_date, language,
		                   rating, cover_palette,
		                   description, isbn, isbn10, publisher,
		                   series, series_index, series_total,
		                   genres, moods, tags,
		                   age_rating, content_rating, pages, public_reviews,
		                   path, has_cover, cover_mime)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,
		        ?,?,?,?,?,?,?,?,?,?)
		RETURNING
		    id, library_id, title, subtitle, author, format, year,
		    publish_date, language,
		    0 AS progress,
		    rating, cover_palette,
		    description, isbn, isbn10, publisher,
		    series, series_index, series_total,
		    genres, moods, tags,
		    age_rating, content_rating, pages, public_reviews,
		    created_at, path,
		    has_cover, cover_mime,
		    '' AS resume_cfi,
		    title_locked, subtitle_locked, author_locked,
		    description_locked, publisher_locked, series_locked,
		    isbn_locked, isbn10_locked, language_locked,
		    publish_date_locked, genres_locked, moods_locked,
		    tags_locked, pages_locked, cover_locked,
		    duration_seconds, narrator, chapters,
		    uuid, folder_path
	`

	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), args...)
	return scanBook(r.db.Dialect, row)
}

func (r *LibraryRepo) GetBookByID(ctx context.Context, userID, id string) (model.Book, error) {
	const qPG = `
		SELECT ` + bookCols + `
		` + bookFromPG + `
		WHERE b.id = $2 AND b.deleted_at IS NULL
	`
	const qSQLite = `
		SELECT ` + bookCols + `
		` + bookFromSQLite + `
		WHERE b.id = ?2 AND b.deleted_at IS NULL
	`
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), userID, id)
	b, err := scanBook(r.db.Dialect, row)
	if err != nil {
		if dberr.IsNotFound(err) {
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
	const qPG = `SELECT count(*) FROM books WHERE path = $1 AND deleted_at IS NULL`
	const qSQLite = `SELECT count(*) FROM books WHERE path = ? AND deleted_at IS NULL`
	var n int
	err := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), path).Scan(&n)
	return n > 0, err
}

// SetCover flips the cover flags on a book. The coverstore is expected to
// have the bytes on disk already (SaveBook); this just records that fact.
func (r *LibraryRepo) SetCover(ctx context.Context, bookID string, hasCover bool, mime string) error {
	const qPG = `
		UPDATE books SET has_cover = $2, cover_mime = $3, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
	`
	const qSQLite = `
		UPDATE books SET has_cover = ?, cover_mime = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND deleted_at IS NULL
	`
	var res sql.Result
	var err error
	if r.db.Dialect == db.DialectSQLite {
		res, err = r.db.SQL.ExecContext(ctx, qSQLite, hasCover, mime, bookID)
	} else {
		res, err = r.db.SQL.ExecContext(ctx, qPG, bookID, hasCover, mime)
	}
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
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
	const qPG = `DELETE FROM books WHERE id = $1`
	const qSQLite = `DELETE FROM books WHERE id = ?`
	res, err := r.db.SQL.ExecContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateMetadata applies the user-editable metadata fields for a book,
// including the per-field lock flags. Manual edits (PATCH /books/:id)
// flow through here; the apply-metadata path (PUT /books/:id/metadata)
// also writes via this method after the service has filtered locked
// fields out of the candidate.
func (r *LibraryRepo) UpdateMetadata(ctx context.Context, b model.Book) error {
	if b.Genres == nil {
		b.Genres = []string{}
	}
	if b.Moods == nil {
		b.Moods = []string{}
	}
	if b.Tags == nil {
		b.Tags = []string{}
	}

	genresVal, err := db.ValueStringSlice(r.db.Dialect, b.Genres)
	if err != nil {
		return fmt.Errorf("encode genres: %w", err)
	}
	moodsVal, err := db.ValueStringSlice(r.db.Dialect, b.Moods)
	if err != nil {
		return fmt.Errorf("encode moods: %w", err)
	}
	tagsVal, err := db.ValueStringSlice(r.db.Dialect, b.Tags)
	if err != nil {
		return fmt.Errorf("encode tags: %w", err)
	}

	const qPG = `
		UPDATE books SET
			title          = $1,
			subtitle       = $2,
			author         = $3,
			format         = $4,
			year           = $5,
			publish_date   = $6,
			language       = $7,
			rating         = $8,
			cover_palette  = $9,
			description    = $10,
			isbn           = $11,
			isbn10         = $12,
			publisher      = $13,
			series         = $14,
			series_index   = $15,
			series_total   = $16,
			genres         = $17,
			moods          = $18,
			tags           = $19,
			age_rating     = $20,
			content_rating = $21,
			pages          = $22,
			public_reviews = $23,
			title_locked          = $24,
			subtitle_locked       = $25,
			author_locked         = $26,
			description_locked    = $27,
			publisher_locked      = $28,
			series_locked         = $29,
			isbn_locked           = $30,
			isbn10_locked         = $31,
			language_locked       = $32,
			publish_date_locked   = $33,
			genres_locked         = $34,
			moods_locked          = $35,
			tags_locked           = $36,
			pages_locked          = $37,
			cover_locked          = $38,
			updated_at     = now()
		WHERE id = $39 AND deleted_at IS NULL
	`
	const qSQLite = `
		UPDATE books SET
			title          = ?,
			subtitle       = ?,
			author         = ?,
			format         = ?,
			year           = ?,
			publish_date   = ?,
			language       = ?,
			rating         = ?,
			cover_palette  = ?,
			description    = ?,
			isbn           = ?,
			isbn10         = ?,
			publisher      = ?,
			series         = ?,
			series_index   = ?,
			series_total   = ?,
			genres         = ?,
			moods          = ?,
			tags           = ?,
			age_rating     = ?,
			content_rating = ?,
			pages          = ?,
			public_reviews = ?,
			title_locked          = ?,
			subtitle_locked       = ?,
			author_locked         = ?,
			description_locked    = ?,
			publisher_locked      = ?,
			series_locked         = ?,
			isbn_locked           = ?,
			isbn10_locked         = ?,
			language_locked       = ?,
			publish_date_locked   = ?,
			genres_locked         = ?,
			moods_locked          = ?,
			tags_locked           = ?,
			pages_locked          = ?,
			cover_locked          = ?,
			updated_at     = CURRENT_TIMESTAMP
		WHERE id = ? AND deleted_at IS NULL
	`

	var res sql.Result
	if r.db.Dialect == db.DialectSQLite {
		res, err = r.db.SQL.ExecContext(ctx, qSQLite,
			b.Title, b.Subtitle, b.Author, b.Format, b.Year,
			b.PublishDate, b.Language,
			b.Rating, b.CoverPalette,
			b.Description, b.ISBN, b.ISBN10, b.Publisher,
			b.Series, b.SeriesIndex, b.SeriesTotal,
			genresVal, moodsVal, tagsVal,
			b.AgeRating, b.ContentRating, b.Pages, b.PublicReviews,
			b.Locks.Title, b.Locks.Subtitle, b.Locks.Author,
			b.Locks.Description, b.Locks.Publisher, b.Locks.Series,
			b.Locks.ISBN, b.Locks.ISBN10, b.Locks.Language,
			b.Locks.PublishDate, b.Locks.Genres, b.Locks.Moods,
			b.Locks.Tags, b.Locks.Pages, b.Locks.Cover,
			b.ID,
		)
	} else {
		res, err = r.db.SQL.ExecContext(ctx, qPG,
			b.Title, b.Subtitle, b.Author, b.Format, b.Year,
			b.PublishDate, b.Language,
			b.Rating, b.CoverPalette,
			b.Description, b.ISBN, b.ISBN10, b.Publisher,
			b.Series, b.SeriesIndex, b.SeriesTotal,
			genresVal, moodsVal, tagsVal,
			b.AgeRating, b.ContentRating, b.Pages, b.PublicReviews,
			b.Locks.Title, b.Locks.Subtitle, b.Locks.Author,
			b.Locks.Description, b.Locks.Publisher, b.Locks.Series,
			b.Locks.ISBN, b.Locks.ISBN10, b.Locks.Language,
			b.Locks.PublishDate, b.Locks.Genres, b.Locks.Moods,
			b.Locks.Tags, b.Locks.Pages, b.Locks.Cover,
			b.ID,
		)
	}
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateAudio writes the audiobook-specific metadata fields onto an
// existing books row. Used right after Create() in the bookdrop Approve
// flow for MP3/M4B imports — those fields aren't part of the bookdrop
// review surface, so it's cheaper to re-extract on approval than to
// schema-bloat bookdrop_items.
//
// chapters is JSON-encoded into TEXT (SQLite) / JSONB (PG). Passing
// a nil slice writes SQL NULL so the UI can distinguish "no chapter
// metadata" from "empty chapter list".
func (r *LibraryRepo) UpdateAudio(
	ctx context.Context,
	id string,
	durationSeconds *int,
	narrator string,
	chapters []model.Chapter,
) error {
	var chaptersVal any
	if chapters != nil {
		b, err := json.Marshal(chapters)
		if err != nil {
			return fmt.Errorf("encode chapters: %w", err)
		}
		chaptersVal = string(b)
	}

	const qPG = `
		UPDATE books SET
			duration_seconds = $1,
			narrator         = $2,
			chapters         = $3,
			updated_at       = now()
		WHERE id = $4 AND deleted_at IS NULL
	`
	const qSQLite = `
		UPDATE books SET
			duration_seconds = ?,
			narrator         = ?,
			chapters         = ?,
			updated_at       = (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		WHERE id = ? AND deleted_at IS NULL
	`
	res, err := r.db.SQL.ExecContext(ctx,
		db.SelectQ(r.db.Dialect, qPG, qSQLite),
		durationSeconds, narrator, chaptersVal, id,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// scanner lets us reuse scanBook for both Row and Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanBook(d db.Dialect, s scanner) (model.Book, error) {
	var b model.Book
	var genresAny, moodsAny, tagsAny, publishDateAny, createdAny any
	var durationAny, chaptersAny any
	var bookUUID, folderPath sql.NullString
	err := s.Scan(
		&b.ID, &b.LibraryID, &b.Title, &b.Subtitle, &b.Author, &b.Format, &b.Year,
		&publishDateAny, &b.Language,
		&b.Progress, &b.Rating, &b.CoverPalette,
		&b.Description, &b.ISBN, &b.ISBN10, &b.Publisher,
		&b.Series, &b.SeriesIndex, &b.SeriesTotal,
		&genresAny, &moodsAny, &tagsAny,
		&b.AgeRating, &b.ContentRating, &b.Pages, &b.PublicReviews,
		&createdAny, &b.Path,
		&b.HasCover, &b.CoverMime,
		&b.ResumeCFI,
		&b.Locks.Title, &b.Locks.Subtitle, &b.Locks.Author,
		&b.Locks.Description, &b.Locks.Publisher, &b.Locks.Series,
		&b.Locks.ISBN, &b.Locks.ISBN10, &b.Locks.Language,
		&b.Locks.PublishDate, &b.Locks.Genres, &b.Locks.Moods,
		&b.Locks.Tags, &b.Locks.Pages, &b.Locks.Cover,
		&durationAny, &b.Narrator, &chaptersAny,
		&bookUUID, &folderPath,
	)
	if err != nil {
		return b, err
	}
	if err := db.ScanNullTime(d, publishDateAny, &b.PublishDate); err != nil {
		return b, fmt.Errorf("scan publish_date: %w", err)
	}
	if err := db.ScanTime(d, createdAny, &b.CreatedAt); err != nil {
		return b, fmt.Errorf("scan created_at: %w", err)
	}
	if err := db.ScanStringSlice(d, genresAny, &b.Genres); err != nil {
		return b, fmt.Errorf("scan genres: %w", err)
	}
	if err := db.ScanStringSlice(d, moodsAny, &b.Moods); err != nil {
		return b, fmt.Errorf("scan moods: %w", err)
	}
	if err := db.ScanStringSlice(d, tagsAny, &b.Tags); err != nil {
		return b, fmt.Errorf("scan tags: %w", err)
	}
	if v, ok := durationAny.(int64); ok {
		n := int(v)
		b.DurationSeconds = &n
	}
	// chapters: TEXT JSON on SQLite, JSONB on PG (delivered as []byte or
	// string by pgx stdlib). NULL → nil slice; non-NULL → JSON-decode.
	if chaptersAny != nil {
		var raw []byte
		switch v := chaptersAny.(type) {
		case []byte:
			raw = v
		case string:
			raw = []byte(v)
		}
		if len(raw) > 0 {
			var ch []model.Chapter
			if err := json.Unmarshal(raw, &ch); err == nil && len(ch) > 0 {
				b.Chapters = ch
			}
		}
	}
	if bookUUID.Valid {
		s := bookUUID.String
		b.UUID = &s
	}
	if folderPath.Valid {
		s := folderPath.String
		b.FolderPath = &s
	}
	return b, nil
}

// collectBooks iterates rows into a slice of Book values. The caller is
// responsible for closing rows (typically via defer) before or after this call.
func collectBooks(d db.Dialect, rows *sql.Rows) ([]model.Book, error) {
	var books []model.Book
	for rows.Next() {
		b, err := scanBook(d, rows)
		if err != nil {
			return nil, err
		}
		books = append(books, b)
	}
	return books, rows.Err()
}

// SuggestBook is the slim shape returned by SearchSuggestBooks. No progress,
// no locks, no extended metadata — just enough for an autocomplete row.
type SuggestBook struct {
	ID       string
	Title    string
	Author   string
	HasCover bool
}

// SuggestLibrary is the slim shape for library autocomplete rows.
type SuggestLibrary struct {
	ID   string
	Name string
	Slug string
}

// SearchSuggestBooks returns the top `limit` books matching `q` for the
// autocomplete surfaces. Reuses the same FTS infrastructure as the main
// book listing. `limit` is assumed already clamped by the caller (service
// caps at 20).
func (r *LibraryRepo) SearchSuggestBooks(ctx context.Context, q string, limit int) ([]SuggestBook, error) {
	const qPG = `
		SELECT b.id, b.title, b.author, b.has_cover
		FROM books b
		WHERE b.deleted_at IS NULL
		  AND b.tsv @@ websearch_to_tsquery('english', $1)
		ORDER BY ts_rank(b.tsv, websearch_to_tsquery('english', $1)) DESC,
		         b.title ASC
		LIMIT $2
	`
	const qSQLite = `
		SELECT b.id, b.title, b.author, b.has_cover
		FROM books b
		WHERE b.deleted_at IS NULL
		  AND b.rowid IN (SELECT rowid FROM books_fts WHERE books_fts MATCH ?1)
		ORDER BY (
		    SELECT bm25(books_fts) FROM books_fts
		    WHERE books_fts.rowid = b.rowid AND books_fts MATCH ?1
		) ASC,
		b.title ASC
		LIMIT ?2
	`

	var rows *sql.Rows
	var err error
	if r.db.Dialect == db.DialectSQLite {
		fts := search.EscapeFTS5Query(q)
		if fts == "" {
			return nil, nil
		}
		rows, err = r.db.SQL.QueryContext(ctx, qSQLite, fts, limit)
	} else {
		rows, err = r.db.SQL.QueryContext(ctx, qPG, q, limit)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SuggestBook
	for rows.Next() {
		var b SuggestBook
		if err := rows.Scan(&b.ID, &b.Title, &b.Author, &b.HasCover); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// SearchSuggestLibraries returns libraries whose name matches `q`. Today
// every authenticated user can see every library (mirrors GET /libraries),
// so there is no per-user filter here — adopt one if/when library
// visibility becomes user-scoped.
func (r *LibraryRepo) SearchSuggestLibraries(ctx context.Context, q string, limit int) ([]SuggestLibrary, error) {
	// ILIKE is Postgres-specific; SQLite's LIKE is case-insensitive for ASCII
	// by default (the project's SQLite pragma does not override that).
	const qPG = `
		SELECT l.id, l.name, l.slug
		FROM libraries l
		WHERE l.name ILIKE '%' || $1 || '%'
		ORDER BY l.name ASC
		LIMIT $2
	`
	const qSQLite = `
		SELECT l.id, l.name, l.slug
		FROM libraries l
		WHERE l.name LIKE '%' || ? || '%'
		ORDER BY l.name ASC
		LIMIT ?
	`
	rows, err := r.db.SQL.QueryContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), q, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SuggestLibrary
	for rows.Next() {
		var l SuggestLibrary
		if err := rows.Scan(&l.ID, &l.Name, &l.Slug); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// LibraryBackend returns the storage_backends row associated with the given
// library by joining through the backend_id FK. Returns ErrNotFound when the
// library either does not exist or has no backend_id set yet.
func (r *LibraryRepo) LibraryBackend(ctx context.Context, libraryID string) (model.StorageBackend, error) {
	const qPG = `
		SELECT sb.id, sb.kind, sb.config, sb.created_at
		FROM libraries l
		JOIN storage_backends sb ON sb.id = l.backend_id
		WHERE l.id = $1
	`
	const qSQLite = `
		SELECT sb.id, sb.kind, sb.config, sb.created_at
		FROM libraries l
		JOIN storage_backends sb ON sb.id = l.backend_id
		WHERE l.id = ?
	`
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), libraryID)

	// Re-use the same scan logic as StorageBackendRepo to avoid duplication.
	var b model.StorageBackend
	var configRaw, createdAny any
	if err := row.Scan(&b.ID, &b.Kind, &configRaw, &createdAny); err != nil {
		if dberr.IsNotFound(err) {
			return model.StorageBackend{}, ErrNotFound
		}
		return model.StorageBackend{}, err
	}

	var raw []byte
	switch v := configRaw.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return model.StorageBackend{}, fmt.Errorf("unexpected type for config column: %T", configRaw)
	}
	if err := json.Unmarshal(raw, &b.Config); err != nil {
		return model.StorageBackend{}, fmt.Errorf("decode config: %w", err)
	}
	if err := db.ScanTime(r.db.Dialect, createdAny, &b.CreatedAt); err != nil {
		return model.StorageBackend{}, fmt.Errorf("scan created_at: %w", err)
	}
	return b, nil
}

// SetBackendID wires a library to a storage backend by writing the
// backend_id FK column. Pass an empty string to clear the association.
// Used by StorageBackendRepo tests and the library-update handler.
func (r *LibraryRepo) SetBackendID(ctx context.Context, libraryID, backendID string) error {
	const qPG = `UPDATE libraries SET backend_id = $2 WHERE id = $1`
	const qSQLite = `UPDATE libraries SET backend_id = ? WHERE id = ?`
	var nilableBackend any
	if backendID != "" {
		nilableBackend = backendID
	}
	var res sql.Result
	var err error
	if r.db.Dialect == db.DialectSQLite {
		res, err = r.db.SQL.ExecContext(ctx, qSQLite, nilableBackend, libraryID)
	} else {
		res, err = r.db.SQL.ExecContext(ctx, qPG, libraryID, nilableBackend)
	}
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
