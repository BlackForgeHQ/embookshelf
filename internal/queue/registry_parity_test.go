// SPDX-License-Identifier: AGPL-3.0-or-later

package queue

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// architectureDoc holds the register §4.4 describes in prose.
// Lower-case, as on disk. This read spelled it ARCHITECTURE.md and
// passed on macOS, whose filesystem is case-insensitive, then failed on
// the first Linux CI run that ever executed it (#216).
const architectureDoc = "../../docs/architecture.md"

// workerBullet matches one entry of the §4.4 worker list: a bullet whose
// first backticked token is the job kind.
var workerBullet = regexp.MustCompile("^- `([a-z_]+\\.[a-z_]+)`")

// TestArchitectureDescribesEveryJobKind guards the drift that put a
// scan-import worker in §4.4 for as long as ADR-0018 has been in force.
//
// The register is the one part of this document a reader trusts
// literally: it names a job kind and the file behind it, so a wrong
// entry does not read as vagueness, it reads as a fact. The previous
// drift in this file was a stale migration count, which announces
// itself the moment anyone checks. A worker that does not exist does
// not (#215).
//
// Both directions, for the same reason the error-code parity test
// checks both: a kind missing from the doc is an undocumented job, and
// a kind in the doc that the registry no longer declares is the exact
// failure this test was written for.
func TestArchitectureDescribesEveryJobKind(t *testing.T) {
	documented := documentedJobKinds(t)

	declared := map[string]bool{}
	for _, r := range registry(Deps{}) {
		declared[r.kind] = true
		if !documented[r.kind] {
			t.Errorf("job kind %q is registered but absent from the §4.4 register in %s "+
				"— an undocumented worker", r.kind, architectureDoc)
		}
	}
	for kind := range documented {
		if !declared[kind] {
			t.Errorf("job kind %q is described in %s but no longer registered "+
				"— the document points a reader at a worker that does not exist",
				kind, architectureDoc)
		}
	}
}

// documentedJobKinds parses the bullet list under §4.4's register.
//
// Anchored on the exact heading and the exact sentence that introduces
// the list, so renaming either cannot leave this test parsing nothing
// and quietly reporting success.
func documentedJobKinds(t *testing.T) map[string]bool {
	t.Helper()

	src, err := os.ReadFile(architectureDoc)
	if err != nil {
		t.Fatalf("cannot read %s: %v", architectureDoc, err)
	}

	kinds := map[string]bool{}
	inSection, inList := false, false
	for _, line := range strings.Split(string(src), "\n") {
		switch {
		case strings.HasPrefix(line, "### 4.4 "):
			inSection = true
		case inSection && strings.HasPrefix(line, "### "):
			inSection = false
		}
		if !inSection {
			continue
		}
		if strings.Contains(line, "`internal/task/` contains the per-kind workers:") {
			inList = true
			continue
		}
		if !inList {
			continue
		}
		if m := workerBullet.FindStringSubmatch(line); m != nil {
			kinds[m[1]] = true
			continue
		}
		// The list ends at the first blank line that is not inside a
		// bullet's continuation.
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "- ") {
			inList = false
		}
	}

	if len(kinds) == 0 {
		t.Fatalf("parsed no job kinds from §4.4 of %s — the anchors this test "+
			"keys on have moved, so it was about to report success on nothing",
			architectureDoc)
	}
	return kinds
}
