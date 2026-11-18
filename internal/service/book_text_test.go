// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
)

// markdownText is a BookText over a ready Markdown rendition holding text.
func markdownText(text string) *BookText {
	return &BookText{
		Books: &fakeBookOpener{err: errors.New("a PDF's own bytes must not be opened")},
		Markdown: &MarkdownFeed{
			Renditions: &fakeRenditionReader{row: model.MarkdownRendition{State: model.RenditionReady, Location: "x.md"}},
			Open: func(context.Context, model.Book, string) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader(text)), nil
			},
			CurrentHash: func(context.Context, model.Book) []byte { return nil },
		},
	}
}

var pdfBook = model.Book{ID: "b1", Format: "PDF"}

// epubWithChapters builds an EPUB with one spine document per body.
func epubWithChapters(t *testing.T, bodies ...string) storage.Source {
	t.Helper()
	var (
		buf             bytes.Buffer
		manifest, spine strings.Builder
	)
	zw := zip.NewWriter(&buf)
	add := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip: %v", err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	for i, body := range bodies {
		id := fmt.Sprintf("c%d", i)
		fmt.Fprintf(&manifest, `<item id="%s" href="%s.xhtml" media-type="application/xhtml+xml"/>`, id, id)
		fmt.Fprintf(&spine, `<itemref idref="%s"/>`, id)
		add(id+".xhtml", `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><body><p>`+body+`</p></body></html>`)
	}
	add("META-INF/container.xml", `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`)
	add("content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><manifest>`+manifest.String()+`</manifest><spine>`+spine.String()+`</spine></package>`)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	b := buf.Bytes()
	return memSrc{Reader: bytes.NewReader(b), size: int64(len(b))}
}

func TestBookTextReadReportsTruncation(t *testing.T) {
	bt := markdownText("0123456789")

	got, more, err := bt.Read(context.Background(), pdfBook, 10)
	if err != nil || got != "0123456789" || more {
		t.Fatalf("exact fit: %q, more=%v, err=%v", got, more, err)
	}
	got, more, err = bt.Read(context.Background(), pdfBook, 4)
	if err != nil || got != "0123" || !more {
		t.Fatalf("truncated: %q, more=%v, err=%v", got, more, err)
	}
}

func TestBookTextPagesReassembleMultibyteText(t *testing.T) {
	// Every rune here is 2-4 bytes, so a byte-sized page lands mid-rune
	// on almost every boundary.
	text := strings.Repeat("Дюна — 砂の惑星 🏜️ ", 40)
	bt := markdownText(text)

	var (
		got    strings.Builder
		offset int64
	)
	for pages := 0; ; pages++ {
		if pages > 1000 {
			t.Fatal("paging never ended")
		}
		p, err := bt.Page(context.Background(), pdfBook, offset, 7)
		if err != nil {
			t.Fatal(err)
		}
		if !utf8.ValidString(p.Text) {
			t.Fatalf("page at %d is not valid UTF-8: %q", offset, p.Text)
		}
		if p.Offset != offset {
			t.Fatalf("page started at %d, asked for %d (a next_offset should already be aligned)", p.Offset, offset)
		}
		got.WriteString(p.Text)
		if p.NextOffset == nil {
			break
		}
		if *p.NextOffset <= offset {
			t.Fatalf("next offset %d does not advance past %d", *p.NextOffset, offset)
		}
		offset = *p.NextOffset
	}
	if got.String() != text {
		t.Fatal("pages do not reassemble the text")
	}
}

// Paging a real multi-chapter EPUB in pages of every size must reassemble
// the whole-book read exactly. Caught live: pages used to stop early when
// a cut ended on whitespace, and drift by a byte at chapter boundaries.
func TestBookTextPagesAnEPUBAcrossChapters(t *testing.T) {
	src := epubWithChapters(t,
		strings.Repeat("Chapter one about dunes and spice. ", 20),
		strings.Repeat("Chapter two: Дюна, 砂の惑星. ", 20),
	)
	bt := &BookText{Books: &fakeBookOpener{src: src}}
	book := model.Book{Format: "EPUB"}
	full, more, err := bt.Read(context.Background(), book, 1<<20)
	if err != nil || more {
		t.Fatalf("full read: more=%v err=%v", more, err)
	}

	for _, size := range []int64{1, 7, 64, 333, int64(len(full)) - 1, int64(len(full))} {
		var (
			got    strings.Builder
			offset int64
		)
		for pages := 0; ; pages++ {
			if pages > len(full)+1 {
				t.Fatalf("size %d: paging never ended", size)
			}
			p, err := bt.Page(context.Background(), book, offset, size)
			if err != nil {
				t.Fatalf("size %d offset %d: %v", size, offset, err)
			}
			got.WriteString(p.Text)
			if p.NextOffset == nil {
				break
			}
			offset = *p.NextOffset
		}
		if got.String() != full {
			t.Fatalf("size %d: pages reassemble %d bytes, want %d", size, got.Len(), len(full))
		}
	}
}

func TestBookTextPageMovesAMidRuneStartForward(t *testing.T) {
	bt := markdownText("aДb") // Д is 2 bytes, at offsets 1-2

	p, err := bt.Page(context.Background(), pdfBook, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if p.Offset != 3 || p.Text != "b" {
		t.Fatalf("page = %+v, want offset 3 text \"b\"", p)
	}
}

func TestBookTextPagePastTheEndIsEmpty(t *testing.T) {
	p, err := markdownText("short").Page(context.Background(), pdfBook, 100, 10)
	if err != nil {
		t.Fatal(err)
	}
	if p.Text != "" || p.NextOffset != nil {
		t.Fatalf("page = %+v, want empty and final", p)
	}
}

func TestBookTextReadsEPUBFromItsOwnBytes(t *testing.T) {
	bt := &BookText{Books: &fakeBookOpener{src: epubWithText(t, "Arrakis. Dune. Desert planet.")}}

	got, more, err := bt.Read(context.Background(), model.Book{Format: "epub"}, 1000)
	if err != nil || more || got != "Arrakis. Dune. Desert planet." {
		t.Fatalf("got %q, more=%v, err=%v", got, more, err)
	}
}

func TestBookTextRefusals(t *testing.T) {
	ctx := context.Background()

	if _, _, err := (&BookText{}).Read(ctx, model.Book{Format: "CBZ"}, 10); !errors.Is(err, ErrNoBookText) {
		t.Errorf("CBZ: err = %v, want ErrNoBookText", err)
	}
	// No converter wired: a PDF is textless, as before ADR-0033.
	if _, _, err := (&BookText{}).Read(ctx, pdfBook, 10); !errors.Is(err, ErrNoBookText) {
		t.Errorf("PDF without converter: err = %v, want ErrNoBookText", err)
	}
	empty := &BookText{Books: &fakeBookOpener{src: epubWithText(t, "")}}
	if _, _, err := empty.Read(ctx, model.Book{Format: "EPUB"}, 10); !errors.Is(err, fileproc.ErrNoReadableText) {
		t.Errorf("empty EPUB: err = %v, want ErrNoReadableText", err)
	}
}

// A missing rendition is requested and reported pending — the feed's
// loud-failure rule, which the MCP read inherits (ADR-0039 §5).
func TestBookTextRequestsAMissingRendition(t *testing.T) {
	requested := ""
	bt := &BookText{Markdown: &MarkdownFeed{
		Renditions: &fakeRenditionReader{missing: true},
		IsMissing:  func(err error) bool { return errors.Is(err, errNoRow) },
		Request:    func(_ context.Context, id string) error { requested = id; return nil },
	}}

	_, _, err := bt.Read(context.Background(), pdfBook, 10)
	if !errors.Is(err, ErrRenditionPending) {
		t.Fatalf("err = %v, want ErrRenditionPending", err)
	}
	if requested != pdfBook.ID {
		t.Fatalf("requested %q, want %q", requested, pdfBook.ID)
	}
}
