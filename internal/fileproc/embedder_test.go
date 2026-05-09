// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
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
		EditableMetadata: model.EditableMetadata{
			Title:         "New Title",
			Subtitle:      "A Subtitle",
			Author:        "New Author",
			Description:   "Long description here.",
			Language:      "fr",
			Publisher:     "Acme Press",
			PublishedDate: "2024-06-15",
			ISBN:          "978-0-00-000000-0",
		},
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
	in := EmbedInput{EditableMetadata: model.EditableMetadata{Title: "X", Series: "Foundation", SeriesIndex: 3}}
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

func TestMutateOPF_TagsAndGenresDualWrite(t *testing.T) {
	original := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>X</dc:title>
  </metadata>
  <manifest/><spine/>
</package>`)
	in := EmbedInput{
		EditableMetadata: model.EditableMetadata{
			Title:  "X",
			Tags:   []string{"jazz-age", "tragedy"},
			Genres: []string{"fiction", "literary"},
		},
	}
	out, err := mutateOPF(original, in)
	if err != nil {
		t.Fatalf("mutateOPF: %v", err)
	}
	s := string(out)
	for _, want := range []string{
		`<meta property="embookshelf:tag">jazz-age</meta>`,
		`<meta property="embookshelf:tag">tragedy</meta>`,
		`<meta property="embookshelf:genre">fiction</meta>`,
		`<meta property="embookshelf:genre">literary</meta>`,
		`<dc:subject>jazz-age</dc:subject>`,
		`<dc:subject>tragedy</dc:subject>`,
		`<dc:subject>fiction</dc:subject>`,
		`<dc:subject>literary</dc:subject>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q\n%s", want, s)
		}
	}
}

func TestRezipEPUB_PreservesNonTouchedEntries(t *testing.T) {
	original := makeMinimalEPUB(t)
	src := newBytesSource(original)
	defer func() { _ = src.Close() }()

	zr, err := zip.NewReader(bytes.NewReader(original), int64(len(original)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	opfPath := "OEBPS/content.opf"
	newOPF := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Rewritten</dc:title></metadata>
  <manifest><item id="ch1" href="chapter1.xhtml" media-type="application/xhtml+xml"/><item id="cover-img" href="cover.jpg" media-type="image/jpeg" properties="cover-image"/></manifest>
  <spine><itemref idref="ch1"/></spine>
</package>`)

	out, err := rezipEPUB(zr, opfPath, newOPF, "", nil, "")
	if err != nil {
		t.Fatalf("rezipEPUB: %v", err)
	}

	zr2, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("re-open zip: %v", err)
	}

	if zr2.File[0].Name != "mimetype" {
		t.Errorf("entry[0]=%q want mimetype", zr2.File[0].Name)
	}
	if zr2.File[0].Method != zip.Store {
		t.Errorf("mimetype method=%v want Store", zr2.File[0].Method)
	}

	chapterBytes, err := readZipFile(zr2, "OEBPS/chapter1.xhtml")
	if err != nil {
		t.Fatalf("read chapter: %v", err)
	}
	if !bytes.Contains(chapterBytes, []byte("Hello.")) {
		t.Error("chapter contents lost in rezip")
	}

	opfBytes, err := readZipFile(zr2, opfPath)
	if err != nil {
		t.Fatalf("read opf: %v", err)
	}
	if !bytes.Contains(opfBytes, []byte("Rewritten")) {
		t.Error("OPF not rewritten")
	}

	coverBytes, err := readZipFile(zr2, "OEBPS/cover.jpg")
	if err != nil {
		t.Fatalf("read cover: %v", err)
	}
	if !bytes.Contains(coverBytes, []byte("ORIGINAL_COVER_BYTES")) {
		t.Error("cover bytes changed when not requested")
	}
}

func TestEPUBEmbedder_Embed_RoundTrip(t *testing.T) {
	original := makeMinimalEPUB(t)
	src := newBytesSource(original)
	defer func() { _ = src.Close() }()

	in := EmbedInput{
		EditableMetadata: model.EditableMetadata{
			Title:    "Curated Title",
			Author:   "Curated Author",
			Language: "es",
			Tags:     []string{"alpha", "beta"},
		},
	}
	out, err := EPUBEmbedder{}.Embed(context.Background(), src, in)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	src2 := newBytesSource(out)
	defer func() { _ = src2.Close() }()
	m, err := EPUBProcessor{}.Extract(context.Background(), src2)
	if err != nil {
		t.Fatalf("Extract after Embed: %v", err)
	}
	if m.Title != "Curated Title" {
		t.Errorf("Title=%q want Curated Title", m.Title)
	}
	if m.Author != "Curated Author" {
		t.Errorf("Author=%q want Curated Author", m.Author)
	}
	if m.Language != "es" {
		t.Errorf("Language=%q want es", m.Language)
	}
}

func TestEPUBEmbedder_Embed_CoverReplaced(t *testing.T) {
	original := makeMinimalEPUB(t)
	src := newBytesSource(original)
	defer func() { _ = src.Close() }()

	newCover := []byte("\xff\xd8\xff\xe0NEW_COVER_BYTES_PATTERN")
	in := EmbedInput{
		EditableMetadata: model.EditableMetadata{Title: "X"},
		CoverBytes:       newCover,
		CoverMime:        "image/jpeg",
	}
	out, err := EPUBEmbedder{}.Embed(context.Background(), src, in)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	src2 := newBytesSource(out)
	defer func() { _ = src2.Close() }()
	m, err := EPUBProcessor{}.Extract(context.Background(), src2)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !bytes.Contains(m.CoverBytes, []byte("NEW_COVER_BYTES_PATTERN")) {
		t.Errorf("cover not replaced; got=%q", m.CoverBytes)
	}
}
