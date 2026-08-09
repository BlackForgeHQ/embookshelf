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

// The anchors are matched exactly, so renaming a constant cannot leave
// this test parsing something else and reporting success.
const (
	clientNarratableAnchor  = "export const NARRATABLE_FORMATS = ["
	clientKindleAnchor      = "export const KINDLE_ELIGIBLE_FORMATS = ["
	clientConvertibleAnchor = "export const CONVERTIBLE_FORMATS = ["
)

// TestNarratableFormatsMatchClient asserts the two declarations are the
// same set, in both directions.
//
// A format the server narrates but the client omits is a Generate button
// that never appears for a book the backend would happily read. The
// reverse is a button that appears and then fails at the handler with a
// 415 the user cannot act on.
func TestNarratableFormatsMatchClient(t *testing.T) {
	t.Parallel()

	client := clientFormatList(t, clientNarratableAnchor)

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

// TestKindleEligibleFormatsMatchClient guards the other capability the
// client answers locally.
//
// Divergence here is a disabled button for a format Amazon would have
// taken, or an enabled one that 415s after the user clicks it — and
// Send-to-Kindle has no end-to-end coverage, so this pair of lists is
// most of what stands behind that gate.
func TestKindleEligibleFormatsMatchClient(t *testing.T) {
	t.Parallel()

	client := clientFormatList(t, clientKindleAnchor)

	for _, format := range KindleEligibleFormats() {
		if !client[format] {
			t.Errorf("format %q is Send-to-Kindle eligible server-side but absent from %s — "+
				"the UI disables a send that would have worked", format, clientFormatFile)
		}
	}
	for format := range client {
		if !KindleEligible(format) {
			t.Errorf("format %q is listed as Kindle-eligible in %s but the server refuses it — "+
				"the UI offers a send that 415s", format, clientFormatFile)
		}
	}
}

// TestConvertibleFormatsMatchClient guards the third capability set
// (ADR-0033). Divergence is a convert affordance for a format the
// server 415s, or a book quietly denied a conversion the sidecar would
// have done.
func TestConvertibleFormatsMatchClient(t *testing.T) {
	t.Parallel()

	client := clientFormatList(t, clientConvertibleAnchor)

	for _, format := range ConvertibleFormats() {
		if !client[format] {
			t.Errorf("format %q is convertible server-side but absent from %s — "+
				"the UI will never offer conversion", format, clientFormatFile)
		}
	}
	for format := range client {
		if !Convertible(format) {
			t.Errorf("format %q is listed as convertible in %s but the server refuses it — "+
				"the UI offers a conversion that 415s", format, clientFormatFile)
		}
	}
}

// The two capabilities are separate sets that happen to overlap. A
// client module that reused one list for both would pass each test above
// while making PDF narratable, so the difference itself is asserted.
func TestClientKeepsTheTwoCapabilitiesApart(t *testing.T) {
	t.Parallel()

	narratable := clientFormatList(t, clientNarratableAnchor)
	kindle := clientFormatList(t, clientKindleAnchor)

	if narratable["PDF"] {
		t.Error("the client lists PDF as narratable — the Kindle set has been reused for narration")
	}
	if !kindle["PDF"] {
		t.Error("the client omits PDF from the Kindle set — the narration set has been reused for sending")
	}
}

// clientFormatList parses the members of one of the client's lists.
func clientFormatList(t *testing.T, anchor string) map[string]bool {
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
		if trimmed == anchor {
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
			"and this test is no longer checking anything", anchor, clientFormatFile)
	}
	return names
}

// clientReaderAnchor opens the client's reader-kind map.
const clientReaderAnchor = "export const FORMAT_READERS: Record<string, ReaderKind> = {"

// TestReaderKindsMatchClient guards the third capability the client
// answers locally: which surface opens a format.
//
// Drift here is the reader route sending a book to a surface the server
// will not serve bytes for — an EPUB opened in the comic reader, or a
// "reader not implemented" page for a format the API happily streams.
// The route enumerated the format set itself before this (#194).
func TestReaderKindsMatchClient(t *testing.T) {
	t.Parallel()

	client := clientReaderKinds(t)

	for _, s := range FormatSpecs {
		if s.Reader == ReaderNone {
			if kind, ok := client[s.Format]; ok {
				t.Errorf("format %q has no reader server-side but %s sends it to the %q reader",
					s.Format, clientFormatFile, kind)
			}
			continue
		}
		if got := client[s.Format]; got != string(s.Reader) {
			t.Errorf("format %q opens the %q reader server-side but %q in %s",
				s.Format, s.Reader, got, clientFormatFile)
		}
	}
	for format := range client {
		if ReaderForFormat(format) == ReaderNone {
			t.Errorf("format %q is mapped to a reader in %s but the server has none for it",
				format, clientFormatFile)
		}
	}
}

// clientReaderKinds parses the client's format → reader map.
func clientReaderKinds(t *testing.T) map[string]string {
	t.Helper()

	src, err := os.ReadFile(clientFormatFile)
	if err != nil {
		t.Fatalf("cannot read %s: %v", clientFormatFile, err)
	}

	kinds := map[string]string{}
	inMap := false
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == clientReaderAnchor {
			inMap = true
			continue
		}
		if !inMap {
			continue
		}
		format, kind, found := strings.Cut(trimmed, ":")
		if !found {
			break
		}
		kinds[strings.TrimSpace(format)] = strings.Trim(strings.TrimSpace(kind), `",`)
	}

	if len(kinds) == 0 {
		t.Fatalf("parsed no entries from %s in %s — the map's shape changed and this "+
			"test is no longer checking anything", clientReaderAnchor, clientFormatFile)
	}
	return kinds
}
