package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blackforge/embookshelf/internal/model"
)

// ErrShelfSlugTaken is returned when a user tries to create a second shelf
// with the same slug they already have.
var ErrShelfSlugTaken = errors.New("shelf slug already exists for this user")

type ShelfRepo struct {
	pool *pgxpool.Pool
}

func NewShelfRepo(pool *pgxpool.Pool) *ShelfRepo {
	return &ShelfRepo{pool: pool}
}

const shelfCols = `s.id, s.user_id, s.name, s.slug, s.accent, s.created_at,
                  (SELECT count(*) FROM shelf_books sb WHERE sb.shelf_id = s.id) AS book_count`

func (r *ShelfRepo) ListForUser(ctx context.Context, userID string) ([]model.Shelf, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+shelfCols+`
		FROM shelves s
		WHERE s.user_id = $1
		ORDER BY s.created_at ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Shelf
	for rows.Next() {
		s, err := scanShelf(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *ShelfRepo) GetBySlugForUser(ctx context.Context, userID, slug string) (model.Shelf, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT `+shelfCols+`
		FROM shelves s
		WHERE s.user_id = $1 AND s.slug = $2
	`, userID, slug)
	return scanShelf(row)
}

func (r *ShelfRepo) BooksInShelfForUser(ctx context.Context, userID, shelfSlug string) ([]model.Book, error) {
	// $1 = userID for progress join; $2 = shelf slug; plus shelf-owner check.
	rows, err := r.pool.Query(ctx, `
		SELECT `+bookCols+`
		`+bookFrom+`
		JOIN shelf_books sb ON sb.book_id = b.id
		JOIN shelves     s  ON s.id = sb.shelf_id
		WHERE s.user_id = $1 AND s.slug = $2 AND b.deleted_at IS NULL
		ORDER BY sb.added_at DESC
	`, userID, shelfSlug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectBooks(rows)
}

// Create inserts a new shelf for the user. Generates a URL-safe slug from the
// name and appends -N on collision until unique.
func (r *ShelfRepo) Create(ctx context.Context, userID, name, accent string) (model.Shelf, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.Shelf{}, errors.New("name is required")
	}
	if accent == "" {
		accent = "accent"
	}
	baseSlug := slugify(name)
	if baseSlug == "" {
		baseSlug = "shelf"
	}

	for attempt := 0; attempt < 50; attempt++ {
		slug := baseSlug
		if attempt > 0 {
			slug = fmt.Sprintf("%s-%d", baseSlug, attempt+1)
		}
		row := r.pool.QueryRow(ctx, `
			INSERT INTO shelves (user_id, name, slug, accent)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (user_id, slug) DO NOTHING
			RETURNING `+shelfCols,
			userID, name, slug, accent)
		s, err := scanShelf(row)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // collision, try next -N
		}
		return s, err
	}
	return model.Shelf{}, ErrShelfSlugTaken
}

func (r *ShelfRepo) Delete(ctx context.Context, userID, slug string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM shelves WHERE user_id = $1 AND slug = $2`, userID, slug)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AddBook links a book to one of the user's shelves. ON CONFLICT → no-op.
func (r *ShelfRepo) AddBook(ctx context.Context, userID, slug, bookID string) error {
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO shelf_books (shelf_id, book_id)
		SELECT s.id, $3 FROM shelves s WHERE s.user_id = $1 AND s.slug = $2
		ON CONFLICT DO NOTHING
	`, userID, slug, bookID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Either the shelf doesn't belong to the user, or the row was a no-op.
		// Disambiguate by checking existence.
		if _, err := r.GetBySlugForUser(ctx, userID, slug); err != nil {
			return err
		}
	}
	return nil
}

func (r *ShelfRepo) RemoveBook(ctx context.Context, userID, slug, bookID string) error {
	_, err := r.pool.Exec(ctx, `
		DELETE FROM shelf_books
		WHERE book_id = $3
		  AND shelf_id IN (SELECT id FROM shelves WHERE user_id = $1 AND slug = $2)
	`, userID, slug, bookID)
	return err
}

// ShelfSlugsForBook returns the slugs of the user's shelves that contain a book.
func (r *ShelfRepo) ShelfSlugsForBook(ctx context.Context, userID, bookID string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT s.slug
		FROM shelf_books sb
		JOIN shelves s ON s.id = sb.shelf_id
		WHERE s.user_id = $1 AND sb.book_id = $2
	`, userID, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func scanShelf(s scanner) (model.Shelf, error) {
	var sh model.Shelf
	err := s.Scan(&sh.ID, &sh.UserID, &sh.Name, &sh.Slug, &sh.Accent, &sh.CreatedAt, &sh.BookCount)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sh, ErrNotFound
		}
		return sh, err
	}
	return sh, nil
}

// slugify turns a shelf name into a URL-safe slug. Keeps a-z0-9, collapses
// whitespace and punctuation to single hyphens, trims leading/trailing '-'.
func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	prevDash := true
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
