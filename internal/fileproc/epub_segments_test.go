// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// Boundaries — one segment per spine document, in reading order
// ---------------------------------------------------------------------------

// Narration is billed and retried per segment, so the split has to follow
// the book's own structure rather than the manifest's arbitrary order.
func TestExtractEPUBSegmentsSplitsOnSpineItemsInReadingOrder(t *testing.T) {
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

	segs, err := ExtractEPUBSegments(context.Background(), src, SegmentOptions{})
	if err != nil {
		t.Fatalf("ExtractEPUBSegments: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("got %d segments, want 2: %+v", len(segs), segs)
	}
	if !strings.Contains(segs[0].Text, "First chapter.") {
		t.Errorf("segment 0 text = %q, want the first chapter", segs[0].Text)
	}
	if !strings.Contains(segs[1].Text, "Second chapter.") {
		t.Errorf("segment 1 text = %q, want the second chapter", segs[1].Text)
	}
	if strings.Contains(segs[0].Text, "Second chapter.") {
		t.Error("segment 0 leaked the next document's text — boundaries are not being kept")
	}
}

// Seq is what orders the finished audio and what a retry addresses, so it
// must be dense and monotonic even when documents are skipped.
func TestExtractEPUBSegmentsNumbersSequentiallyFromZero(t *testing.T) {
	src := buildEPUB(t, "OEBPS/content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <manifest>
    <item id="c1" href="one.xhtml" media-type="application/xhtml+xml"/>
    <item id="css" href="s.css" media-type="text/css"/>
    <item id="c2" href="two.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="c1"/><itemref idref="css"/><itemref idref="c2"/></spine>
</package>`,
		epubFile{"OEBPS/one.xhtml", xhtml(`<p>One.</p>`)},
		epubFile{"OEBPS/s.css", `body { color: red }`},
		epubFile{"OEBPS/two.xhtml", xhtml(`<p>Two.</p>`)},
	)

	segs, err := ExtractEPUBSegments(context.Background(), src, SegmentOptions{})
	if err != nil {
		t.Fatalf("ExtractEPUBSegments: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("got %d segments, want 2 (the stylesheet is not prose)", len(segs))
	}
	for i, s := range segs {
		if s.Seq != i {
			t.Errorf("segs[%d].Seq = %d, want %d", i, s.Seq, i)
		}
	}
}

// A document that extracts to nothing — an image plate, an empty divider —
// must not become a segment. Paying an engine to narrate silence is the
// mildest failure; a zero-length MP3 in the concatenation is not.
func TestExtractEPUBSegmentsSkipsDocumentsWithNoProse(t *testing.T) {
	src := buildEPUB(t, "OEBPS/content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <manifest>
    <item id="plate" href="plate.xhtml" media-type="application/xhtml+xml"/>
    <item id="c1" href="one.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="plate"/><itemref idref="c1"/></spine>
</package>`,
		epubFile{"OEBPS/plate.xhtml", xhtml(`<img src="plate.jpg" alt="A plate"/>`)},
		epubFile{"OEBPS/one.xhtml", xhtml(`<p>Real prose.</p>`)},
	)

	segs, err := ExtractEPUBSegments(context.Background(), src, SegmentOptions{})
	if err != nil {
		t.Fatalf("ExtractEPUBSegments: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("got %d segments, want 1: %+v", len(segs), segs)
	}
	if strings.Contains(segs[0].Text, "A plate") {
		t.Error("image alt text reached the narration")
	}
}

// An EPUB with no prose at all cannot be narrated, and the caller needs to
// tell that apart from a corrupt archive.
func TestExtractEPUBSegmentsReportsNoReadableText(t *testing.T) {
	src := buildEPUB(t, "OEBPS/content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <manifest><item id="css" href="s.css" media-type="text/css"/></manifest>
  <spine><itemref idref="css"/></spine>
</package>`,
		epubFile{"OEBPS/s.css", `body{}`},
	)

	if _, err := ExtractEPUBSegments(context.Background(), src, SegmentOptions{}); err == nil {
		t.Fatal("want ErrNoReadableText for a book with no prose, got nil")
	}
}

// ---------------------------------------------------------------------------
// Titles — from the table of contents, which is the only place they exist
// ---------------------------------------------------------------------------

// EPUB 3 keeps the table of contents in an XHTML nav document, flagged by
// a manifest property rather than a media type.
func TestExtractEPUBSegmentsTakesTitlesFromEPUB3Nav(t *testing.T) {
	src := buildEPUB(t, "OEBPS/content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="c1" href="one.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="two.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="c1"/><itemref idref="c2"/></spine>
</package>`,
		epubFile{"OEBPS/nav.xhtml", xhtml(`<nav epub:type="toc"><ol>
      <li><a href="one.xhtml">The Ruined Map</a></li>
      <li><a href="two.xhtml#start">Woman in the Dunes</a></li>
    </ol></nav>`)},
		epubFile{"OEBPS/one.xhtml", xhtml(`<p>One.</p>`)},
		epubFile{"OEBPS/two.xhtml", xhtml(`<p>Two.</p>`)},
	)

	segs, err := ExtractEPUBSegments(context.Background(), src, SegmentOptions{})
	if err != nil {
		t.Fatalf("ExtractEPUBSegments: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("got %d segments, want 2", len(segs))
	}
	if segs[0].ChapterTitle != "The Ruined Map" {
		t.Errorf("segs[0].ChapterTitle = %q, want %q", segs[0].ChapterTitle, "The Ruined Map")
	}
	// The fragment must be stripped: a TOC entry pointing mid-file still
	// names the document it lives in.
	if segs[1].ChapterTitle != "Woman in the Dunes" {
		t.Errorf("segs[1].ChapterTitle = %q, want %q", segs[1].ChapterTitle, "Woman in the Dunes")
	}
}

// EPUB 2 books are still the majority of most libraries, and they keep the
// table of contents in a separate NCX document.
func TestExtractEPUBSegmentsTakesTitlesFromNCX(t *testing.T) {
	src := buildEPUB(t, "OEBPS/content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
  <manifest>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
    <item id="c1" href="one.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine toc="ncx"><itemref idref="c1"/></spine>
</package>`,
		epubFile{"OEBPS/toc.ncx", `<?xml version="1.0"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <navMap>
    <navPoint id="np1" playOrder="1">
      <navLabel><text>Chapter the First</text></navLabel>
      <content src="one.xhtml"/>
    </navPoint>
  </navMap>
</ncx>`},
		epubFile{"OEBPS/one.xhtml", xhtml(`<p>One.</p>`)},
	)

	segs, err := ExtractEPUBSegments(context.Background(), src, SegmentOptions{})
	if err != nil {
		t.Fatalf("ExtractEPUBSegments: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("got %d segments, want 1", len(segs))
	}
	if segs[0].ChapterTitle != "Chapter the First" {
		t.Errorf("ChapterTitle = %q, want %q", segs[0].ChapterTitle, "Chapter the First")
	}
}

// Plenty of books have documents the TOC never names. The drawer still
// needs a label for them, and it has to be stable and human-readable.
func TestExtractEPUBSegmentsFallsBackToNumberedSectionTitles(t *testing.T) {
	src := buildEPUB(t, "OEBPS/content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <manifest>
    <item id="c1" href="one.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="two.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="c1"/><itemref idref="c2"/></spine>
</package>`,
		epubFile{"OEBPS/one.xhtml", xhtml(`<p>One.</p>`)},
		epubFile{"OEBPS/two.xhtml", xhtml(`<p>Two.</p>`)},
	)

	segs, err := ExtractEPUBSegments(context.Background(), src, SegmentOptions{})
	if err != nil {
		t.Fatalf("ExtractEPUBSegments: %v", err)
	}
	if segs[0].ChapterTitle != "Section 1" || segs[1].ChapterTitle != "Section 2" {
		t.Errorf("titles = %q / %q, want Section 1 / Section 2",
			segs[0].ChapterTitle, segs[1].ChapterTitle)
	}
}

// ---------------------------------------------------------------------------
// Front matter — the parts nobody wants read aloud
// ---------------------------------------------------------------------------

// Narrating the table of contents is worse than useless: eight minutes of
// a robot reading chapter names before the book starts.
func TestExtractEPUBSegmentsSkipsTheNavDocument(t *testing.T) {
	src := buildEPUB(t, "OEBPS/content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="c1" href="one.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="nav"/><itemref idref="c1"/></spine>
</package>`,
		epubFile{"OEBPS/nav.xhtml", xhtml(`<nav epub:type="toc"><ol>
      <li><a href="one.xhtml">Chapter One</a></li>
    </ol></nav>`)},
		epubFile{"OEBPS/one.xhtml", xhtml(`<p>Real prose.</p>`)},
	)

	segs, err := ExtractEPUBSegments(context.Background(), src, SegmentOptions{})
	if err != nil {
		t.Fatalf("ExtractEPUBSegments: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("got %d segments, want 1 — the nav document was narrated", len(segs))
	}
	if segs[0].ChapterTitle != "Chapter One" {
		t.Errorf("ChapterTitle = %q, want %q", segs[0].ChapterTitle, "Chapter One")
	}
}

// EPUB 2 books declare their front matter in a guide element, which is
// more reliable than guessing from the filename.
func TestExtractEPUBSegmentsSkipsGuideDeclaredFrontMatter(t *testing.T) {
	src := buildEPUB(t, "OEBPS/content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
  <manifest>
    <item id="cov" href="front.xhtml" media-type="application/xhtml+xml"/>
    <item id="rights" href="rights.xhtml" media-type="application/xhtml+xml"/>
    <item id="c1" href="one.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="cov"/><itemref idref="rights"/><itemref idref="c1"/></spine>
  <guide>
    <reference type="cover" href="front.xhtml"/>
    <reference type="copyright-page" href="rights.xhtml"/>
  </guide>
</package>`,
		epubFile{"OEBPS/front.xhtml", xhtml(`<p>A Novel by Someone</p>`)},
		epubFile{"OEBPS/rights.xhtml", xhtml(`<p>All rights reserved.</p>`)},
		epubFile{"OEBPS/one.xhtml", xhtml(`<p>Real prose.</p>`)},
	)

	segs, err := ExtractEPUBSegments(context.Background(), src, SegmentOptions{})
	if err != nil {
		t.Fatalf("ExtractEPUBSegments: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("got %d segments, want 1: %+v", len(segs), segs)
	}
	if !strings.Contains(segs[0].Text, "Real prose.") {
		t.Errorf("wrong document survived: %q", segs[0].Text)
	}
}

// ---------------------------------------------------------------------------
// The size cap — what keeps a job inside River's rescue window
// ---------------------------------------------------------------------------

// A long chapter becomes several jobs, but stays one chapter: the drawer
// shows what the author wrote, not what the engine's request cap forced.
func TestExtractEPUBSegmentsSplitsLongChaptersKeepingOneChapterIdentity(t *testing.T) {
	sentence := "This is a sentence of a reasonable length. "
	long := strings.Repeat(sentence, 60) // ~2.5k chars

	src := buildEPUB(t, "OEBPS/content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="c1" href="one.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="c1"/></spine>
</package>`,
		epubFile{"OEBPS/nav.xhtml", xhtml(`<nav><ol><li><a href="one.xhtml">A Long Chapter</a></li></ol></nav>`)},
		epubFile{"OEBPS/one.xhtml", xhtml(`<p>` + long + `</p>`)},
	)

	segs, err := ExtractEPUBSegments(context.Background(), src, SegmentOptions{MaxChars: 500})
	if err != nil {
		t.Fatalf("ExtractEPUBSegments: %v", err)
	}
	if len(segs) < 3 {
		t.Fatalf("got %d segments, want the chapter split across several", len(segs))
	}
	for i, s := range segs {
		if s.ChapterIndex != 0 {
			t.Errorf("segs[%d].ChapterIndex = %d, want 0 — the split invented a chapter", i, s.ChapterIndex)
		}
		if s.ChapterTitle != "A Long Chapter" {
			t.Errorf("segs[%d].ChapterTitle = %q, want the chapter's own title", i, s.ChapterTitle)
		}
		if len(s.Text) > 500 {
			t.Errorf("segs[%d] is %d chars, over the 500 cap", i, len(s.Text))
		}
	}
}

// A cut mid-sentence is audible at every seam, and a book has ~180 of
// them. Each piece must end where a sentence does.
func TestExtractEPUBSegmentsCutsOnSentenceBoundaries(t *testing.T) {
	long := strings.Repeat("Alpha beta gamma delta. ", 100)

	src := buildEPUB(t, "OEBPS/content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <manifest><item id="c1" href="one.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="c1"/></spine>
</package>`,
		epubFile{"OEBPS/one.xhtml", xhtml(`<p>` + long + `</p>`)},
	)

	segs, err := ExtractEPUBSegments(context.Background(), src, SegmentOptions{MaxChars: 300})
	if err != nil {
		t.Fatalf("ExtractEPUBSegments: %v", err)
	}
	if len(segs) < 2 {
		t.Fatalf("got %d segments, want several", len(segs))
	}
	// Every piece but the last ends a sentence.
	for i, s := range segs[:len(segs)-1] {
		if !strings.HasSuffix(s.Text, ".") {
			t.Errorf("segs[%d] ends %q, want a sentence boundary", i, tail(s.Text, 24))
		}
	}
	// And no text is lost across the seams. Joined with a space, because
	// the seam is where one synthesis request ends and the next begins —
	// concatenating bare would glue the last word to the first.
	var joined strings.Builder
	for _, s := range segs {
		joined.WriteString(s.Text)
		joined.WriteByte(' ')
	}
	if got, want := len(strings.Fields(joined.String())), len(strings.Fields(strings.TrimSpace(long))); got != want {
		t.Errorf("word count across segments = %d, want %d — the split dropped text", got, want)
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// The cap is in characters, not bytes, and a cut must never land inside a
// multi-byte rune — an engine handed invalid UTF-8 either errors or reads
// a replacement character aloud.
func TestExtractEPUBSegmentsSplitsMultibyteTextSafely(t *testing.T) {
	long := strings.Repeat("彼は砂の中に降りていった。", 80)

	src := buildEPUB(t, "OEBPS/content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <manifest><item id="c1" href="one.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="c1"/></spine>
</package>`,
		epubFile{"OEBPS/one.xhtml", xhtml(`<p>` + long + `</p>`)},
	)

	segs, err := ExtractEPUBSegments(context.Background(), src, SegmentOptions{MaxChars: 100})
	if err != nil {
		t.Fatalf("ExtractEPUBSegments: %v", err)
	}
	if len(segs) < 2 {
		t.Fatalf("got %d segments, want several", len(segs))
	}
	for i, s := range segs {
		if !utf8.ValidString(s.Text) {
			t.Errorf("segs[%d] is not valid UTF-8 — a rune was cut in half", i)
		}
		if n := utf8.RuneCountInString(s.Text); n > 100 {
			t.Errorf("segs[%d] is %d characters, over the 100 cap", i, n)
		}
	}
	// The CJK full stop is a sentence boundary too.
	for i, s := range segs[:len(segs)-1] {
		if !strings.HasSuffix(s.Text, "。") {
			t.Errorf("segs[%d] ends %q, want a CJK sentence boundary", i, tail(s.Text, 12))
		}
	}
}

// ---------------------------------------------------------------------------
// Character ranges — the alignment map, and the run's proof of its own plan
// ---------------------------------------------------------------------------

// The offsets are the alignment map's text side, and they are also what a
// segment worker re-extracting the book hours later compares against to
// know it is narrating the same prose the plan priced. Computing them
// here rather than at the two call sites is what makes the two agree by
// construction (#189).
func TestExtractEPUBSegmentsCarriesContiguousCharacterRanges(t *testing.T) {
	src := buildEPUB(t, "OEBPS/content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <manifest>
    <item id="c1" href="one.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="two.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="c1"/><itemref idref="c2"/></spine>
</package>`,
		epubFile{"OEBPS/one.xhtml", xhtml(`<p>First chapter.</p>`)},
		epubFile{"OEBPS/two.xhtml", xhtml(`<p>Second chapter.</p>`)},
	)

	segs, err := ExtractEPUBSegments(context.Background(), src, SegmentOptions{})
	if err != nil {
		t.Fatalf("ExtractEPUBSegments: %v", err)
	}
	if segs[0].CharStart != 0 {
		t.Errorf("segs[0].CharStart = %d, want 0 — the first segment opens the narrated text", segs[0].CharStart)
	}
	offset := 0
	for i, s := range segs {
		length := utf8.RuneCountInString(s.Text)
		if s.CharStart != offset {
			t.Errorf("segs[%d].CharStart = %d, want %d — ranges must be contiguous", i, s.CharStart, offset)
		}
		if s.CharEnd != offset+length {
			t.Errorf("segs[%d].CharEnd = %d, want %d — the range must cover its own text",
				i, s.CharEnd, offset+length)
		}
		offset += length
	}
}

// The ranges count characters, not bytes: an offset that drifts on every
// CJK chapter would put the audio and the text out of step for the rest
// of the book.
func TestExtractEPUBSegmentsCountsCharacterRangesInRunes(t *testing.T) {
	src := buildEPUB(t, "OEBPS/content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <manifest>
    <item id="c1" href="one.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="two.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="c1"/><itemref idref="c2"/></spine>
</package>`,
		epubFile{"OEBPS/one.xhtml", xhtml(`<p>彼は砂の中に降りていった。</p>`)},
		epubFile{"OEBPS/two.xhtml", xhtml(`<p>Second chapter.</p>`)},
	)

	segs, err := ExtractEPUBSegments(context.Background(), src, SegmentOptions{})
	if err != nil {
		t.Fatalf("ExtractEPUBSegments: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("got %d segments, want 2", len(segs))
	}
	want := utf8.RuneCountInString(segs[0].Text)
	if segs[1].CharStart != want {
		t.Errorf("segs[1].CharStart = %d, want %d runes — the offset was counted in bytes",
			segs[1].CharStart, want)
	}
}
