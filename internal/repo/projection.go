// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"strconv"
	"strings"
)

// aliasToken marks where a table alias belongs inside a computed
// column's expr. SELECTs render it as the query's alias ("s", "l", "b");
// RETURNING clauses, which have no alias in scope, render it as the
// table name.
const aliasToken = "{alias}"

// column is one entry in a table's projection. It carries a column's
// name, its SQL rendering and its scan destination in a single value, so
// the SELECT order and the Scan order cannot be stated separately and
// therefore cannot drift. This is the Column-order coupling hazard from
// CONTEXT.md: a count mismatch failed loudly, but swapping two same-type
// adjacent columns in one list compiled, ran, and crossed those fields on
// every row.
type column[T any] struct {
	// name is the table column. Computed entries (book_count,
	// owner_name, progress) name their output alias instead and carry
	// an expr.
	name string
	// expr overrides the rendered SQL for this entry. Empty means the
	// plain qualified/bare column reference. aliasToken is substituted
	// with the table alias.
	expr string
	// dest is where this column's value lands on a *T. Adapters
	// (db.TextArray, nullText, the JSON columns) go here rather than
	// after the Scan, so the destination is always the model field.
	dest func(*T) any
	// arg is the value bound when this column takes part in the
	// full-row UPDATE. nil means the column is not user-updatable, so
	// the SET list and its argument slice come out of one walk.
	arg func(*T) any
}

// projection is a table's ordered column list — the single declaration
// every SQL context for that table is derived from.
type projection[T any] []column[T]

// selectList renders the projection for a SELECT, qualifying plain
// columns with alias.
func (p projection[T]) selectList(alias string) string {
	return p.render(alias, true)
}

// returningList renders the projection for a RETURNING clause. No alias
// is in scope there, so plain columns are bare and computed entries are
// qualified against the table itself.
func (p projection[T]) returningList(table string) string {
	return p.render(table, false)
}

func (p projection[T]) render(alias string, qualify bool) string {
	parts := make([]string, len(p))
	for i, c := range p {
		switch {
		case c.expr != "":
			parts[i] = strings.ReplaceAll(c.expr, aliasToken, alias)
		case qualify:
			parts[i] = alias + "." + c.name
		default:
			parts[i] = c.name
		}
	}
	return strings.Join(parts, ", ")
}

// with returns a copy of the projection with one entry's expr replaced.
// Used where a query genuinely computes a column differently — the
// visible-shelves query filling in owner_name, the create-book CTE that
// has no progress row to join. The column stays in the same position, so
// the same scanner reads the result.
//
// Panics on an unknown name: the call sites are package-level, so a typo
// fails at init rather than on a request.
func (p projection[T]) with(name, expr string) projection[T] {
	out := make(projection[T], len(p))
	copy(out, p)
	for i := range out {
		if out[i].name == name {
			out[i].expr = expr
			return out
		}
	}
	panic("repo: projection has no column named " + name)
}

// scan reads one row into dst, one destination per declared column.
func (p projection[T]) scan(s scanner, dst *T) error {
	dests := make([]any, len(p))
	for i, c := range p {
		dests[i] = c.dest(dst)
	}
	return s.Scan(dests...)
}

// updateSet renders "col = $first, col = $first+1, …" over the columns
// that declared an arg, and returns the accessors in the same order. The
// caller binds them by walking the returned slice, so the placeholder
// numbering and the argument order come from one traversal instead of
// two hand-kept lists.
func (p projection[T]) updateSet(first int) (string, []func(*T) any) {
	var (
		sets []string
		args []func(*T) any
	)
	for _, c := range p {
		if c.arg == nil {
			continue
		}
		sets = append(sets, c.name+" = $"+strconv.Itoa(first+len(args)))
		args = append(args, c.arg)
	}
	return strings.Join(sets, ", "), args
}

// bind materialises the arguments for an updateSet walk.
func bind[T any](accessors []func(*T) any, v *T) []any {
	out := make([]any, len(accessors))
	for i, f := range accessors {
		out[i] = f(v)
	}
	return out
}
