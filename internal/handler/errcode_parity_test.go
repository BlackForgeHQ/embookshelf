// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"os"
	"strings"
	"testing"
)

// clientErrorCodeFile holds the union the UI switches on.
const clientErrorCodeFile = "../../ui/src/api/client.ts"

// TestErrorCodesMatchClientUnion guards the same drift the SSE catalog
// parity test guards, for the other hand-kept pair of lists.
//
// A code the server sends but the client's union omits is a case the UI
// cannot branch on — it falls through to the generic message and the
// specific handling silently never runs. That is exactly how kindle.sent
// shipped unlistened for months, which is why the SSE side got a test;
// error codes had no equivalent until now.
func TestErrorCodesMatchClientUnion(t *testing.T) {
	client := clientErrorCodes(t)

	for _, code := range AllErrorCodes {
		if !client[code] {
			t.Errorf("code %q is sent by the server but absent from the ApiErrorCode "+
				"union in %s — the UI cannot branch on it", code, clientErrorCodeFile)
		}
	}
	for code := range client {
		found := false
		for _, declared := range AllErrorCodes {
			if declared == code {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("code %q is in the client union but no longer declared in "+
				"AllErrorCodes — dead branch in the UI", code)
		}
	}
}

// clientErrorCodes parses the ApiErrorCode union members.
func clientErrorCodes(t *testing.T) map[string]bool {
	t.Helper()

	src, err := os.ReadFile(clientErrorCodeFile)
	if err != nil {
		t.Skipf("cannot read %s: %v", clientErrorCodeFile, err)
	}

	names := map[string]bool{}
	inUnion := false
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		// Exact, so renaming the type cannot leave this test parsing a
		// different union and quietly reporting success.
		if trimmed == "export type ApiErrorCode =" {
			inUnion = true
			continue
		}
		if !inUnion {
			continue
		}
		if strings.HasPrefix(trimmed, `| "`) {
			names[strings.Trim(strings.TrimPrefix(trimmed, "|"), " \"")] = true
			continue
		}
		if trimmed != "" {
			break
		}
	}

	if len(names) == 0 {
		t.Fatalf("parsed no members from the ApiErrorCode union in %s — the union's "+
			"shape changed and this test is no longer checking anything", clientErrorCodeFile)
	}
	return names
}

// clientAffordanceFile holds the client's own copy of the code list,
// the one every affordance is keyed on.
const clientAffordanceFile = "../../ui/src/lib/affordance.ts"

const clientAffordanceAnchor = "export const ALL_ERROR_CODES = ["

// TestErrorCodesHaveClientAffordances guards the list the UI decides
// with, which is a different list from the union it merely types.
//
// The union above only says a code may arrive. This one says the client
// knows what to do about it — hide the control, explain it, or point at
// the fix. A code declared here and missing there falls through to
// whatever sentence the server happened to send, which is the state
// five of the eight codes were in before #171.
func TestErrorCodesHaveClientAffordances(t *testing.T) {
	client := clientAffordanceCodes(t)

	for _, code := range AllErrorCodes {
		if !client[code] {
			t.Errorf("code %q has no affordance in %s — the UI cannot decide what to "+
				"do about it and will show the raw server message", code, clientAffordanceFile)
		}
	}
	for code := range client {
		found := false
		for _, declared := range AllErrorCodes {
			if declared == code {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("code %q has an affordance in %s but the server no longer sends it",
				code, clientAffordanceFile)
		}
	}
}

func clientAffordanceCodes(t *testing.T) map[string]bool {
	t.Helper()

	src, err := os.ReadFile(clientAffordanceFile)
	if err != nil {
		t.Fatalf("cannot read %s: %v", clientAffordanceFile, err)
	}

	names := map[string]bool{}
	inList := false
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == clientAffordanceAnchor {
			inList = true
			continue
		}
		if !inList {
			continue
		}
		if strings.HasPrefix(trimmed, `"`) {
			names[strings.Trim(trimmed, `",`)] = true
			continue
		}
		if trimmed != "" {
			break
		}
	}

	if len(names) == 0 {
		t.Fatalf("parsed no codes from %s in %s — the list's shape changed and this "+
			"test is no longer checking anything", clientAffordanceAnchor, clientAffordanceFile)
	}
	return names
}
