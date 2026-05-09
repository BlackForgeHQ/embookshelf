// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"encoding/json"
	"testing"
)

func TestSelectQ(t *testing.T) {
	const pg = "SELECT $1"
	const sq = "SELECT ?"
	if got := SelectQ(DialectPostgres, pg, sq); got != pg {
		t.Fatalf("PG: got %q want %q", got, pg)
	}
	if got := SelectQ(DialectSQLite, pg, sq); got != sq {
		t.Fatalf("SQLite: got %q want %q", got, sq)
	}
}

func TestNewID(t *testing.T) {
	id := NewID()
	if len(id) != 36 {
		t.Fatalf("got %q (len=%d), want 36-char UUID", id, len(id))
	}
	if id == NewID() {
		t.Fatal("two NewID() calls returned the same value")
	}
}

func TestValueStringSlice(t *testing.T) {
	in := []string{"sci-fi", "drama"}

	// PG: returns the slice unchanged; pgx codec handles encoding.
	pgVal, err := ValueStringSlice(DialectPostgres, in)
	if err != nil {
		t.Fatalf("PG ValueStringSlice: %v", err)
	}
	pgSlice, ok := pgVal.([]string)
	if !ok {
		t.Fatalf("PG: got %T, want []string", pgVal)
	}
	if len(pgSlice) != 2 || pgSlice[0] != "sci-fi" {
		t.Fatalf("PG: got %v, want [sci-fi drama]", pgSlice)
	}

	// SQLite: returns a JSON-encoded string.
	sqliteVal, err := ValueStringSlice(DialectSQLite, in)
	if err != nil {
		t.Fatalf("SQLite ValueStringSlice: %v", err)
	}
	sqliteStr, ok := sqliteVal.(string)
	if !ok {
		t.Fatalf("SQLite: got %T, want string", sqliteVal)
	}
	var roundTrip []string
	if err := json.Unmarshal([]byte(sqliteStr), &roundTrip); err != nil {
		t.Fatalf("SQLite roundtrip unmarshal: %v", err)
	}
	if len(roundTrip) != 2 || roundTrip[0] != "sci-fi" {
		t.Fatalf("SQLite roundtrip: got %v, want [sci-fi drama]", roundTrip)
	}

	// Empty slice: SQLite produces "[]", not "null".
	emptyVal, err := ValueStringSlice(DialectSQLite, nil)
	if err != nil {
		t.Fatalf("SQLite ValueStringSlice(nil): %v", err)
	}
	if emptyVal.(string) != "[]" {
		t.Fatalf("SQLite empty: got %q want []", emptyVal)
	}
}

func TestScanStringSlice(t *testing.T) {
	// PG: src will be []string already (pgx codec). Just copy.
	var pgDst []string
	if err := ScanStringSlice(DialectPostgres, []string{"a", "b"}, &pgDst); err != nil {
		t.Fatalf("PG ScanStringSlice: %v", err)
	}
	if len(pgDst) != 2 || pgDst[0] != "a" {
		t.Fatalf("PG: got %v, want [a b]", pgDst)
	}

	// PG: pgx stdlib delivers TEXT[] as a literal string when the scan
	// destination is `any`. Confirm we parse the literal form.
	cases := []struct {
		in   string
		want []string
	}{
		{"{}", []string{}},
		{"{sci-fi,drama}", []string{"sci-fi", "drama"}},
		{`{"a,b",c}`, []string{"a,b", "c"}},
		{`{"with \"quote\"","back\\slash"}`, []string{`with "quote"`, `back\slash`}},
		{"{NULL,a}", []string{"", "a"}},
		{`{"NULL"}`, []string{"NULL"}},
	}
	for _, tc := range cases {
		var got []string
		if err := ScanStringSlice(DialectPostgres, tc.in, &got); err != nil {
			t.Fatalf("PG literal %q: %v", tc.in, err)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("PG literal %q: got %v, want %v", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("PG literal %q: got %v, want %v", tc.in, got, tc.want)
			}
		}
	}

	// SQLite: src is a string holding JSON.
	var sqliteDst []string
	if err := ScanStringSlice(DialectSQLite, `["x","y","z"]`, &sqliteDst); err != nil {
		t.Fatalf("SQLite ScanStringSlice: %v", err)
	}
	if len(sqliteDst) != 3 || sqliteDst[2] != "z" {
		t.Fatalf("SQLite: got %v, want [x y z]", sqliteDst)
	}

	// SQLite empty: "[]" decodes to empty slice.
	var emptyDst []string
	if err := ScanStringSlice(DialectSQLite, "[]", &emptyDst); err != nil {
		t.Fatalf("SQLite empty ScanStringSlice: %v", err)
	}
	if len(emptyDst) != 0 {
		t.Fatalf("SQLite empty: got %v, want []", emptyDst)
	}

	// SQLite nil src: empty slice, no error.
	var nilDst []string
	if err := ScanStringSlice(DialectSQLite, nil, &nilDst); err != nil {
		t.Fatalf("SQLite nil src: %v", err)
	}
	if len(nilDst) != 0 {
		t.Fatalf("SQLite nil src: got %v, want []", nilDst)
	}
}
