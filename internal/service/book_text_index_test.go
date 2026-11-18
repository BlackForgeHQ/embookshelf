// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/blackforge/embookshelf/internal/model"
)

func TestMarkdownOutline(t *testing.T) {
	md := "# The Book\n" +
		"intro\n" +
		"## Part One ##\n" +
		"```go\n# not a heading\n```\n" +
		"#hashtag is not a heading\n" +
		"    # indented code is not either\n" +
		"   ### Three spaces is still a heading\n" +
		"~~~~\n## fenced\n~~~\n## still fenced (a shorter fence does not close)\n~~~~\n" +
		"## C#\n" +
		"####### seven is too many\n"

	got := markdownOutline(md)
	want := []struct {
		title string
		level int
	}{
		{"The Book", 1}, {"Part One", 2}, {"Three spaces is still a heading", 3}, {"C#", 2},
	}
	if len(got) != len(want) {
		t.Fatalf("outline = %+v", got)
	}
	for i, w := range want {
		if got[i].Title != w.title || got[i].Level != w.level {
			t.Errorf("entry %d = %+v, want %q level %d", i, got[i], w.title, w.level)
		}
		if !strings.HasPrefix(strings.TrimLeft(md[got[i].Offset:], " "), strings.Repeat("#", w.level)+" ") {
			t.Errorf("entry %d offset %d does not land on its heading line: %q", i, got[i].Offset, md[got[i].Offset:])
		}
	}
}

func TestBookTextOutlineOfAPDFReadsItsRendition(t *testing.T) {
	out, err := markdownText("# One\ntext\n# Two\n").Outline(context.Background(), pdfBook)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[1].Title != "Two" || out[1].Offset != int64(len("# One\ntext\n")) {
		t.Fatalf("outline = %+v", out)
	}
}

func TestBookTextOutlineOfAnEPUB(t *testing.T) {
	bt := &BookText{Books: &fakeBookOpener{src: epubWithChapters(t, "Alpha.", "Beta.")}}
	out, err := bt.Outline(context.Background(), model.Book{Format: "EPUB"})
	if err != nil {
		t.Fatal(err)
	}
	// No TOC in this fixture: every document is a section.
	if len(out) != 2 || out[1].Title != "Section 2" {
		t.Fatalf("outline = %+v", out)
	}
	page, err := bt.Page(context.Background(), model.Book{Format: "EPUB"}, out[1].Offset, 100)
	if err != nil || page.Text != "Beta." {
		t.Fatalf("paging from the outline's offset read %q, %v", page.Text, err)
	}
}

func TestBookTextOutlineRefusesTextlessFormats(t *testing.T) {
	if _, err := (&BookText{}).Outline(context.Background(), model.Book{Format: "MP3"}); !errors.Is(err, ErrNoBookText) {
		t.Fatalf("err = %v, want ErrNoBookText", err)
	}
}

func TestBookTextSearch(t *testing.T) {
	text := "The spice must flow. Spices\nflow too.  The SPICE   must flow, said Dюна. spicy."
	bt := markdownText(text)

	res, err := bt.Search(context.Background(), pdfBook, "spice must FLOW", 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 2 || len(res.Matches) != 2 || res.Partial || res.TotalCapped {
		t.Fatalf("phrase across spacing and case: %+v", res)
	}
	for _, m := range res.Matches {
		if !strings.HasPrefix(strings.ToLower(text[m.Offset:]), "spice") {
			t.Errorf("match offset %d lands on %q", m.Offset, text[m.Offset:])
		}
	}

	res, _ = bt.Search(context.Background(), pdfBook, "spices flow", 10)
	if res.Total != 1 {
		t.Fatalf("a phrase across a line break: total %d, want 1", res.Total)
	}

	// spice, Spices, SPICE — not spicy.
	res, _ = bt.Search(context.Background(), pdfBook, "spice", 1)
	if res.Total != 3 || len(res.Matches) != 1 {
		t.Fatalf("maxMatches caps the list, not the count: %+v", res)
	}

	res, _ = bt.Search(context.Background(), pdfBook, "a.b(c", 10)
	if res.Total != 0 {
		t.Fatalf("regexp metacharacters in a query must be literal: %+v", res)
	}

	if _, err := bt.Search(context.Background(), pdfBook, "   ", 10); !errors.Is(err, ErrEmptyTextQuery) {
		t.Fatalf("blank query err = %v", err)
	}
}

func TestSnippetIsBoundedValidAndMarked(t *testing.T) {
	text := strings.Repeat("Дюна ", 200) + "NEEDLE" + strings.Repeat(" 砂の惑星", 200)
	i := strings.Index(text, "NEEDLE")

	s := snippet(text, i, i+len("NEEDLE"))
	if !utf8.ValidString(s) {
		t.Fatal("snippet is not valid UTF-8")
	}
	if !strings.Contains(s, "NEEDLE") || !strings.HasPrefix(s, "…") || !strings.HasSuffix(s, "…") {
		t.Fatalf("snippet = %q", s)
	}
	if len(s) > len("NEEDLE")+2*snippetContext+2*len("…") {
		t.Fatalf("snippet is %d bytes, too long", len(s))
	}
	if whole := snippet("NEEDLE", 0, 6); whole != "NEEDLE" {
		t.Fatalf("an uncut snippet gained ellipses: %q", whole)
	}
}
