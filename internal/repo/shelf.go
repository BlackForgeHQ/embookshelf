// SPDX-License-Identifier: AGPL-3.0-or-later

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
)

// ErrShelfSlugTaken is returned when a user tries to create a second shelf
// with the same slug they already have.
var ErrShelfSlugTaken = errors.New("shelf slug already exists for this user")

type ShelfRepo struct {
	db *db.DB
}

func NewShelfRepo(d *db.DB) *ShelfRepo {
	return &ShelfRepo{db: d}
}

// shelfCols keeps the book_count cheap for regular shelves by pulling it
// from shelf_books; smart shelves return 0 here and the service fills in
// the live count via CountForSmartShelf. The trailing empty owner_name
// is overridden in queries that JOIN users; left blank for own-only
// queries to avoid a needless join.
const shelfCols = `s.id, s.user_id, s.name, s.slug, s.accent, s.icon, s.created_at,
                  s.is_smart, s.rule, s.is_public,
                  CASE
                    WHEN s.is_smart THEN 0
                    ELSE (SELECT count(*) FROM shelf_books sb WHERE sb.shelf_id = s.id)
                  END AS book_count,
                  '' AS owner_name`

// shelfColsReturning is the same projection for INSERT/UPDATE ... RETURNING
// clauses, which can't use a table alias — they reference columns on the
// target table directly. Kept in sync with shelfCols column order so
// scanShelf handles rows from either.
const shelfColsReturning = `id, user_id, name, slug, accent, icon, created_at,
                           is_smart, rule, is_public,
                           CASE
                             WHEN is_smart THEN 0
                             ELSE (SELECT count(*) FROM shelf_books sb WHERE sb.shelf_id = shelves.id)
                           END AS book_count,
                           '' AS owner_name`

func (r *ShelfRepo) ListForUser(ctx context.Context, userID string) ([]model.Shelf, error) {
	const q = `
		SELECT ` + shelfCols + `
		FROM shelves s
		WHERE s.user_id = $1
		ORDER BY s.is_smart ASC, s.created_at ASC
	`
	rows, err := r.db.SQL.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.Shelf
	for rows.Next() {
		s, err := r.scanShelf(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *ShelfRepo) GetBySlugForUser(ctx context.Context, userID, slug string) (model.Shelf, error) {
	const q = `
		SELECT ` + shelfCols + `
		FROM shelves s
		WHERE s.user_id = $1 AND s.slug = $2
	`
	row := r.db.SQL.QueryRowContext(ctx, q, userID, slug)
	return r.scanShelf(row)
}

// BooksInShelfForUser returns the books on a user shelf. If the shelf is
// smart, the rule is compiled to SQL and joined against books directly;
// otherwise we hit the shelf_books join table as before.
//
// sort accepts the same vocabulary as library search (title|author|recent|
// year|rating). Empty/unknown falls back to shelf-membership recency
// (sb.added_at DESC) for regular shelves and book recency for smart shelves.
func (r *ShelfRepo) BooksInShelfForUser(ctx context.Context, userID, shelfSlug, sort string) ([]model.Book, error) {
	sh, err := r.GetBySlugForUser(ctx, userID, shelfSlug)
	if err != nil {
		return nil, err
	}

	if sh.IsSmart {
		return r.booksMatchingRule(ctx, userID, sh.Rule, sort)
	}

	orderBy := shelfBooksOrderBy(sort)
	q := `
		SELECT ` + bookCols + `
		` + bookFromPG + `
		JOIN shelf_books sb ON sb.book_id = b.id
		JOIN shelves     s  ON s.id = sb.shelf_id
		-- $1 is cast explicitly because bookFromPG already pins it to
		-- text via NULLIF($1, '')::uuid. Postgres infers one type per
		-- parameter across the whole statement, so comparing the same
		-- $1 to a uuid column bare fails with "operator does not exist:
		-- uuid = text" — which is what made every regular shelf 500.
		WHERE s.user_id = $1::uuid AND s.slug = $2 AND b.deleted_at IS NULL
		ORDER BY ` + orderBy
	rows, err := r.db.SQL.QueryContext(ctx, q, userID, shelfSlug)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectBooks(rows)
}

// CountForSmartShelf runs the rule as a COUNT(*) so the sidebar can show
// the live membership size without materializing every matching book.
func (r *ShelfRepo) CountForSmartShelf(ctx context.Context, userID string, rule *model.ShelfRule) (int, error) {
	compiled, err := compileRule(rule, 2) // $1 is userID for the progress join
	if err != nil {
		return 0, err
	}
	args := append([]any{userID}, compiled.args...)
	query := `
		SELECT COUNT(*)
		` + bookFromPG + `
		WHERE b.deleted_at IS NULL
	`
	if compiled.where != "" {
		query += " AND (" + compiled.where + ")"
	}
	var n int
	if err := r.db.SQL.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (r *ShelfRepo) booksMatchingRule(ctx context.Context, userID string, rule *model.ShelfRule, sort string) ([]model.Book, error) {
	compiled, err := compileRule(rule, 2)
	if err != nil {
		return nil, err
	}
	args := append([]any{userID}, compiled.args...)
	query := `
		SELECT ` + bookCols + `
		` + bookFromPG + `
		WHERE b.deleted_at IS NULL
	`
	if compiled.where != "" {
		query += " AND (" + compiled.where + ")"
	}
	query += " ORDER BY " + smartShelfOrderBy(sort) + " LIMIT 500"

	rows, err := r.db.SQL.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectBooks(rows)
}

// shelfBooksOrderBy maps the API sort vocabulary to ORDER BY for regular
// shelves (which can sort by sb.added_at via the join). Default keeps
// shelf-membership recency so existing callers and "added" UI sort behave
// as before.
func shelfBooksOrderBy(sort string) string {
	switch sort {
	case "title":
		return "b.title ASC"
	case "author":
		return "b.author ASC, b.title ASC"
	case "year":
		return "b.year DESC, b.title ASC"
	case "rating":
		return "b.rating DESC, b.title ASC"
	case "recent":
		return "sb.added_at DESC"
	default:
		return "sb.added_at DESC"
	}
}

// smartShelfOrderBy is the same mapping for smart shelves, which have no
// shelf_books join — "recent" falls back to b.created_at.
func smartShelfOrderBy(sort string) string {
	switch sort {
	case "title":
		return "b.title ASC"
	case "author":
		return "b.author ASC, b.title ASC"
	case "year":
		return "b.year DESC, b.title ASC"
	case "rating":
		return "b.rating DESC, b.title ASC"
	case "recent":
		return "b.created_at DESC"
	default:
		return "b.created_at DESC"
	}
}

// Create inserts a new shelf. For regular shelves, rule must be nil; for
// smart shelves it must be non-nil and already validated by the service.
// Generates a URL-safe slug from the name and appends -N on collision
// until unique.
func (r *ShelfRepo) Create(ctx context.Context, userID, name, accent, icon string, rule *model.ShelfRule) (model.Shelf, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.Shelf{}, errors.New("name is required")
	}
	if accent == "" {
		accent = "accent"
	}
	if icon == "" {
		icon = "library"
	}
	baseSlug := slugify(name)
	if baseSlug == "" {
		baseSlug = "shelf"
	}

	isSmart := rule != nil
	var ruleJSON []byte
	if isSmart {
		j, err := json.Marshal(rule)
		if err != nil {
			return model.Shelf{}, fmt.Errorf("marshal rule: %w", err)
		}
		ruleJSON = j
	}

	id := db.NewID()
	const q = `
		INSERT INTO shelves (id, user_id, name, slug, accent, icon, is_smart, rule)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (user_id, slug) DO NOTHING
		RETURNING ` + shelfColsReturning

	for attempt := 0; attempt < 50; attempt++ {
		slug := baseSlug
		if attempt > 0 {
			slug = fmt.Sprintf("%s-%d", baseSlug, attempt+1)
		}
		row := r.db.SQL.QueryRowContext(ctx, q,
			id, userID, name, slug, accent, icon, isSmart, nullOrJSON(ruleJSON))
		s, err := r.scanShelf(row)
		// scanShelf maps sql.ErrNoRows → repo.ErrNotFound. Empty
		// RETURNING after `ON CONFLICT DO NOTHING` lands here, so
		// match the wrapped sentinel — `dberr.IsNotFound` only sees
		// raw sql.ErrNoRows and would loop-end early.
		if errors.Is(err, ErrNotFound) {
			// Collision on (user_id, slug) — generate a new ID for the next attempt
			id = db.NewID()
			continue
		}
		return s, err
	}
	return model.Shelf{}, ErrShelfSlugTaken
}

// Update edits a shelf's name, accent, or rule. Nil pointers are
// untouched. Converting a regular shelf to smart or vice-versa is
// intentionally not supported — shelf_books membership and smart rules
// don't coexist cleanly, so the right action is delete + re-create.
func (r *ShelfRepo) Update(ctx context.Context, userID, slug string, name, accent, icon *string, rule *model.ShelfRule, ruleChanged bool) (model.Shelf, error) {
	// Pre-check so we can reject name→empty and smart→regular edits
	// before building a dynamic UPDATE.
	cur, err := r.GetBySlugForUser(ctx, userID, slug)
	if err != nil {
		return model.Shelf{}, err
	}

	var (
		sets []string
		args = []any{userID, slug}
	)
	if name != nil {
		trimmed := strings.TrimSpace(*name)
		if trimmed == "" {
			return model.Shelf{}, errors.New("name cannot be empty")
		}
		args = append(args, trimmed)
		sets = append(sets, fmt.Sprintf("name = $%d", len(args)))
	}
	if accent != nil {
		args = append(args, strings.TrimSpace(*accent))
		sets = append(sets, fmt.Sprintf("accent = $%d", len(args)))
	}
	if icon != nil {
		trimmed := strings.TrimSpace(*icon)
		if trimmed == "" {
			return model.Shelf{}, errors.New("icon cannot be empty")
		}
		args = append(args, trimmed)
		sets = append(sets, fmt.Sprintf("icon = $%d", len(args)))
	}
	if ruleChanged {
		if !cur.IsSmart {
			return model.Shelf{}, errors.New("cannot assign a rule to a regular shelf")
		}
		if rule == nil {
			return model.Shelf{}, errors.New("smart shelf requires a rule")
		}
		j, err := json.Marshal(rule)
		if err != nil {
			return model.Shelf{}, fmt.Errorf("marshal rule: %w", err)
		}
		args = append(args, string(j))
		sets = append(sets, fmt.Sprintf("rule = $%d::jsonb", len(args)))
	}
	if len(sets) == 0 {
		return cur, nil
	}
	query := `
		UPDATE shelves
		SET ` + strings.Join(sets, ", ") + `
		WHERE user_id = $1 AND slug = $2
	`
	if _, err := r.db.SQL.ExecContext(ctx, query, args...); err != nil {
		return model.Shelf{}, err
	}
	// Re-fetch via GetBySlugForUser so we consistently compute book_count
	// through the same CASE expression. Cheaper than wrestling with a
	// RETURNING list that can't reference the `s` alias shelfCols uses.
	return r.GetBySlugForUser(ctx, userID, slug)
}

func (r *ShelfRepo) Delete(ctx context.Context, userID, slug string) error {
	const q = `DELETE FROM shelves WHERE user_id = $1 AND slug = $2`
	res, err := r.db.SQL.ExecContext(ctx, q, userID, slug)
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

// AddBook links a book to one of the user's regular shelves. Returns
// ErrSmartShelfImmutable when the target is a smart shelf — membership
// there is derived from the rule.
var ErrSmartShelfImmutable = errors.New("smart shelves do not accept manual book additions")

func (r *ShelfRepo) AddBook(ctx context.Context, userID, slug, bookID string) error {
	sh, err := r.GetBySlugForUser(ctx, userID, slug)
	if err != nil {
		return err
	}
	if sh.IsSmart {
		return ErrSmartShelfImmutable
	}
	const q = `
		INSERT INTO shelf_books (shelf_id, book_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`
	res, err := r.db.SQL.ExecContext(ctx, q, sh.ID, bookID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		// Row already existed — idempotent no-op.
		return nil
	}
	return nil
}

func (r *ShelfRepo) RemoveBook(ctx context.Context, userID, slug, bookID string) error {
	sh, err := r.GetBySlugForUser(ctx, userID, slug)
	if err != nil {
		return err
	}
	if sh.IsSmart {
		return ErrSmartShelfImmutable
	}
	const q = `
		DELETE FROM shelf_books
		WHERE shelf_id = $1 AND book_id = $2
	`
	_, err = r.db.SQL.ExecContext(ctx, q, sh.ID, bookID)
	return err
}

// ShelfSlugsForBook returns the slugs of the user's regular shelves that
// contain a book. Smart shelves are deliberately excluded — a book's
// "is it in this smart shelf" relationship is query-time, not stored.
func (r *ShelfRepo) ShelfSlugsForBook(ctx context.Context, userID, bookID string) ([]string, error) {
	const q = `
		SELECT s.slug
		FROM shelf_books sb
		JOIN shelves s ON s.id = sb.shelf_id
		WHERE s.user_id = $1 AND sb.book_id = $2 AND s.is_smart = false
	`
	rows, err := r.db.SQL.QueryContext(ctx, q, userID, bookID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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

func (r *ShelfRepo) scanShelf(s scanner) (model.Shelf, error) {
	var (
		sh     model.Shelf
		ruleJS []byte
	)
	err := s.Scan(
		&sh.ID, &sh.UserID, &sh.Name, &sh.Slug, &sh.Accent, &sh.Icon, &sh.CreatedAt,
		&sh.IsSmart, &ruleJS, &sh.IsPublic, &sh.BookCount, &sh.OwnerName,
	)
	if err != nil {
		if dberr.IsNotFound(err) {
			return sh, ErrNotFound
		}
		return sh, err
	}
	if len(ruleJS) > 0 {
		var r model.ShelfRule
		if err := json.Unmarshal(ruleJS, &r); err != nil {
			return sh, fmt.Errorf("decode rule: %w", err)
		}
		sh.Rule = &r
	}
	return sh, nil
}

// nullOrJSON returns nil for missing rules (so the column stays NULL) and
// the raw JSON bytes otherwise.
func nullOrJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
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

// -----------------------------------------------------------------------------
// Rule compiler — turns a validated model.ShelfRule into a SQL WHERE
// fragment + args. Called by both the members query and the count query.

type compiledRule struct {
	where string
	args  []any
}

func compileRule(r *model.ShelfRule, startArg int) (compiledRule, error) {
	if r == nil || len(r.Predicates) == 0 {
		// Empty rule → match nothing. `FALSE` is cleaner than dropping the
		// clause (caller wants the rule to behave like a restrictive filter).
		return compiledRule{where: "FALSE"}, nil
	}

	parts := make([]string, 0, len(r.Predicates))
	args := make([]any, 0, len(r.Predicates))
	arg := startArg

	for _, p := range r.Predicates {
		frag, ps, err := compilePredicate(p, arg)
		if err != nil {
			return compiledRule{}, err
		}
		parts = append(parts, frag)
		args = append(args, ps...)
		arg += len(ps)
	}

	combiner := " AND "
	if r.Match == model.RuleMatchAny {
		combiner = " OR "
	}
	return compiledRule{where: strings.Join(parts, combiner), args: args}, nil
}

// compilePredicate maps one predicate to a SQL fragment plus its bound
// args. Column names are hardcoded — user input never reaches an
// identifier slot, so SQL injection is structurally impossible.
func compilePredicate(p model.ShelfPredicate, arg int) (string, []any, error) {
	switch p.Field {
	case model.RuleFieldTitle:
		return compileStringPredicate("b.title", p, arg)
	case model.RuleFieldAuthor:
		return compileStringPredicate("b.author", p, arg)
	case model.RuleFieldFormat:
		return compileStringPredicate("b.format", p, arg)
	case model.RuleFieldSeries:
		return compileStringPredicate("COALESCE(b.series,'')", p, arg)
	case model.RuleFieldYear:
		return compileIntPredicate("b.year", p, arg)
	case model.RuleFieldRating:
		return compileIntPredicate("b.rating", p, arg)
	case model.RuleFieldTags:
		if p.Op != model.OpContains {
			return "", nil, fmt.Errorf("%w: tags supports only contains", model.ErrInvalidRule)
		}
		v, ok := p.Value.(string)
		if !ok {
			return "", nil, fmt.Errorf("%w: tags contains expects a string", model.ErrInvalidRule)
		}
		return fmt.Sprintf("$%d = ANY(b.tags)", arg), []any{v}, nil
	case model.RuleFieldProgress:
		pct, err := p.FloatValue()
		if err != nil {
			return "", nil, err
		}
		// Convert the 0..1 wire value to the 0..100 int the column stores.
		intPct := int(pct*100 + 0.5)
		op, err := sqlOp(p.Op)
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("COALESCE(ubp.progress, 0) %s $%d", op, arg), []any{intPct}, nil
	}
	return "", nil, fmt.Errorf("%w: unknown field %q", model.ErrInvalidRule, p.Field)
}

func compileStringPredicate(column string, p model.ShelfPredicate, arg int) (string, []any, error) {
	v, ok := p.Value.(string)
	if !ok {
		return "", nil, fmt.Errorf("%w: %q expects a string", model.ErrInvalidRule, p.Field)
	}
	switch p.Op {
	case model.OpEq:
		return fmt.Sprintf("%s = $%d", column, arg), []any{v}, nil
	case model.OpNe:
		return fmt.Sprintf("%s <> $%d", column, arg), []any{v}, nil
	case model.OpContains:
		return fmt.Sprintf("%s ILIKE $%d", column, arg), []any{"%" + v + "%"}, nil
	case model.OpStartsWith:
		return fmt.Sprintf("%s ILIKE $%d", column, arg), []any{v + "%"}, nil
	}
	return "", nil, fmt.Errorf("%w: op %q not valid for %q", model.ErrInvalidRule, p.Op, p.Field)
}

func compileIntPredicate(column string, p model.ShelfPredicate, arg int) (string, []any, error) {
	n, err := p.IntValue()
	if err != nil {
		return "", nil, err
	}
	op, err := sqlOp(p.Op)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("%s %s $%d", column, op, arg), []any{n}, nil
}

func sqlOp(op model.RuleOp) (string, error) {
	switch op {
	case model.OpEq:
		return "=", nil
	case model.OpNe:
		return "<>", nil
	case model.OpLt:
		return "<", nil
	case model.OpLte:
		return "<=", nil
	case model.OpGt:
		return ">", nil
	case model.OpGte:
		return ">=", nil
	}
	return "", fmt.Errorf("%w: op %q not a comparison operator", model.ErrInvalidRule, op)
}

// CountUnshelvedForUser counts the user's books not on any of their
// regular non-system shelves. Smart shelves are ignored (membership is
// query-time, not stored), and `reading`/`finished` are excluded — they
// auto-populate from progress, not curation. Hits idx_shelf_books_book
// via the NOT EXISTS subquery.
func (r *ShelfRepo) CountUnshelvedForUser(ctx context.Context, userID string) (int, error) {
	const q = `
		SELECT COUNT(*)
		FROM books b
		JOIN libraries l ON l.id = b.library_id
		WHERE b.deleted_at IS NULL
		  AND NOT EXISTS (
		    SELECT 1 FROM shelf_books sb
		    JOIN shelves s ON s.id = sb.shelf_id
		    WHERE sb.book_id = b.id
		      AND s.user_id = $1
		      AND s.is_smart = false
		      AND s.slug NOT IN ('reading','finished')
		  )
	`
	var n int
	if err := r.db.SQL.QueryRowContext(ctx, q, userID).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// shelfColsVisible mirrors shelfCols but populates owner_name for public
// shelves the viewer doesn't own (LEFT JOIN users so private rows still
// match without paying the join cost). $1 is bound to userID.
const shelfColsVisible = `s.id, s.user_id, s.name, s.slug, s.accent, s.icon, s.created_at,
                  s.is_smart, s.rule, s.is_public,
                  CASE
                    WHEN s.is_smart THEN 0
                    ELSE (SELECT count(*) FROM shelf_books sb WHERE sb.shelf_id = s.id)
                  END AS book_count,
                  CASE
                    WHEN s.user_id = $1 THEN ''
                    ELSE COALESCE(NULLIF(u.name, ''), u.email, '')
                  END AS owner_name`

// ListVisibleToUser returns the user's own shelves plus every public
// shelf in the system. Own shelves come first; public ones land after,
// ordered by creation date — same shape sidebar UI expects.
func (r *ShelfRepo) ListVisibleToUser(ctx context.Context, userID string) ([]model.Shelf, error) {
	const q = `
		SELECT ` + shelfColsVisible + `
		FROM shelves s
		LEFT JOIN users u ON u.id = s.user_id
		WHERE s.user_id = $1 OR s.is_public = true
		ORDER BY (s.user_id = $1) DESC, s.is_smart ASC, s.created_at ASC
	`
	rows, err := r.db.SQL.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.Shelf
	for rows.Next() {
		s, err := r.scanShelf(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetPublicBySlug looks up a shelf by its public-namespace slug. Used by
// the read-only viewer paths that resolve `public:<slug>`. Returns
// ErrNotFound when the slug doesn't exist or the shelf is not public.
func (r *ShelfRepo) GetPublicBySlug(ctx context.Context, slug string) (model.Shelf, error) {
	const q = `
		SELECT ` + shelfCols + `
		FROM shelves s
		WHERE s.slug = $1 AND s.is_public = true
	`
	row := r.db.SQL.QueryRowContext(ctx, q, slug)
	return r.scanShelf(row)
}

// BooksInPublicShelf returns the books on a public shelf. The viewer's
// userID is passed in only because the books-projection joins per-user
// progress on $1; the WHERE clause itself filters by is_public + slug,
// not by ownership. Smart shelves can never be public so membership
// comes straight from shelf_books.
func (r *ShelfRepo) BooksInPublicShelf(ctx context.Context, viewerUserID, slug, sort string) ([]model.Book, error) {
	orderBy := shelfBooksOrderBy(sort)
	q := `
		SELECT ` + bookCols + `
		` + bookFromPG + `
		JOIN shelf_books sb ON sb.book_id = b.id
		JOIN shelves     s  ON s.id = sb.shelf_id
		WHERE s.is_public = true AND s.slug = $2 AND b.deleted_at IS NULL
		ORDER BY ` + orderBy
	rows, err := r.db.SQL.QueryContext(ctx, q, viewerUserID, slug)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectBooks(rows)
}

// SetPublic flips a shelf's is_public flag, scoped to the owner. Returns
// ErrNotFound when the slug doesn't belong to userID. The caller is
// responsible for the role check (admin-only); the repo enforces
// ownership only. Smart shelves are rejected by a CHECK constraint at
// the SQL layer, and explicitly here so the caller gets a clear error.
func (r *ShelfRepo) SetPublic(ctx context.Context, userID, slug string, public bool) (model.Shelf, error) {
	cur, err := r.GetBySlugForUser(ctx, userID, slug)
	if err != nil {
		return model.Shelf{}, err
	}
	if public && cur.IsSmart {
		return model.Shelf{}, errors.New("smart shelves cannot be public")
	}
	if cur.IsPublic == public {
		return cur, nil // idempotent
	}
	const q = `
		UPDATE shelves SET is_public = $3
		WHERE user_id = $1 AND slug = $2
	`
	if _, err := r.db.SQL.ExecContext(ctx, q, userID, slug, public); err != nil {
		return model.Shelf{}, err
	}
	return r.GetBySlugForUser(ctx, userID, slug)
}

// UnpublishAllForOwner flips every is_public shelf belonging to userID
// back to private. Returns the slugs that were affected so callers can
// emit one removed-broadcast per shelf. Used when an admin is demoted
// to a regular user — they lose authority to keep shelves shared.
func (r *ShelfRepo) UnpublishAllForOwner(ctx context.Context, userID string) ([]string, error) {
	const q = `
		UPDATE shelves SET is_public = false
		WHERE user_id = $1 AND is_public = true
		RETURNING slug
	`
	rows, err := r.db.SQL.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var slugs []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		slugs = append(slugs, s)
	}
	return slugs, rows.Err()
}

// SuggestShelf is the slim shape returned by SearchSuggest for the
// autocomplete surfaces.
type SuggestShelf struct {
	Slug   string
	Name   string
	Accent string
}

// SearchSuggest returns the user's shelves whose name matches `q`. Used by
// the global command palette; per-user scoping is enforced via the
// user_id WHERE clause.
func (r *ShelfRepo) SearchSuggest(ctx context.Context, userID, q string, limit int) ([]SuggestShelf, error) {
	const query = `
		SELECT s.slug, s.name, s.accent
		FROM shelves s
		WHERE s.user_id = $1
		  AND s.name ILIKE '%' || $2 || '%'
		ORDER BY s.name ASC
		LIMIT $3
	`
	rows, err := r.db.SQL.QueryContext(ctx, query, userID, q, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SuggestShelf
	for rows.Next() {
		var s SuggestShelf
		if err := rows.Scan(&s.Slug, &s.Name, &s.Accent); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ensure *sql.Rows satisfies scanner at compile time (used by collectBooks).
var _ scanner = (*sql.Rows)(nil)
