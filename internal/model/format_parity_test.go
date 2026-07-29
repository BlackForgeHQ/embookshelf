// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"os"
	"strings"
	"testing"
)

// clientFormatFile holds the client's own declaration of which formats
// can be narrated. Both tiers answer the question from a constant in
// their own layer — a runtime fetch to learn that EPUB has text would be
// absurd — so what stops them diverging is this test, following
// internal/sse's catalog parity and internal/handler's error-code union.
const clientFormatFile = "../../ui/src/lib/formats.ts"

// clientNarratableAnchor is matched exactly, so renaming the constant
// cannot leave this test parsing something else and reporting success.
const clientNarratableAnchor = "export const NARRATABLE_FORMATS = ["

// TestNarratableFormatsMatchClient asserts the two declarations are the
// same set, in both directions.
//
// A format the server narrates but the client omits is a Generate button
// that never appears for a book the backend would happily read. The
// reverse is a button that appears and then fails at the handler with a
// 415 the user cannot act on.
func TestNarratableFormatsMatchClient(t *testing.T) {
	t.Parallel()

	client := clientNarratableFormats(t)

	for _, format := range NarratableFormats() {
		if !client[format] {
			t.Errorf("format %q is narratable server-side but absent from %s — "+
				"the UI will never offer it", format, clientFormatFile)
		}
	}
	for format := range client {
		if !Narratable(format) {
			t.Errorf("format %q is listed as narratable in %s but the server refuses it — "+
				"the UI offers a button that 415s", format, clientFormatFile)
		}
	}
}

// clientNarratableFormats parses the members of the client's list.
func clientNarratableFormats(t *testing.T) map[string]bool {
	t.Helper()

	src, err := os.ReadFile(clientFormatFile)
	if err != nil {
		t.Fatalf("cannot read %s: %v — the client's declaration moved, and this "+
			"test is the only thing holding the two sets together", clientFormatFile, err)
	}

	names := map[string]bool{}
	inList := false
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == clientNarratableAnchor {
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
		t.Fatalf("parsed no members from %s in %s — the declaration's shape changed "+
			"and this test is no longer checking anything", clientNarratableAnchor, clientFormatFile)
	}
	return names
}
