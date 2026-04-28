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
// the live count via CountForSmartShelf.
const shelfCols = `s.id, s.user_id, s.name, s.slug, s.accent, s.created_at,
                  s.is_smart, s.rule,
                  CASE
                    WHEN s.is_smart THEN 0
                    ELSE (SELECT count(*) FROM shelf_books sb WHERE sb.shelf_id = s.id)
                  END AS book_count`

// shelfColsReturning is the same projection for INSERT/UPDATE ... RETURNING
// clauses, which can't use a table alias — they reference columns on the
// target table directly. Kept in sync with shelfCols column order so
// scanShelf handles rows from either.
const shelfColsReturning = `id, user_id, name, slug, accent, created_at,
                           is_smart, rule,
                           CASE
                             WHEN is_smart THEN 0
                             ELSE (SELECT count(*) FROM shelf_books sb WHERE sb.shelf_id = shelves.id)
                           END AS book_count`

func (r *ShelfRepo) ListForUser(ctx context.Context, userID string) ([]model.Shelf, error) {
	rows, err := r.db.SQL.QueryContext(ctx, `
		SELECT `+shelfCols+`
		FROM shelves s
		WHERE s.user_id = $1
		ORDER BY s.is_smart ASC, s.created_at ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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
	row := r.db.SQL.QueryRowContext(ctx, `
		SELECT `+shelfCols+`
		FROM shelves s
		WHERE s.user_id = $1 AND s.slug = $2
	`, userID, slug)
	return scanShelf(row)
}

// BooksInShelfForUser returns the books on a user shelf. If the shelf is
// smart, the rule is compiled to SQL and joined against books directly;
// otherwise we hit the shelf_books join table as before.
func (r *ShelfRepo) BooksInShelfForUser(ctx context.Context, userID, shelfSlug string) ([]model.Book, error) {
	sh, err := r.GetBySlugForUser(ctx, userID, shelfSlug)
	if err != nil {
		return nil, err
	}

	if sh.IsSmart {
		return r.booksMatchingRule(ctx, userID, sh.Rule)
	}

	rows, err := r.db.SQL.QueryContext(ctx, `
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
		` + bookFrom + `
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

func (r *ShelfRepo) booksMatchingRule(ctx context.Context, userID string, rule *model.ShelfRule) ([]model.Book, error) {
	compiled, err := compileRule(rule, 2)
	if err != nil {
		return nil, err
	}
	args := append([]any{userID}, compiled.args...)
	query := `
		SELECT ` + bookCols + `
		` + bookFrom + `
		WHERE b.deleted_at IS NULL
	`
	if compiled.where != "" {
		query += " AND (" + compiled.where + ")"
	}
	query += " ORDER BY b.created_at DESC LIMIT 500"

	rows, err := r.db.SQL.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectBooks(rows)
}

// Create inserts a new shelf. For regular shelves, rule must be nil; for
// smart shelves it must be non-nil and already validated by the service.
// Generates a URL-safe slug from the name and appends -N on collision
// until unique.
func (r *ShelfRepo) Create(ctx context.Context, userID, name, accent string, rule *model.ShelfRule) (model.Shelf, error) {
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

	isSmart := rule != nil
	var ruleJSON []byte
	if isSmart {
		j, err := json.Marshal(rule)
		if err != nil {
			return model.Shelf{}, fmt.Errorf("marshal rule: %w", err)
		}
		ruleJSON = j
	}

	for attempt := 0; attempt < 50; attempt++ {
		slug := baseSlug
		if attempt > 0 {
			slug = fmt.Sprintf("%s-%d", baseSlug, attempt+1)
		}
		row := r.db.SQL.QueryRowContext(ctx, `
			INSERT INTO shelves (user_id, name, slug, accent, is_smart, rule)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (user_id, slug) DO NOTHING
			RETURNING `+shelfColsReturning,
			userID, name, slug, accent, isSmart, nullOrJSON(ruleJSON))
		s, err := scanShelf(row)
		if dberr.IsNotFound(err) {
			continue // collision, try next -N
		}
		return s, err
	}
	return model.Shelf{}, ErrShelfSlugTaken
}

// Update edits a shelf's name, accent, or rule. Nil pointers are
// untouched. Converting a regular shelf to smart or vice-versa is
// intentionally not supported — shelf_books membership and smart rules
// don't coexist cleanly, so the right action is delete + re-create.
func (r *ShelfRepo) Update(ctx context.Context, userID, slug string, name, accent *string, rule *model.ShelfRule, ruleChanged bool) (model.Shelf, error) {
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
	// Re-fetch via GetBySlugForUser so we consistently compute book_count
	// through the same CASE expression. Cheaper than wrestling with a
	// RETURNING list that can't reference the `s` alias shelfCols uses.
	if _, err := r.db.SQL.ExecContext(ctx, query, args...); err != nil {
		return model.Shelf{}, err
	}
	return r.GetBySlugForUser(ctx, userID, slug)
}

func (r *ShelfRepo) Delete(ctx context.Context, userID, slug string) error {
	res, err := r.db.SQL.ExecContext(ctx, `DELETE FROM shelves WHERE user_id = $1 AND slug = $2`, userID, slug)
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
	res, err := r.db.SQL.ExecContext(ctx, `
		INSERT INTO shelf_books (shelf_id, book_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, sh.ID, bookID)
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
	_, err = r.db.SQL.ExecContext(ctx, `
		DELETE FROM shelf_books
		WHERE shelf_id = $1 AND book_id = $2
	`, sh.ID, bookID)
	return err
}

// ShelfSlugsForBook returns the slugs of the user's regular shelves that
// contain a book. Smart shelves are deliberately excluded — a book's
// "is it in this smart shelf" relationship is query-time, not stored.
func (r *ShelfRepo) ShelfSlugsForBook(ctx context.Context, userID, bookID string) ([]string, error) {
	rows, err := r.db.SQL.QueryContext(ctx, `
		SELECT s.slug
		FROM shelf_books sb
		JOIN shelves s ON s.id = sb.shelf_id
		WHERE s.user_id = $1 AND sb.book_id = $2 AND s.is_smart = false
	`, userID, bookID)
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

func scanShelf(s scanner) (model.Shelf, error) {
	var (
		sh     model.Shelf
		ruleJS []byte
	)
	err := s.Scan(
		&sh.ID, &sh.UserID, &sh.Name, &sh.Slug, &sh.Accent, &sh.CreatedAt,
		&sh.IsSmart, &ruleJS, &sh.BookCount,
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
	rows, err := r.db.SQL.QueryContext(ctx, `
		SELECT s.slug, s.name, s.accent
		FROM shelves s
		WHERE s.user_id = $1
		  AND s.name ILIKE '%' || $2 || '%'
		ORDER BY s.name ASC
		LIMIT $3
	`, userID, q, limit)
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
