// SPDX-License-Identifier: AGPL-3.0-or-later

package migrator

import (
	"regexp"
	"sort"
	"strconv"
	"testing"
)

// migrationFileRE matches the canonical migrate filename:
//
//	000024_descriptive_name.up.sql
//	000024_descriptive_name.down.sql
var migrationFileRE = regexp.MustCompile(`^(\d+)_([^.]+)\.(up|down)\.sql$`)

// versionsBySuffix walks one branch of the embedded migrations tree
// (subdir = "migrations/postgres" or "migrations/sqlite") and returns
// a sorted slice of version numbers AND a map from version → filename
// stem (without extension). Filenames that don't match the canonical
// pattern are ignored.
func versionsBySuffix(t *testing.T, subdir string) ([]int, map[int]string) {
	t.Helper()
	entries, err := FS.ReadDir(subdir)
	if err != nil {
		t.Fatalf("ReadDir %q: %v", subdir, err)
	}

	stems := map[int]string{}
	upPresent := map[int]bool{}
	downPresent := map[int]bool{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationFileRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		v, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("bad version in %q: %v", e.Name(), err)
		}
		stems[v] = m[2]
		switch m[3] {
		case "up":
			upPresent[v] = true
		case "down":
			downPresent[v] = true
		}
	}

	var versions []int
	for v := range stems {
		if !upPresent[v] || !downPresent[v] {
			t.Errorf("%s: version %d is missing %s file",
				subdir, v, missingSide(upPresent[v], downPresent[v]))
			continue
		}
		versions = append(versions, v)
	}
	sort.Ints(versions)
	return versions, stems
}

func missingSide(up, down bool) string {
	switch {
	case !up:
		return "up"
	case !down:
		return "down"
	default:
		return ""
	}
}

// TestMigrationParity asserts that every migration with version >= 24
// is present in BOTH migrations/postgres/ and migrations/sqlite/ with
// the same filename stem (descriptive name).
//
// Versions < 24 only exist in postgres/; the sqlite/ tree is squashed
// at version 0 (0000_init). This test ignores those — the rule kicks
// in once parallel migrations begin.
func TestMigrationParity(t *testing.T) {
	const cutoff = 24
	// ADR-0023 froze SQLite: it survives as a read-only source for
	// `import-sqlite` and nothing creates a SQLite database any more. So
	// parity is a closed range, not an open one — migrations above the
	// freeze are Postgres-only by design, and demanding a SQLite twin for
	// them would be asking for a migration no database will ever run.
	// Raise this only if SQLite ever becomes a write target again.
	const sqliteFrozenAt = 38

	pgVersions, pgStems := versionsBySuffix(t, "migrations/postgres")
	sqVersions, sqStems := versionsBySuffix(t, "migrations/sqlite")

	pgFromCutoff := versionsInRange(versionsAtLeast(pgVersions, cutoff), sqliteFrozenAt)
	sqFromCutoff := versionsInRange(versionsAtLeast(sqVersions, cutoff), sqliteFrozenAt)

	if !sliceEqualInt(pgFromCutoff, sqFromCutoff) {
		t.Errorf("version mismatch >= %d: pg=%v sqlite=%v",
			cutoff, pgFromCutoff, sqFromCutoff)
	}

	for _, v := range pgFromCutoff {
		if sqStems[v] == "" {
			continue
		}
		if pgStems[v] != sqStems[v] {
			t.Errorf("version %d stem mismatch: pg=%q sqlite=%q",
				v, pgStems[v], sqStems[v])
		}
	}

	t.Logf("parity check: %d versions in [%d, %d] (pg=%d, sqlite=%d), all aligned",
		len(pgFromCutoff), cutoff, sqliteFrozenAt, len(pgVersions), len(sqVersions))
}

// versionsInRange drops versions above the SQLite freeze.
func versionsInRange(vs []int, max int) []int {
	out := make([]int, 0, len(vs))
	for _, v := range vs {
		if v <= max {
			out = append(out, v)
		}
	}
	return out
}

func versionsAtLeast(vs []int, min int) []int {
	out := make([]int, 0, len(vs))
	for _, v := range vs {
		if v >= min {
			out = append(out, v)
		}
	}
	return out
}

func sliceEqualInt(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
