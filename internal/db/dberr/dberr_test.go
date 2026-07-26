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
