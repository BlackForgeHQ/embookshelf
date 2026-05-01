package fileproc

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// bytesSource adapts a []byte to storage.Source for tests.
type bytesSource struct {
	data []byte
	r    *bytes.Reader
}

func newBytesSource(data []byte) *bytesSource {
	return &bytesSource{data: data, r: bytes.NewReader(data)}
}

func (b *bytesSource) ReadAt(p []byte, off int64) (int, error) {
	return b.r.ReadAt(p, off)
}
func (b *bytesSource) Close() error { return nil }
func (b *bytesSource) Size() int64  { return int64(len(b.data)) }

func TestMinimalFixture_Parses(t *testing.T) {
	data := makeMinimalEPUB(t)
	src := newBytesSource(data)
	defer func() { _ = src.Close() }()
	m, err := EPUBProcessor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if m.Title != "Original Title" {
		t.Errorf("Title=%q want Original Title", m.Title)
	}
	if m.Author != "Original Author" {
		t.Errorf("Author=%q want Original Author", m.Author)
	}
	if !m.HasCover {
		t.Error("HasCover=false; want true")
	}
}

func TestDispatchEmbedder_UnsupportedFormat(t *testing.T) {
	_, err := DispatchEmbedder("CBZ")
	if !errors.Is(err, ErrUnsupportedEmbed) {
		t.Errorf("got %v, want ErrUnsupportedEmbed", err)
	}
}

func TestDispatchEmbedder_EPUB(t *testing.T) {
	emb, err := DispatchEmbedder("EPUB")
	if err != nil {
		t.Fatalf("DispatchEmbedder(EPUB): %v", err)
	}
	if _, ok := emb.(EPUBEmbedder); !ok {
		t.Errorf("got %T, want EPUBEmbedder", emb)
	}
}

func TestMutateOPF_ScalarFields(t *testing.T) {
	original := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>Old</dc:title>
    <dc:creator opf:role="aut">Old Author</dc:creator>
    <dc:identifier id="bookid">urn:uuid:x</dc:identifier>
    <dc:language>en</dc:language>
  </metadata>
  <manifest><item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="ch1"/></spine>
</package>`)
	in := EmbedInput{
		Title:         "New Title",
		Subtitle:      "A Subtitle",
		Author:        "New Author",
		Description:   "Long description here.",
		Language:      "fr",
		Publisher:     "Acme Press",
		PublishedDate: "2024-06-15",
		ISBN:          "978-0-00-000000-0",
	}
	out, err := mutateOPF(original, in)
	if err != nil {
		t.Fatalf("mutateOPF: %v", err)
	}
	asString := string(out)
	for _, want := range []string{
		"<dc:title>New Title</dc:title>",
		"<dc:creator>New Author</dc:creator>",
		"<dc:description>Long description here.</dc:description>",
		"<dc:language>fr</dc:language>",
		"<dc:publisher>Acme Press</dc:publisher>",
		"<dc:date>2024-06-15</dc:date>",
		"<dc:identifier opf:scheme=\"ISBN\">978-0-00-000000-0</dc:identifier>",
	} {
		if !strings.Contains(asString, want) {
			t.Errorf("mutated OPF missing %q\n--- output ---\n%s", want, asString)
		}
	}
}

func TestMutateOPF_SeriesCalibreCompat(t *testing.T) {
	original := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" xmlns:opf="http://www.idpf.org/2007/opf">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf"
            xmlns:calibre="http://calibre.kovidgoyal.net/2009/metadata">
    <dc:title>X</dc:title>
  </metadata>
  <manifest/><spine/>
</package>`)
	in := EmbedInput{Title: "X", Series: "Foundation", SeriesIndex: 3}
	out, err := mutateOPF(original, in)
	if err != nil {
		t.Fatalf("mutateOPF: %v", err)
	}
	asString := string(out)
	for _, want := range []string{
		`property="belongs-to-collection"`,
		`property="group-position">3</meta>`,
		`<meta name="calibre:series" content="Foundation"`,
		`<meta name="calibre:series_index" content="3"`,
	} {
		if !strings.Contains(asString, want) {
			t.Errorf("output missing %q\n%s", want, asString)
		}
	}
}
