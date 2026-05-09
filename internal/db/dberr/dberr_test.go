// SPDX-License-Identifier: AGPL-3.0-or-later

package dberr

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsNotFound(t *testing.T) {
	if !IsNotFound(sql.ErrNoRows) {
		t.Fatal("sql.ErrNoRows should be NotFound")
	}
	if IsNotFound(errors.New("nope")) {
		t.Fatal("plain error should not be NotFound")
	}
	if IsNotFound(nil) {
		t.Fatal("nil should not be NotFound")
	}
}

func TestIsUniqueViolation_pg(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "libraries_slug_key"}
	ok, name := IsUniqueViolation(pgErr)
	if !ok {
		t.Fatal("23505 should be a unique violation")
	}
	if name != "libraries_slug_key" {
		t.Fatalf("constraint=%q want libraries_slug_key", name)
	}
}

func TestIsUniqueViolation_pg_otherCode(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "23503"} // foreign key violation
	if ok, _ := IsUniqueViolation(pgErr); ok {
		t.Fatal("23503 should not be a unique violation")
	}
}

func TestIsUniqueViolation_nil(t *testing.T) {
	if ok, _ := IsUniqueViolation(nil); ok {
		t.Fatal("nil should not be a unique violation")
	}
}

func TestIsUniqueViolation_plain(t *testing.T) {
	if ok, _ := IsUniqueViolation(errors.New("nope")); ok {
		t.Fatal("plain error should not be a unique violation")
	}
}

func TestIsUniqueViolation_sqlite_libraries_slug(t *testing.T) {
	wrapped := errors.New("constraint failed: UNIQUE constraint failed: libraries.slug (2067)")
	ok, name := IsUniqueViolation(wrapped)
	if !ok {
		t.Fatal("UNIQUE constraint failed message should be a unique violation")
	}
	if name != "libraries_slug_key" {
		t.Fatalf("constraint=%q want libraries_slug_key", name)
	}
}

func TestIsUniqueViolation_sqlite_libraries_path(t *testing.T) {
	wrapped := errors.New("UNIQUE constraint failed: libraries.path")
	ok, name := IsUniqueViolation(wrapped)
	if !ok || name != "libraries_path_key" {
		t.Fatalf("got (%v, %q), want (true, libraries_path_key)", ok, name)
	}
}

func TestIsUniqueViolation_sqlite_devices_composite(t *testing.T) {
	wrapped := errors.New("UNIQUE constraint failed: user_devices.user_id, user_devices.name")
	ok, name := IsUniqueViolation(wrapped)
	if !ok || name != "idx_user_devices_user_name" {
		t.Fatalf("got (%v, %q), want (true, idx_user_devices_user_name)", ok, name)
	}
}

func TestIsUniqueViolation_sqlite_unmapped(t *testing.T) {
	wrapped := errors.New("UNIQUE constraint failed: some_table.some_col")
	ok, name := IsUniqueViolation(wrapped)
	if !ok {
		t.Fatal("should still classify as unique violation")
	}
	// Unmapped columns return the dotted form so callers can log it.
	if name != "some_table.some_col" {
		t.Fatalf("constraint=%q want some_table.some_col", name)
	}
}
