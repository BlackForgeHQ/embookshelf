// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/db/dberr"
	"github.com/blackforge/embookshelf/internal/model"
)

// bookCols is the SELECT column list shared by every book-returning query.
// The trailing `COALESCE(ubp.progress, 0)` comes from a LEFT JOIN on
// user_book_progress; callers must alias that join as `ubp` and bind the
// user id as the first parameter ($1).
var bookCols = `
	b.id, b.library_id, b.title, b.subtitle, b.author, b.format, b.year,
	b.publish_date, b.language,
	COALESCE(ubp.progress, 0) AS progress,
	b.rating, b.cover_palette,
	b.description, b.isbn, b.isbn10, b.publisher,
	b.series, b.series_index, b.series_total,
	b.genres, b.moods, b.tags,
	b.age_rating, b.content_rating, b.pages, b.public_reviews,
	b.created_at, b.path,
	b.has_cover, b.cover_mime, b.cover_hash,
	COALESCE(ubp.resume_cfi, '') AS resume_cfi,
	` + lockColumnList("b.") + `,
	b.duration_seconds, b.narrator, b.chapters,
	b.uuid, b.folder_path
`

// lockColumnList renders the books lock columns, in model.LockSpecs
// order, with the given qualifier. Every SQL context that names the lock
// block goes through here, so the SELECT order, the scan destinations in
// scanBook and the UPDATE SET list are one traversal of one declaration
// rather than four hand-kept lists — the Column-order coupling hazard
// applied to the part of the row that grows.
func lockColumnList(qualifier string) string {
	cols := model.LockColumns()
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = qualifier + c
	}
	return strings.Join(out, ", ")
}

// lockSetList renders "title_locked = $n, ..." starting at placeholder
// first, paired with model.LockValues for the arguments.
func lockSetList(first int) string {
	cols := model.LockColumns()
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c + " = $" + strconv.Itoa(first+i)
	}
	return strings.Join(out, ", ")
}

// bookFromPG is the FROM + LEFT JOIN clause for book queries, where
// the user_id parameter is $1.
// NULLIF makes "no user" a supported input: backfill tasks and admin
// reads legitimately want a book row without per-user progress, and an
// empty string is not a valid uuid. NULL never matches, so the LEFT JOIN
// yields NULL progress columns rather than erroring.
const bookFromPG = `
	FROM books b
	LEFT JOIN user_book_progress ubp ON ubp.book_id = b.id AND ubp.user_id = NULLIF($1, '')::uuid
`

// BookRepo owns SQL for the books table. Split out from LibraryRepo so
// the file name matches its actual scope. JOINs across libraries are
// fine here when the *return* shape is books.
type BookRepo struct {
	db *db.DB
}

func NewBookRepo(d *db.DB) *BookRepo {
	return &BookRepo{db: d}
}

// Search lists books scoped to a specific user's progress. An empty
// librarySlug means "across all libraries"; passing a slug filters down.
// Always capped at 500 rows today — the Library UI renders them all
// client-side. Server-side pagination is a future slice when library
// sizes demand it.
func (r *BookRepo) Search(ctx context.Context, userID, librarySlug string, p model.SearchParams) ([]model.Book, error) {
	// arg[0] is always the user id (driven by the LEFT JOIN on user_book_progress).
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
	if p.Unshelved {
		args = append(args, userID)
		where = append(where, fmt.Sprintf(`NOT EXISTS (
			SELECT 1 FROM shelf_books sb
			JOIN shelves s ON s.id = sb.shelf_id
			WHERE sb.book_id = b.id
			  AND s.user_id = $%d
			  AND s.is_smart = false
			  AND s.slug NOT IN ('reading','finished')
		)`, len(args)))
	}

	// Unshelved is a triage view — newest imports float to the top by
	// default so the user shelves them first. Explicit p.Sort overrides.
	var orderBy string
	switch p.Sort {
	case "title":
		orderBy = "b.title ASC"
	case "author":
		orderBy = "b.author ASC, b.title ASC"
	case "recent":
		orderBy = "b.created_at DESC"
	case "year":
		orderBy = "b.year DESC, b.title ASC"
	case "rating":
		orderBy = "b.rating DESC, b.title ASC"
	default:
		if p.Unshelved {
			orderBy = "b.created_at DESC"
		} else {
			orderBy = "b.title ASC"
		}
	}

	query := `
		SELECT ` + bookCols + `
		` + bookFromPG + `
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
	return collectBooks(rows)
}

// BooksByLibrarySlug is retained for the home dashboard's simple count.
func (r *BookRepo) BooksByLibrarySlug(ctx context.Context, userID, slug string) ([]model.Book, error) {
	return r.Search(ctx, userID, slug, model.SearchParams{})
}

// Create inserts a new book row. Progress is not a column anymore — callers
// that want to record progress for the creator should call ProgressRepo.Set.
func (r *BookRepo) Create(ctx context.Context, b model.Book) (model.Book, error) {
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

	id := db.NewID()

	var folderPathArg any
	if b.FolderPath != nil {
		folderPathArg = *b.FolderPath
	}

	args := []any{
		id, b.LibraryID, b.Title, b.Subtitle, b.Author, b.Format, b.Year,
		b.PublishDate, b.Language,
		b.Rating, b.CoverPalette,
		b.Description, b.ISBN, b.ISBN10, b.Publisher,
		b.Series, b.SeriesIndex, b.SeriesTotal,
		b.Genres, b.Moods, b.Tags,
		b.AgeRating, b.ContentRating, b.Pages, b.PublicReviews,
		b.Path, b.HasCover, b.CoverMime, folderPathArg,
	}

	qPG := `
		WITH inserted AS (
			INSERT INTO books (id, library_id, title, subtitle, author, format, year,
			                   publish_date, language,
			                   rating, cover_palette,
			                   description, isbn, isbn10, publisher,
			                   series, series_index, series_total,
			                   genres, moods, tags,
			                   age_rating, content_rating, pages, public_reviews,
			                   path, has_cover, cover_mime, folder_path)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,
			        $19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29)
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
		       b.has_cover, b.cover_mime, b.cover_hash,
		       '' AS resume_cfi,
		       ` + lockColumnList("b.") + `,
		       b.duration_seconds, b.narrator, b.chapters,
		       b.uuid, b.folder_path
		FROM inserted b
	`

	row := r.db.SQL.QueryRowContext(ctx, qPG, args...)
	return scanBook(row)
}

var bookGetByIDQuery = `
	SELECT ` + bookCols + `
	` + bookFromPG + `
	WHERE b.id = $2 AND b.deleted_at IS NULL
`

func (r *BookRepo) GetByID(ctx context.Context, userID, id string) (model.Book, error) {
	qPG := bookGetByIDQuery
	row := r.db.SQL.QueryRowContext(ctx, qPG, userID, id)
	b, err := scanBook(row)
	if err != nil {
		if dberr.IsNotFound(err) {
			return model.Book{}, ErrNotFound
		}
		return model.Book{}, err
	}
	return b, nil
}

// ExistsByPath reports whether a non-deleted book already points at this
// file. Used by the library scanner to skip files we've already imported.
func (r *BookRepo) ExistsByPath(ctx context.Context, path string) (bool, error) {
	const qPG = `SELECT count(*) FROM books WHERE path = $1 AND deleted_at IS NULL`
	var n int
	err := r.db.SQL.QueryRowContext(ctx, qPG, path).Scan(&n)
	return n > 0, err
}

// SetCover flips the cover flags on a book. The coverstore is expected to
// have the bytes on disk already (SaveBook); this just records that fact.
func (r *BookRepo) SetCover(ctx context.Context, bookID string, hasCover bool, mime string) error {
	const qPG = `
		UPDATE books SET has_cover = $2, cover_mime = $3, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
	`
	res, err := r.db.SQL.ExecContext(ctx, qPG, bookID, hasCover, mime)
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

// SetCoverHash records the sha256 of the cover image. NULL means
// "not yet hashed" (covers backfill will set it).
func (r *BookRepo) SetCoverHash(ctx context.Context, bookID string, hash []byte) error {
	const qPG = `UPDATE books SET cover_hash = $1 WHERE id = $2`
	_, err := r.db.SQL.ExecContext(ctx, qPG, hash, bookID)
	return err
}

// ListMissingCoverHash returns books that have a cover on disk
// (has_cover = TRUE) but have not yet been assigned a cover_hash.
// Used by the boot-time covers backfill worker. batchSize controls the
// LIMIT applied; 0 defaults to 100. Re-issued each call so Drain can
// page through all pending rows.
var bookMissingCoverHashQuery = `SELECT ` + bookCols + ` ` + bookFromPG + `
	WHERE b.has_cover = TRUE AND b.cover_hash IS NULL AND b.deleted_at IS NULL
	LIMIT $2`

func (r *BookRepo) ListMissingCoverHash(ctx context.Context, batchSize int) ([]model.Book, error) {
	if batchSize <= 0 {
		batchSize = 100
	}
	qPG := bookMissingCoverHashQuery
	// user_id = NULL never matches user_book_progress; the backfill only needs
	// cover data, not per-user progress. Empty string would 22P02 against the
	// UUID column on Postgres.
	rows, err := r.db.SQL.QueryContext(ctx, qPG, nil, batchSize)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectBooks(rows)
}

// Delete hard-deletes a book row by id. FKs on shelf_books, annotations,
// user_book_progress, and reading_sessions are ON DELETE CASCADE so those
// children disappear in the same statement; bookdrop_items.book_id is
// ON DELETE SET NULL so the import history survives the book going away.
// Returns ErrNotFound when the id is unknown (or was already deleted).
func (r *BookRepo) Delete(ctx context.Context, id string) error {
	const qPG = `DELETE FROM books WHERE id = $1`
	res, err := r.db.SQL.ExecContext(ctx, qPG, id)
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

// SetFolderPath updates the books.folder_path + books.path for a book.
// Used by MetadataWriter after a successful folder rename so the DB
// reflects the new on-disk location. An empty folderPath is stored as
// NULL (legacy flat-layout sentinel). Path is rewritten in tandem so
// callers fetching the book see the new file location.
func (r *BookRepo) SetFolderPath(ctx context.Context, bookID, folderPath, path string) error {
	var folderArg any
	if folderPath != "" {
		folderArg = folderPath
	}
	const qPG = `
		UPDATE books SET folder_path = $2, path = $3, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
	`
	res, err := r.db.SQL.ExecContext(ctx, qPG, bookID, folderArg, path)
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

// FileLocationUpdate is one row mutation for RenameFolderTx: the
// files.id whose location should be rewritten and the new
// library-relative location to store.
type FileLocationUpdate struct {
	FileID   string
	Location string
}

// RenameFolderTxArgs bundles the inputs for RenameFolderTx — the
// single-transaction DB swap that finalises an S3 folder rename per
// ADR-0005. Files are updated row-by-row inside the tx; orphans are
// inserted in the same tx so the sweeper either sees the post-rename
// state or none of it.
type RenameFolderTxArgs struct {
	BookID    string
	NewFolder string
	NewPath   string
	Files     []FileLocationUpdate
	Orphans   []PendingOrphanInsert
}

// RenameFolderTx applies the post-copy DB swap atomically:
//   - rewrites every files.location supplied,
//   - sets books.folder_path + books.path,
//   - enqueues old keys into pending_orphans for the sweeper.
//
// All three happen in one COMMIT so the sweeper either sees the
// post-rename state or never sees the orphan rows at all. On any
// step error the tx rolls back; the caller is responsible for
// scheduling cleanup of half-rename garbage on the storage side
// via a separate short-grace orphan insert.
func (r *BookRepo) RenameFolderTx(ctx context.Context, args RenameFolderTxArgs) error {
	tx, err := r.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	const qFilePG = `UPDATE files SET location = $1 WHERE id = $2`
	for _, f := range args.Files {
		if _, err := tx.ExecContext(ctx, qFilePG, f.Location, f.FileID); err != nil {
			return fmt.Errorf("update files.location for %s: %w", f.FileID, err)
		}
	}

	var folderArg any
	if args.NewFolder != "" {
		folderArg = args.NewFolder
	}
	const qBookPG = `
		UPDATE books SET folder_path = $2, path = $3, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
	`
	res, err := tx.ExecContext(ctx, qBookPG, args.BookID, folderArg, args.NewPath)
	if err != nil {
		return fmt.Errorf("update books folder_path: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}

	if len(args.Orphans) > 0 {
		if err := insertPendingOrphansInTx(ctx, tx, args.Orphans); err != nil {
			return fmt.Errorf("enqueue pending_orphans: %w", err)
		}
	}

	return tx.Commit()
}

// UpdateMetadata applies the user-editable metadata fields for a book,
// including the per-field lock flags. Manual edits (PATCH /books/:id)
// flow through here; the apply-metadata path (PUT /books/:id/metadata)
// also writes via this method after the service has filtered locked
// fields out of the candidate.
func (r *BookRepo) UpdateMetadata(ctx context.Context, b model.Book) error {
	if b.Genres == nil {
		b.Genres = []string{}
	}
	if b.Moods == nil {
		b.Moods = []string{}
	}
	if b.Tags == nil {
		b.Tags = []string{}
	}

	// The scalar placeholders come first, then the lock block, then the
	// id — all three numbered from one count, so adding a lock cannot
	// leave the WHERE clause pointing at a lock argument.
	const scalars = 23
	qPG := `
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
			` + lockSetList(scalars+1) + `,
			updated_at     = now()
		WHERE id = $` + strconv.Itoa(scalars+len(model.LockSpecs)+1) + ` AND deleted_at IS NULL
	`

	args := []any{
		b.Title, b.Subtitle, b.Author, b.Format, b.Year,
		b.PublishDate, b.Language,
		b.Rating, b.CoverPalette,
		b.Description, b.ISBN, b.ISBN10, b.Publisher,
		b.Series, b.SeriesIndex, b.SeriesTotal,
		b.Genres, b.Moods, b.Tags,
		b.AgeRating, b.ContentRating, b.Pages, b.PublicReviews,
	}
	if len(args) != scalars {
		return fmt.Errorf("book update: %d scalar args, want %d", len(args), scalars)
	}
	args = append(args, model.LockValues(b.Locks)...)
	args = append(args, b.ID)

	res, err := r.db.SQL.ExecContext(ctx, qPG, args...)
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
// chapters is JSON-encoded into JSONB. Passing a nil slice writes SQL
// NULL so the UI can distinguish "no chapter metadata" from "empty
// chapter list".
func (r *BookRepo) UpdateAudio(
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
	res, err := r.db.SQL.ExecContext(ctx, qPG,
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

// scanner lets us reuse scanBook/scanLibrary for both Row and Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanBook(s scanner) (model.Book, error) {
	var b model.Book
	var genresAny, moodsAny, tagsAny, publishDateAny, createdAny any
	var durationAny, chaptersAny any
	var coverHashAny any
	var bookUUID, folderPath sql.NullString
	dest := []any{
		&b.ID, &b.LibraryID, &b.Title, &b.Subtitle, &b.Author, &b.Format, &b.Year,
		&publishDateAny, &b.Language,
		&b.Progress, &b.Rating, &b.CoverPalette,
		&b.Description, &b.ISBN, &b.ISBN10, &b.Publisher,
		&b.Series, &b.SeriesIndex, &b.SeriesTotal,
		&genresAny, &moodsAny, &tagsAny,
		&b.AgeRating, &b.ContentRating, &b.Pages, &b.PublicReviews,
		&createdAny, &b.Path,
		&b.HasCover, &b.CoverMime, &coverHashAny,
		&b.ResumeCFI,
	}
	dest = append(dest, model.LockFlags(&b.Locks)...)
	dest = append(dest,
		&durationAny, &b.Narrator, &chaptersAny,
		&bookUUID, &folderPath,
	)
	err := s.Scan(dest...)
	if err != nil {
		return b, err
	}
	if err := db.ScanNullTime(publishDateAny, &b.PublishDate); err != nil {
		return b, fmt.Errorf("scan publish_date: %w", err)
	}
	if err := db.ScanTime(createdAny, &b.CreatedAt); err != nil {
		return b, fmt.Errorf("scan created_at: %w", err)
	}
	if err := db.ScanStringSlice(genresAny, &b.Genres); err != nil {
		return b, fmt.Errorf("scan genres: %w", err)
	}
	if err := db.ScanStringSlice(moodsAny, &b.Moods); err != nil {
		return b, fmt.Errorf("scan moods: %w", err)
	}
	if err := db.ScanStringSlice(tagsAny, &b.Tags); err != nil {
		return b, fmt.Errorf("scan tags: %w", err)
	}
	if v, ok := durationAny.(int64); ok {
		n := int(v)
		b.DurationSeconds = &n
	}
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
	if coverHashAny != nil {
		switch v := coverHashAny.(type) {
		case []byte:
			if len(v) > 0 {
				b.CoverHash = v
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

func collectBooks(rows *sql.Rows) ([]model.Book, error) {
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

// SuggestBook is the slim shape returned by SearchSuggest. No progress,
// no locks, no extended metadata — just enough for an autocomplete row.
type SuggestBook struct {
	ID       string
	Title    string
	Author   string
	HasCover bool
}

// SearchSuggest returns the top `limit` books matching `q` for the
// autocomplete surfaces. Reuses the same FTS infrastructure as Search.
// `limit` is assumed already clamped by the caller (service caps at 20).
func (r *BookRepo) SearchSuggest(ctx context.Context, q string, limit int) ([]SuggestBook, error) {
	const qPG = `
		SELECT b.id, b.title, b.author, b.has_cover
		FROM books b
		WHERE b.deleted_at IS NULL
		  AND b.tsv @@ websearch_to_tsquery('english', $1)
		ORDER BY ts_rank(b.tsv, websearch_to_tsquery('english', $1)) DESC,
		         b.title ASC
		LIMIT $2
	`
	rows, err := r.db.SQL.QueryContext(ctx, qPG, q, limit)
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
