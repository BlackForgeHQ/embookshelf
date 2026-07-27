// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/storage"
)

// epubFile is one entry in a synthetic EPUB.
type epubFile struct{ name, body string }

// buildEPUB assembles a minimal but structurally real EPUB: container.xml
// pointing at an OPF, and whatever files the caller supplies.
func buildEPUB(t *testing.T, opfPath, opf string, files ...epubFile) storage.Source {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	write := func(name, body string) {
		t.Helper()
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}

	write("META-INF/container.xml", `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="`+opfPath+`" media-type="application/oebps-package+xml"/></rootfiles>
</container>`)
	write(opfPath, opf)
	for _, f := range files {
		write(f.name, f.body)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return memSourceFromBytes(buf.Bytes())
}

func xhtml(body string) string {
	return `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>ignored</title></head>
<body>` + body + `</body></html>`
}

const noLimit = 1 << 20

// TestExtractEPUBTextReadsSpineOrder — the spine is the reading order, and
// it is routinely not the manifest order. Concatenating the manifest would
// hand the model a shuffled book.
func TestExtractEPUBTextReadsSpineOrder(t *testing.T) {
	src := buildEPUB(t, "OEBPS/content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <manifest>
    <item id="c2" href="two.xhtml" media-type="application/xhtml+xml"/>
    <item id="c1" href="one.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="c1"/><itemref idref="c2"/></spine>
</package>`,
		epubFile{"OEBPS/one.xhtml", xhtml(`<p>First chapter.</p>`)},
		epubFile{"OEBPS/two.xhtml", xhtml(`<p>Second chapter.</p>`)},
	)

	got, _, err := ExtractEPUBText(context.Background(), src, noLimit)
	if err != nil {
		t.Fatalf("ExtractEPUBText: %v", err)
	}
	first := strings.Index(got, "First chapter.")
	second := strings.Index(got, "Second chapter.")
	if first < 0 || second < 0 {
		t.Fatalf("missing chapters in %q", got)
	}
	if first > second {
		t.Fatalf("manifest order used, not spine order: %q", got)
	}
}

// TestExtractEPUBTextStripsMarkup — the model should read prose, not tags,
// and paying for angle brackets by the token is pure waste.
func TestExtractEPUBTextStripsMarkup(t *testing.T) {
	src := simpleEPUB(t, xhtml(`<h1>Chapter One</h1><p>He said <em>hello</em> to her.</p>`))

	got, _, err := ExtractEPUBText(context.Background(), src, noLimit)
	if err != nil {
		t.Fatalf("ExtractEPUBText: %v", err)
	}
	if strings.Contains(got, "<") || strings.Contains(got, "xmlns") {
		t.Fatalf("markup survived: %q", got)
	}
	for _, want := range []string{"Chapter One", "hello", "to her"} {
		if !strings.Contains(got, want) {
			t.Errorf("text %q missing from %q", want, got)
		}
	}
}

// TestExtractEPUBTextDropsScriptAndStyle — their contents are not prose and
// would otherwise be concatenated verbatim into the prompt.
func TestExtractEPUBTextDropsScriptAndStyle(t *testing.T) {
	src := simpleEPUB(t, xhtml(
		`<style>body { color: red; }</style><script>var x = 1;</script><p>Real prose.</p>`))

	got, _, err := ExtractEPUBText(context.Background(), src, noLimit)
	if err != nil {
		t.Fatalf("ExtractEPUBText: %v", err)
	}
	if !strings.Contains(got, "Real prose.") {
		t.Fatalf("prose missing: %q", got)
	}
	for _, unwanted := range []string{"color: red", "var x"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("%q leaked into %q", unwanted, got)
		}
	}
}

func TestExtractEPUBTextDecodesEntities(t *testing.T) {
	src := simpleEPUB(t, xhtml(`<p>Salt &amp; sand &#8212; not &lt;tags&gt;.</p>`))

	got, _, err := ExtractEPUBText(context.Background(), src, noLimit)
	if err != nil {
		t.Fatalf("ExtractEPUBText: %v", err)
	}
	if !strings.Contains(got, "Salt & sand") {
		t.Errorf("entity not decoded: %q", got)
	}
	if !strings.Contains(got, "<tags>") {
		t.Errorf("escaped angle brackets lost: %q", got)
	}
}

// TestExtractEPUBTextSkipsNonDocuments — a spine should only reference
// documents, but manifests carry images and stylesheets, and a malformed
// spine can point at them.
func TestExtractEPUBTextSkipsNonDocuments(t *testing.T) {
	src := buildEPUB(t, "content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <manifest>
    <item id="css" href="style.css" media-type="text/css"/>
    <item id="c1" href="one.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="css"/><itemref idref="c1"/></spine>
</package>`,
		epubFile{"style.css", "body { color: red; }"},
		epubFile{"one.xhtml", xhtml(`<p>Only prose.</p>`)},
	)

	got, _, err := ExtractEPUBText(context.Background(), src, noLimit)
	if err != nil {
		t.Fatalf("ExtractEPUBText: %v", err)
	}
	if strings.Contains(got, "color: red") {
		t.Fatalf("stylesheet was read as a document: %q", got)
	}
	if !strings.Contains(got, "Only prose.") {
		t.Fatalf("document missing: %q", got)
	}
}

// TestExtractEPUBTextResolvesHrefsRelativeToOPF — hrefs are relative to the
// OPF's own directory, not the archive root. Getting this wrong makes every
// chapter unreadable in any EPUB that nests its OPF, which is most of them.
func TestExtractEPUBTextResolvesHrefsRelativeToOPF(t *testing.T) {
	src := buildEPUB(t, "OEBPS/pkg/content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <manifest><item id="c1" href="../text/one.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="c1"/></spine>
</package>`,
		epubFile{"OEBPS/text/one.xhtml", xhtml(`<p>Nested prose.</p>`)},
	)

	got, _, err := ExtractEPUBText(context.Background(), src, noLimit)
	if err != nil {
		t.Fatalf("ExtractEPUBText: %v", err)
	}
	if !strings.Contains(got, "Nested prose.") {
		t.Fatalf("relative href not resolved: %q", got)
	}
}

// TestExtractEPUBTextRespectsLimit — an EPUB can be hundreds of megabytes,
// and the caller pays per token. The cap has to bind and be reported, so the
// generator knows the model saw only part of the book.
func TestExtractEPUBTextRespectsLimit(t *testing.T) {
	long := strings.Repeat("word ", 5000)
	src := simpleEPUB(t, xhtml(`<p>`+long+`</p>`))

	got, truncated, err := ExtractEPUBText(context.Background(), src, 100)
	if err != nil {
		t.Fatalf("ExtractEPUBText: %v", err)
	}
	if len(got) > 100 {
		t.Fatalf("limit ignored: got %d bytes, cap 100", len(got))
	}
	if !truncated {
		t.Error("truncated=false despite hitting the cap")
	}
}

func TestExtractEPUBTextNotTruncatedWhenUnderLimit(t *testing.T) {
	src := simpleEPUB(t, xhtml(`<p>Short.</p>`))

	_, truncated, err := ExtractEPUBText(context.Background(), src, noLimit)
	if err != nil {
		t.Fatalf("ExtractEPUBText: %v", err)
	}
	if truncated {
		t.Error("truncated=true on a book well under the cap")
	}
}

// TestExtractEPUBTextEmptySpine — a book with no readable documents must
// say so rather than hand back an empty string the caller treats as prose.
func TestExtractEPUBTextEmptySpine(t *testing.T) {
	src := buildEPUB(t, "content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <manifest/><spine/>
</package>`)

	if _, _, err := ExtractEPUBText(context.Background(), src, noLimit); err == nil {
		t.Fatal("no error for an EPUB with nothing to read")
	}
}

func TestExtractEPUBTextRejectsNonEPUB(t *testing.T) {
	src := memSourceFromBytes([]byte("this is not a zip archive at all"))

	if _, _, err := ExtractEPUBText(context.Background(), src, noLimit); err == nil {
		t.Fatal("no error for a non-archive")
	}
}

// simpleEPUB wraps one document in a single-item spine.
func simpleEPUB(t *testing.T, body string) storage.Source {
	t.Helper()
	return buildEPUB(t, "content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <manifest><item id="c1" href="one.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="c1"/></spine>
</package>`, epubFile{"one.xhtml", body})
}
