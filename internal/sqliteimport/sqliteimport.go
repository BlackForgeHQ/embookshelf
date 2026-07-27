// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sqliteimport moves an existing SQLite library into Postgres.
//
// It exists because embookshelf became a Postgres-only application
// (ADR-0023) and installs running the old SQLite default need a way out
// that does not risk their library. This is the only remaining reason
// the binary links a SQLite driver; the path is read-only and can be
// deleted once the deprecation window closes.
//
// Generic tools are not used precisely because two encodings differ in
// ways that corrupt silently: a SQLite JSON-text array must become a
// Postgres text[], and an RFC3339 TEXT timestamp must become a
// timestamptz. Rather than hand-write a mapping per table, the importer
// asks Postgres what each column's type is and converts values to suit.
package sqliteimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrTargetNotEmpty is returned when the Postgres database already holds
// application rows. Importing on top would interleave two libraries, so
// the importer refuses rather than merging.
var ErrTargetNotEmpty = errors.New("target database is not empty")

// tableOrder lists the tables to copy in foreign-key-safe order. It is
// deliberately explicit rather than derived: a reviewer can check the
// order, and the parent-before-child sequence is the whole reason the
// import can run in one pass.
//
// Explicit also means hand-maintained, which is a hazard on a one-shot
// data migration — a table added to the schema and forgotten here would
// be skipped with no error and exit code 0, i.e. silent data loss. Two
// things guard that: excludedTables below declares every table
// deliberately left out, and Run reports anything it recognises as
// neither. A test asserts the union covers the live schema.
var tableOrder = []string{
	"app_settings",
	"provider_settings",
	"users",
	"user_identities",
	"sessions",
	"user_devices",
	"user_invites",
	"password_reset_tokens",
	"storage_backends",
	"libraries",
	"books",
	"files",
	"shelves",
	"shelf_books",
	"bookdrop_items",
	"annotations",
	"user_book_progress",
	"reading_sessions",
	"pending_orphans",
}

// jobsTable is the SQLite polling queue. Postgres runs River, which owns
// its own tables, so queued work cannot be carried across. Counted and
// reported rather than silently dropped.
const jobsTable = "jobs"

// excludedTables names every table deliberately not imported, and why.
// The reason is the point: without it, a future reader cannot tell a
// considered omission from a forgotten one, which is exactly the
// ambiguity that makes a hand-maintained tableOrder risky.
var excludedTables = map[string]string{
	"book_reading_guides": "added after ADR-0023 froze SQLite, so no source database can contain it; reading guides are regenerated, not migrated",
	jobsTable:             "SQLite-only polling queue; River owns its own tables on Postgres, so queued work cannot transfer",
	"schema_migrations":   "the migrator's own bookkeeping, rebuilt by migrating the target",
	"library_paths":       "dropped by migration 000018; libraries.path replaced it",
}

// excludedPrefixes covers table families owned by a dependency rather
// than by this schema.
var excludedPrefixes = map[string]string{
	"river_": "River's own queue tables, created by its migrator on the target",
}

// isExcluded reports whether a table is deliberately not imported, and
// the reason if so.
func isExcluded(table string) (string, bool) {
	if why, ok := excludedTables[table]; ok {
		return why, true
	}
	for prefix, why := range excludedPrefixes {
		if strings.HasPrefix(table, prefix) {
			return why, true
		}
	}
	return "", false
}

// Report describes what an import moved.
type Report struct {
	// Rows maps table name to the number of rows copied. Tables absent
	// from the source, or empty in it, are omitted.
	Rows map[string]int
	// SkippedJobs is the number of queued SQLite jobs that could not be
	// transferred. Non-zero means the operator should re-trigger that
	// work (a library scan, a pending bookdrop ingest) after importing.
	SkippedJobs int
	// Orphans maps table name to rows dropped because they referenced a
	// parent that no longer exists. SQLite runs with foreign keys off by
	// default, so an old database can hold rows Postgres will not accept.
	// Non-empty means data was intentionally left behind.
	Orphans map[string]int
	// UnknownTables lists tables present in the source that this build
	// neither imports nor deliberately excludes — almost always a table
	// added to the schema without a tableOrder entry, or a source newer
	// than the binary reading it.
	//
	// Reported rather than silently skipped: the failure mode this
	// guards is a user's rows staying behind with an exit code of 0.
	UnknownTables []string
}

// TotalOrphans is the number of rows dropped as orphans across all tables.
func (r Report) TotalOrphans() int {
	n := 0
	for _, c := range r.Orphans {
		n += c
	}
	return n
}

// Total is the number of rows copied across every table.
func (r Report) Total() int {
	n := 0
	for _, c := range r.Rows {
		n += c
	}
	return n
}

// Run copies every application table from src (SQLite) into dst
// (Postgres). Both databases must already be migrated to the current
// schema. dst must be empty of application rows.
//
// The copy runs in one transaction on the target: a failure part-way
// leaves the Postgres database untouched rather than half-populated.
func Run(ctx context.Context, src, dst *sql.DB) (Report, error) {
	rep := Report{Rows: map[string]int{}, Orphans: map[string]int{}}

	if err := ensureEmpty(ctx, dst); err != nil {
		return rep, err
	}

	skipped, err := countRows(ctx, src, jobsTable)
	if err == nil {
		rep.SkippedJobs = skipped
	}

	unknown, err := unknownSourceTables(ctx, src)
	if err != nil {
		return rep, fmt.Errorf("inspect source tables: %w", err)
	}
	rep.UnknownTables = unknown

	tx, err := dst.BeginTx(ctx, nil)
	if err != nil {
		return rep, err
	}
	defer func() { _ = tx.Rollback() }()

	for _, table := range tableOrder {
		n, orphans, err := copyTable(ctx, src, tx, table)
		if err != nil {
			return rep, fmt.Errorf("copy %s: %w", table, err)
		}
		if n > 0 {
			rep.Rows[table] = n
		}
		if orphans > 0 {
			rep.Orphans[table] = orphans
		}
	}

	if err := tx.Commit(); err != nil {
		return rep, err
	}
	return rep, nil
}

// ensureEmpty refuses an import when any target table already holds
// rows, naming the first one found so the message is actionable.
func ensureEmpty(ctx context.Context, dst *sql.DB) error {
	for _, table := range tableOrder {
		n, err := countRows(ctx, dst, table)
		if err != nil {
			// A missing table means the target is not migrated; that is
			// a clearer error from the first copy attempt than here.
			continue
		}
		if n > 0 {
			return fmt.Errorf("%w: %s already has %d row(s)", ErrTargetNotEmpty, table, n)
		}
	}
	return nil
}

func countRows(ctx context.Context, d *sql.DB, table string) (int, error) {
	var n int
	// #nosec G202 -- table comes from tableOrder, not user input.
	err := d.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n)
	return n, err
}

// copyTable reads every row of one table from SQLite and inserts it into
// Postgres, converting each value according to the destination column's
// type. Columns present in the source but not the target are dropped
// with the row still imported — a SQLite-only column is not a reason to
// abandon a user's library.
func copyTable(ctx context.Context, src *sql.DB, tx *sql.Tx, table string) (copied, orphans int, err error) {
	dstTypes, err := columnTypes(ctx, tx, table)
	if err != nil {
		return 0, 0, fmt.Errorf("describe target: %w", err)
	}
	if len(dstTypes) == 0 {
		return 0, 0, fmt.Errorf("table missing from target — is Postgres migrated?")
	}

	// #nosec G202 -- table comes from tableOrder, not user input.
	rows, err := src.QueryContext(ctx, `SELECT * FROM `+table)
	if err != nil {
		// Table absent from the source (older schema): nothing to copy.
		return 0, 0, nil
	}
	defer func() { _ = rows.Close() }()

	srcCols, err := rows.Columns()
	if err != nil {
		return 0, 0, err
	}

	// Intersect source columns with the target's, preserving source order.
	type col struct {
		name    string
		pgType  string
		srcIdx  int
		include bool
	}
	cols := make([]col, 0, len(srcCols))
	for i, name := range srcCols {
		pgType, ok := dstTypes[name]
		cols = append(cols, col{name: name, pgType: pgType, srcIdx: i, include: ok})
	}

	insertCols := make([]string, 0, len(cols))
	for _, c := range cols {
		if c.include {
			insertCols = append(insertCols, c.name)
		}
	}
	if len(insertCols) == 0 {
		return 0, 0, nil
	}

	placeholders := make([]string, len(insertCols))
	for i := range insertCols {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	// #nosec G201 -- table and column names come from the schema, not input.
	stmtSQL := fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s)`,
		table, strings.Join(insertCols, ", "), strings.Join(placeholders, ", "))

	for rows.Next() {
		raw := make([]any, len(srcCols))
		scanTargets := make([]any, len(srcCols))
		for i := range raw {
			scanTargets[i] = &raw[i]
		}
		if err := rows.Scan(scanTargets...); err != nil {
			return copied, orphans, err
		}

		args := make([]any, 0, len(insertCols))
		for _, c := range cols {
			if !c.include {
				continue
			}
			v, cerr := convert(raw[c.srcIdx], c.pgType)
			if cerr != nil {
				return copied, orphans, fmt.Errorf("column %s: %w", c.name, cerr)
			}
			args = append(args, v)
		}

		// Each row gets a savepoint so a single unsatisfiable foreign key
		// can be skipped without aborting the surrounding transaction —
		// Postgres marks the whole transaction failed after any error.
		if _, err := tx.ExecContext(ctx, "SAVEPOINT row"); err != nil {
			return copied, orphans, err
		}
		if _, err := tx.ExecContext(ctx, stmtSQL, args...); err != nil {
			if !isForeignKeyViolation(err) {
				return copied, orphans, err
			}
			if _, rerr := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT row"); rerr != nil {
				return copied, orphans, rerr
			}
			orphans++
			continue
		}
		if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT row"); err != nil {
			return copied, orphans, err
		}
		copied++
	}
	if err := rows.Err(); err != nil {
		return copied, orphans, err
	}
	return copied, orphans, nil
}

// isForeignKeyViolation reports whether err is Postgres SQLSTATE 23503.
// Matched on the code text rather than a driver type so the check holds
// for both pgx and lib/pq-shaped errors.
func isForeignKeyViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23503"
	}
	return strings.Contains(err.Error(), "SQLSTATE 23503")
}

// columnTypes maps column name to Postgres data type for one table.
func columnTypes(ctx context.Context, tx *sql.Tx, table string) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT column_name, data_type
		FROM information_schema.columns
		WHERE table_name = $1 AND table_schema = current_schema()
	`, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]string{}
	for rows.Next() {
		var name, dataType string
		if err := rows.Scan(&name, &dataType); err != nil {
			return nil, err
		}
		out[name] = dataType
	}
	return out, rows.Err()
}

// convert turns a value read from SQLite into one Postgres accepts for a
// column of pgType. The three translations that matter:
//
//   - ARRAY  — SQLite holds a JSON array in TEXT; decode to []string so
//     the pgx codec writes a real text[].
//   - timestamp/date — SQLite holds RFC3339 TEXT; parse to time.Time.
//   - boolean — SQLite holds 0/1 as INTEGER.
//
// jsonb/json columns pass through as their TEXT form, which Postgres
// parses. Everything else (uuid, text, numeric) passes through: the
// driver's own conversion is already correct.
func convert(v any, pgType string) (any, error) {
	if v == nil {
		return nil, nil
	}

	switch {
	case pgType == "ARRAY":
		s, ok := asString(v)
		if !ok {
			return nil, fmt.Errorf("expected JSON text for array column, got %T", v)
		}
		if strings.TrimSpace(s) == "" {
			return []string{}, nil
		}
		var out []string
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			return nil, fmt.Errorf("decode array %q: %w", s, err)
		}
		if out == nil {
			out = []string{}
		}
		return out, nil

	case strings.HasPrefix(pgType, "timestamp"), pgType == "date":
		if t, ok := v.(time.Time); ok {
			return t.UTC(), nil
		}
		s, ok := asString(v)
		if !ok {
			return nil, fmt.Errorf("expected text timestamp, got %T", v)
		}
		t, err := parseSQLiteTime(s)
		if err != nil {
			return nil, err
		}
		return t, nil

	case pgType == "boolean":
		switch n := v.(type) {
		case bool:
			return n, nil
		case int64:
			return n != 0, nil
		case float64:
			return n != 0, nil
		}
		if s, ok := asString(v); ok {
			return s == "1" || strings.EqualFold(s, "true"), nil
		}
		return nil, fmt.Errorf("expected integer boolean, got %T", v)
	}

	// Byte slices for text-ish columns arrive as []byte from the SQLite
	// driver; hand Postgres a string so it does not infer bytea.
	if b, ok := v.([]byte); ok && pgType != "bytea" {
		return string(b), nil
	}
	return v, nil
}

func asString(v any) (string, bool) {
	switch s := v.(type) {
	case string:
		return s, true
	case []byte:
		return string(s), true
	}
	return "", false
}

// sqliteTimeFormats lists the TEXT timestamp formats the SQLite schema
// could have produced, so the importer reads everything a pre-ADR-0023
// install could have written. internal/db no longer parses these — the
// app is Postgres-only and this importer is their last consumer.
var sqliteTimeFormats = []string{
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
}

func parseSQLiteTime(s string) (time.Time, error) {
	for _, layout := range sqliteTimeFormats {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse %q as a timestamp", s)
}

// unknownSourceTables lists tables in the SQLite source that this build
// neither copies nor deliberately excludes.
//
// It reads the source rather than the target because the source is what
// holds the user's data: a table the binary has never heard of is data
// that will not survive the migration, and the operator should be told
// which one rather than discovering the gap later.
func unknownSourceTables(ctx context.Context, src *sql.DB) ([]string, error) {
	known := make(map[string]bool, len(tableOrder))
	for _, t := range tableOrder {
		known[t] = true
	}

	rows, err := src.QueryContext(ctx, `
		SELECT name FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
		ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var unknown []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if known[name] {
			continue
		}
		if _, excluded := isExcluded(name); excluded {
			continue
		}
		// FTS5 shadow tables belong to books_fts, which is itself an
		// index rather than user data.
		if strings.HasPrefix(name, "books_fts") {
			continue
		}
		unknown = append(unknown, name)
	}
	return unknown, rows.Err()
}
