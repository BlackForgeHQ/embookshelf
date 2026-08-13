// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

// fb2Doc assembles a minimal FictionBook document around the given
// title-info body. Tests supply just the <title-info> contents so the
// envelope (namespace, binaries) stays in one place.
func fb2Doc(titleInfo, binaries string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	sb.WriteString(`<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:xlink="http://www.w3.org/1999/xlink">` + "\n")
	sb.WriteString(`<description><title-info>` + titleInfo + `</title-info></description>` + "\n")
	sb.WriteString(`<body><section><p>Chapter text.</p></section></body>` + "\n")
	sb.WriteString(binaries)
	sb.WriteString(`</FictionBook>`)
	return sb.String()
}

var fakeJPEG = []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F'}

func TestFB2Extract_BasicMetadataAndCover(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(fakeJPEG)
	titleInfo := `
		<genre>sf</genre>
		<genre>adventure</genre>
		<author>
			<first-name>Frank</first-name>
			<middle-name></middle-name>
			<last-name>Herbert</last-name>
		</author>
		<book-title>Dune</book-title>
		<annotation>
			<p>A desert planet.</p>
			<p>Spice must flow.</p>
		</annotation>
		<lang>en</lang>
		<coverpage><image xlink:href="#cover.jpg"/></coverpage>`
	binaries := `<binary id="cover.jpg" content-type="image/jpeg">` + b64 + `</binary>`
	src := memSourceFromBytes([]byte(fb2Doc(titleInfo, binaries)))
	defer func() { _ = src.Close() }()

	meta, err := FB2Processor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if meta.Format != "FB2" {
		t.Errorf("Format = %q, want FB2", meta.Format)
	}
	if meta.Title != "Dune" {
		t.Errorf("Title = %q, want Dune", meta.Title)
	}
	if meta.Author != "Frank Herbert" {
		t.Errorf("Author = %q, want %q", meta.Author, "Frank Herbert")
	}
	if !strings.Contains(meta.Description, "A desert planet.") ||
		!strings.Contains(meta.Description, "Spice must flow.") {
		t.Errorf("Description = %q, want the annotation paragraphs", meta.Description)
	}
	// Genres are parsed (so their presence doesn't fail extraction) but
	// intentionally not persisted anywhere — there is no genre/tags slot
	// on the BookDrop path for any format, and Description carries the
	// annotation only.
	if strings.Contains(meta.Description, "sf") || strings.Contains(meta.Description, "Genres") {
		t.Errorf("Description = %q, genres must not be folded in", meta.Description)
	}
	if meta.Language != "en" {
		t.Errorf("Language = %q, want en", meta.Language)
	}
	if !meta.HasCover {
		t.Fatal("expected HasCover")
	}
	if string(meta.CoverBytes) != string(fakeJPEG) {
		t.Errorf("CoverBytes = %x, want %x", meta.CoverBytes, fakeJPEG)
	}
	if meta.CoverMime != "image/jpeg" {
		t.Errorf("CoverMime = %q, want image/jpeg", meta.CoverMime)
	}
}

// Author name-part assembly: first + last only (no middle), and the
// nickname fallback when no name parts are present at all.
func TestFB2Extract_AuthorNameAssembly(t *testing.T) {
	cases := []struct {
		name       string
		authorXML  string
		wantAuthor string
	}{
		{
			name:       "first and last, no middle",
			authorXML:  `<author><first-name>Ursula</first-name><last-name>Le Guin</last-name></author>`,
			wantAuthor: "Ursula Le Guin",
		},
		{
			name:       "first middle last",
			authorXML:  `<author><first-name>J.</first-name><middle-name>R. R.</middle-name><last-name>Tolkien</last-name></author>`,
			wantAuthor: "J. R. R. Tolkien",
		},
		{
			name:       "nickname only",
			authorXML:  `<author><nickname>Boz</nickname></author>`,
			wantAuthor: "Boz",
		},
		{
			name:       "first author wins when several are present",
			authorXML:  `<author><first-name>First</first-name><last-name>Author</last-name></author><author><first-name>Second</first-name><last-name>Author</last-name></author>`,
			wantAuthor: "First Author",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			titleInfo := c.authorXML + `<book-title>T</book-title>`
			src := memSourceFromBytes([]byte(fb2Doc(titleInfo, "")))
			defer func() { _ = src.Close() }()
			meta, err := FB2Processor{}.Extract(context.Background(), src)
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if meta.Author != c.wantAuthor {
				t.Errorf("Author = %q, want %q", meta.Author, c.wantAuthor)
			}
		})
	}
}

// No coverpage at all is a normal, cover-less book — not an error.
func TestFB2Extract_NoCoverpageNoCover(t *testing.T) {
	titleInfo := `<author><first-name>A</first-name><last-name>B</last-name></author><book-title>No Cover</book-title>`
	src := memSourceFromBytes([]byte(fb2Doc(titleInfo, "")))
	defer func() { _ = src.Close() }()

	meta, err := FB2Processor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if meta.HasCover {
		t.Error("expected no cover")
	}
	if meta.Title != "No Cover" {
		t.Errorf("Title = %q, want %q", meta.Title, "No Cover")
	}
}

// --- malformed input: clear errors, never a panic ------------------------

func TestFB2Extract_TruncatedFileFails(t *testing.T) {
	src := memSourceFromBytes([]byte(`<?xml version="1.0"?><FictionBook><description><title-info><book-tit`))
	defer func() { _ = src.Close() }()

	if _, err := (FB2Processor{}).Extract(context.Background(), src); err == nil {
		t.Fatal("expected an error for truncated XML")
	}
}

// A file over the processing cap fails with a clear error rather than
// being read into memory in full — the filesystem-watcher intake path
// has no upload-size cap of its own, unlike the HTTP path's
// http.MaxBytesReader, so this processor has to refuse on its own.
func TestFB2Extract_OverCapFails(t *testing.T) {
	raw := make([]byte, fb2MaxBytes+1)
	for i := range raw {
		raw[i] = 'A'
	}
	src := memSourceFromBytes(raw)
	defer func() { _ = src.Close() }()

	_, err := (FB2Processor{}).Extract(context.Background(), src)
	if err == nil {
		t.Fatal("expected an error for a file over the processing cap")
	}
	if !strings.Contains(err.Error(), "cap") {
		t.Errorf("err = %q, want a message naming the cap", err)
	}
}

func TestFB2Extract_NotXMLAtAllFails(t *testing.T) {
	src := memSourceFromBytes([]byte("this is not xml at all"))
	defer func() { _ = src.Close() }()

	if _, err := (FB2Processor{}).Extract(context.Background(), src); err == nil {
		t.Fatal("expected an error for non-XML input")
	}
}

// title-info is where every field this processor cares about lives; a
// document without one has nothing to ingest.
func TestFB2Extract_NoTitleInfoFails(t *testing.T) {
	doc := `<?xml version="1.0"?><FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">` +
		`<description></description><body><section><p>Text</p></section></body></FictionBook>`
	src := memSourceFromBytes([]byte(doc))
	defer func() { _ = src.Close() }()

	if _, err := (FB2Processor{}).Extract(context.Background(), src); err == nil {
		t.Fatal("expected an error for a document with no title-info")
	}
}

// Real FB2 producers wrap the base64 body at a fixed column — the
// common case in the wild, not the tidy single-line block the other
// tests use. StdEncoding rejects embedded newlines outright, so this
// pins that the processor strips them before decoding.
func TestFB2Extract_WrappedBase64Cover(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(fakeJPEG)
	var wrapped strings.Builder
	for i := 0; i < len(b64); i += 4 {
		end := i + 4
		if end > len(b64) {
			end = len(b64)
		}
		wrapped.WriteString(b64[i:end])
		wrapped.WriteString("\n")
	}
	titleInfo := `<book-title>T</book-title><coverpage><image xlink:href="#cover.jpg"/></coverpage>`
	binaries := `<binary id="cover.jpg" content-type="image/jpeg">` + "\n" + wrapped.String() + `</binary>`
	src := memSourceFromBytes([]byte(fb2Doc(titleInfo, binaries)))
	defer func() { _ = src.Close() }()

	meta, err := FB2Processor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !meta.HasCover {
		t.Fatal("expected HasCover from wrapped base64")
	}
	if string(meta.CoverBytes) != string(fakeJPEG) {
		t.Errorf("CoverBytes = %x, want %x", meta.CoverBytes, fakeJPEG)
	}
}

// A coverpage pointing at binary data that isn't valid base64 degrades to
// no cover rather than failing the whole extraction or panicking — the
// same best-effort contract EPUB's cover lookup has.
func TestFB2Extract_InvalidBase64CoverIsBestEffort(t *testing.T) {
	titleInfo := `<book-title>T</book-title><coverpage><image xlink:href="#cover.jpg"/></coverpage>`
	binaries := `<binary id="cover.jpg" content-type="image/jpeg">not-valid-base64!!!</binary>`
	src := memSourceFromBytes([]byte(fb2Doc(titleInfo, binaries)))
	defer func() { _ = src.Close() }()

	meta, err := FB2Processor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v, want best-effort success", err)
	}
	if meta.HasCover {
		t.Error("expected no cover from invalid base64")
	}
	if meta.Title != "T" {
		t.Errorf("Title = %q, want T — metadata survives a bad cover", meta.Title)
	}
}

// A coverpage referencing a binary id that isn't in the document degrades
// to no cover the same way — the id is attacker/tool-controlled data, not
// something the whole extraction should die on.
func TestFB2Extract_MissingBinaryIDIsBestEffort(t *testing.T) {
	titleInfo := `<book-title>T</book-title><coverpage><image xlink:href="#does-not-exist.jpg"/></coverpage>`
	src := memSourceFromBytes([]byte(fb2Doc(titleInfo, "")))
	defer func() { _ = src.Close() }()

	meta, err := FB2Processor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v, want best-effort success", err)
	}
	if meta.HasCover {
		t.Error("expected no cover for a missing binary id")
	}
}

// --- charset decoding ------------------------------------------------------

// A document that declares windows-1251 and is genuinely encoded in it —
// not just labeled — must decode to correct UTF-8. windows-1251 is FB2's
// dominant wild encoding (a Russian-origin format), and encoding/xml only
// understands UTF-8/US-ASCII on its own; without a CharsetReader this fails
// outright with "encoding \"windows-1251\" declared but Decoder.CharsetReader
// is nil".
func TestFB2Extract_Windows1251Charset(t *testing.T) {
	const wantTitle = "Дюна"
	const wantFirst = "Фрэнк"
	const wantLast = "Герберт"

	utf8Doc := `<?xml version="1.0" encoding="windows-1251"?>` + "\n" +
		`<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">` +
		`<description><title-info>` +
		`<book-title>` + wantTitle + `</book-title>` +
		`<author><first-name>` + wantFirst + `</first-name><last-name>` + wantLast + `</last-name></author>` +
		`</title-info></description>` +
		`<body><section><p>Текст на русском языке.</p></section></body>` +
		`</FictionBook>`

	encoded, err := charmap.Windows1251.NewEncoder().String(utf8Doc)
	if err != nil {
		t.Fatalf("encode fixture as windows-1251: %v", err)
	}
	// encoded is a Go string whose bytes ARE the windows-1251 byte
	// stream (not valid UTF-8) — []byte(encoded) recovers those raw
	// bytes untouched, which is what a genuinely windows-1251-encoded
	// file on disk looks like.
	src := memSourceFromBytes([]byte(encoded))
	defer func() { _ = src.Close() }()

	meta, err := FB2Processor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if meta.Title != wantTitle {
		t.Errorf("Title = %q, want %q", meta.Title, wantTitle)
	}
	if want := wantFirst + " " + wantLast; meta.Author != want {
		t.Errorf("Author = %q, want %q", meta.Author, want)
	}
}

// A declared encoding this processor (via htmlindex) doesn't recognize —
// garbage, not a real charset label — must fail cleanly, not panic.
func TestFB2Extract_UnknownDeclaredEncodingFails(t *testing.T) {
	doc := `<?xml version="1.0" encoding="totally-bogus-charset"?>` + "\n" +
		`<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">` +
		`<description><title-info><book-title>T</book-title></title-info></description>` +
		`<body><section><p>Text.</p></section></body></FictionBook>`
	src := memSourceFromBytes([]byte(doc))
	defer func() { _ = src.Close() }()

	_, err := (FB2Processor{}).Extract(context.Background(), src)
	if err == nil {
		t.Fatal("expected an error for an unrecognized declared encoding")
	}
}

// --- cover content-type sniffing --------------------------------------------

// A crafted <binary content-type="text/html"> whose bytes are HTML, not an
// image, must never be trusted: the declared attribute is served back
// verbatim as this cover's Content-Type by the handler, so trusting it is
// stored XSS via the cover URL. The bytes are sniffed and don't match any
// recognized image format, so the cover degrades to none.
func TestFB2Extract_CoverContentTypeIsSniffedNotTrusted(t *testing.T) {
	htmlPayload := []byte(`<script>alert(document.domain)</script>`)
	b64 := base64.StdEncoding.EncodeToString(htmlPayload)
	titleInfo := `<book-title>T</book-title><coverpage><image xlink:href="#cover.evil"/></coverpage>`
	binaries := `<binary id="cover.evil" content-type="text/html">` + b64 + `</binary>`
	src := memSourceFromBytes([]byte(fb2Doc(titleInfo, binaries)))
	defer func() { _ = src.Close() }()

	meta, err := FB2Processor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v, want best-effort success", err)
	}
	if meta.HasCover {
		t.Errorf("expected no cover for non-image bytes, got CoverMime=%q CoverBytes=%q", meta.CoverMime, meta.CoverBytes)
	}
}

// A binary whose declared content-type is wrong but harmless (image/png
// declared, real JPEG bytes) still keeps its cover — the sniffed type
// wins, not the declared one.
func TestFB2Extract_CoverContentTypeMismatchUsesSniffedType(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(fakeJPEG)
	titleInfo := `<book-title>T</book-title><coverpage><image xlink:href="#cover.jpg"/></coverpage>`
	binaries := `<binary id="cover.jpg" content-type="image/png">` + b64 + `</binary>`
	src := memSourceFromBytes([]byte(fb2Doc(titleInfo, binaries)))
	defer func() { _ = src.Close() }()

	meta, err := FB2Processor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !meta.HasCover {
		t.Fatal("expected HasCover")
	}
	if meta.CoverMime != "image/jpeg" {
		t.Errorf("CoverMime = %q, want image/jpeg (sniffed, not the declared image/png)", meta.CoverMime)
	}
	if string(meta.CoverBytes) != string(fakeJPEG) {
		t.Errorf("CoverBytes = %x, want %x", meta.CoverBytes, fakeJPEG)
	}
}
