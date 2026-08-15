// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"github.com/blackforge/embookshelf/internal/storage"
)

// The cover's Content-Type is served back to the browser verbatim by the
// cover routes, so every declared type that reaches Metadata.CoverMime is
// attacker-controlled markup waiting for a <script>. These tests pin the
// two halves of the fix: each processor degrades a non-image cover to no
// cover, and ExtractBook re-derives the type from the bytes so a
// processor that forgets (or a new one that never knew) cannot leak a
// declared type into the database.

// --- EPUB 3 ----------------------------------------------------------------

// epubWithCover builds a minimal EPUB whose manifest declares a
// cover-image item with the given media-type, href, and bytes.
func epubWithCover(t *testing.T, mediaType, href string, coverBytes []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	mh := &zip.FileHeader{Name: "mimetype", Method: zip.Store}
	w, err := zw.CreateHeader(mh)
	if err != nil {
		t.Fatalf("create mimetype: %v", err)
	}
	if _, err := w.Write([]byte("application/epub+zip")); err != nil {
		t.Fatalf("write mimetype: %v", err)
	}

	addZip(t, zw, "META-INF/container.xml", []byte(`<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`))

	addZip(t, zw, "OEBPS/content.opf", []byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Crafted</dc:title>
    <dc:identifier id="bookid">urn:uuid:00000000-0000-0000-0000-000000000009</dc:identifier>
    <dc:language>en</dc:language>
  </metadata>
  <manifest>
    <item id="ch1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>
    <item id="cover-img" href="`+href+`" media-type="`+mediaType+`" properties="cover-image"/>
  </manifest>
  <spine><itemref idref="ch1"/></spine>
</package>`))

	addZip(t, zw, "OEBPS/chapter1.xhtml", []byte(`<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Ch 1</title></head><body><p>Hi.</p></body></html>`))
	addZip(t, zw, "OEBPS/"+href, coverBytes)

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// An EPUB 3 <item properties="cover-image"> whose media-type says
// "text/html" and whose bytes are HTML must not become a cover: the
// EPUB 3 branch of findCover used to return the declared type unchecked
// (only the EPUB 2 branch tested for an image/ prefix), which is stored
// XSS through the cover URL.
func TestEPUBExtract_CoverContentTypeIsSniffedNotTrusted(t *testing.T) {
	raw := epubWithCover(t, "text/html", "cover.xhtml",
		[]byte(`<script>alert(document.domain)</script>`))
	src := memSourceFromBytes(raw)
	defer func() { _ = src.Close() }()

	meta, err := EPUBProcessor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v, want best-effort success", err)
	}
	if meta.HasCover {
		t.Errorf("expected no cover for non-image bytes, got CoverMime=%q CoverBytes=%q",
			meta.CoverMime, meta.CoverBytes)
	}
	if meta.CoverMime != "" || meta.CoverBytes != nil {
		t.Errorf("cover fields not cleared: mime=%q bytes=%q", meta.CoverMime, meta.CoverBytes)
	}
}

// An SVG cover is refused at ingest even though EPUB may legitimately
// declare one and the bytes really are what the manifest says they are.
// SVG is a document that can carry script, and it is the one image type
// the nosniff header does not defuse — a cover served as image/svg+xml
// executes when a reader opens the cover URL directly. It is therefore
// outside the set SniffImageMime recognizes, and a book that ships one
// arrives cover-less rather than dangerous.
func TestEPUBExtract_SVGCoverIsRefused(t *testing.T) {
	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" width="1" height="1">` +
		`<script>alert(document.domain)</script></svg>`)
	raw := epubWithCover(t, "image/svg+xml", "cover.svg", svg)
	src := memSourceFromBytes(raw)
	defer func() { _ = src.Close() }()

	meta, err := EPUBProcessor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v, want best-effort success", err)
	}
	if meta.HasCover || meta.CoverMime != "" {
		t.Errorf("SVG cover survived: HasCover=%v CoverMime=%q", meta.HasCover, meta.CoverMime)
	}
}

// The same refusal at the seam, where it holds for every processor —
// including the comic containers, whose cover type comes from an entry
// name and so could have named an SVG the extension filter let through.
func TestExtractBook_RefusesSVGCover(t *testing.T) {
	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	raw := epubWithCover(t, "image/svg+xml", "cover.svg", svg)
	src := memSourceFromBytes(raw)
	defer func() { _ = src.Close() }()

	res, err := ExtractBook(context.Background(), nil, src, "EPUB", "svg-cover.epub")
	if err != nil {
		t.Fatalf("ExtractBook: %v", err)
	}
	if res.HasCover || res.CoverMime != "" || res.CoverBytes != nil {
		t.Errorf("SVG cover crossed the seam: HasCover=%v mime=%q bytes=%q",
			res.HasCover, res.CoverMime, res.CoverBytes)
	}
}

// A cover whose declared type is merely wrong (image/png declared, real
// JPEG bytes) keeps its cover — the sniffed type wins.
func TestEPUBExtract_CoverContentTypeMismatchUsesSniffedType(t *testing.T) {
	raw := epubWithCover(t, "image/png", "cover.png", fakeJPEG)
	src := memSourceFromBytes(raw)
	defer func() { _ = src.Close() }()

	meta, err := EPUBProcessor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !meta.HasCover {
		t.Fatal("expected HasCover")
	}
	if meta.CoverMime != "image/jpeg" {
		t.Errorf("CoverMime = %q, want image/jpeg (sniffed, not the declared image/png)", meta.CoverMime)
	}
	if !bytes.Equal(meta.CoverBytes, fakeJPEG) {
		t.Errorf("CoverBytes = %x, want %x", meta.CoverBytes, fakeJPEG)
	}
}

// --- audio (ID3v2 APIC) -----------------------------------------------------

// mp3WithAPIC builds a bare ID3v2.3 tag carrying a single APIC frame with
// the given declared MIME type and picture bytes. No audio frames follow —
// the tag reader never needs them, and duration parsing degrades to nil.
func mp3WithAPIC(t *testing.T, declaredMime string, pic []byte) []byte {
	t.Helper()

	// APIC payload: encoding(1) + mime\0 + picture type(1) + desc\0 + data.
	var frame bytes.Buffer
	frame.WriteByte(0x00) // ISO-8859-1
	frame.WriteString(declaredMime)
	frame.WriteByte(0x00)
	frame.WriteByte(0x03) // front cover
	frame.WriteByte(0x00) // empty description
	frame.Write(pic)

	// ID3v2.3 frame header: id(4) + size(4, plain big-endian) + flags(2).
	var body bytes.Buffer
	body.WriteString("APIC")
	_ = binary.Write(&body, binary.BigEndian, uint32(frame.Len()))
	body.Write([]byte{0x00, 0x00})
	body.Write(frame.Bytes())

	// A TIT2 so the tag carries a title too and the read is unambiguous.
	title := append([]byte{0x00}, []byte("Crafted")...)
	body.WriteString("TIT2")
	_ = binary.Write(&body, binary.BigEndian, uint32(len(title)))
	body.Write([]byte{0x00, 0x00})
	body.Write(title)

	size := body.Len()
	var out bytes.Buffer
	out.WriteString("ID3")
	out.Write([]byte{0x03, 0x00, 0x00}) // v2.3.0, no flags
	out.Write([]byte{                   // synchsafe size
		byte((size >> 21) & 0x7f),
		byte((size >> 14) & 0x7f),
		byte((size >> 7) & 0x7f),
		byte(size & 0x7f),
	})
	out.Write(body.Bytes())
	return out.Bytes()
}

// An ID3 APIC frame declaring "text/html" and carrying HTML must not
// become a cover: pic.MIMEType was copied into Metadata.CoverMime
// verbatim and served straight back as the cover's Content-Type.
func TestAudioExtract_CoverContentTypeIsSniffedNotTrusted(t *testing.T) {
	raw := mp3WithAPIC(t, "text/html", []byte(`<script>alert(document.domain)</script>`))
	src := memSourceFromBytes(raw)
	defer func() { _ = src.Close() }()

	meta, err := AudioProcessor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v, want best-effort success", err)
	}
	if meta.HasCover {
		t.Errorf("expected no cover for non-image bytes, got CoverMime=%q CoverBytes=%q",
			meta.CoverMime, meta.CoverBytes)
	}
}

// A picture frame that declares the wrong image type still yields a
// cover, typed from the bytes.
func TestAudioExtract_CoverContentTypeMismatchUsesSniffedType(t *testing.T) {
	raw := mp3WithAPIC(t, "image/png", fakeJPEG)
	src := memSourceFromBytes(raw)
	defer func() { _ = src.Close() }()

	meta, err := AudioProcessor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !meta.HasCover {
		t.Fatal("expected HasCover")
	}
	if meta.CoverMime != "image/jpeg" {
		t.Errorf("CoverMime = %q, want image/jpeg (sniffed, not the declared image/png)", meta.CoverMime)
	}
}

// An APIC frame with an empty MIME type and real JPEG bytes keeps
// working — the pre-fix code special-cased this with a hardcoded
// "image/jpeg" default, and the sniff has to reach the same answer.
func TestAudioExtract_CoverEmptyDeclaredMimeStillSniffs(t *testing.T) {
	raw := mp3WithAPIC(t, "", fakeJPEG)
	src := memSourceFromBytes(raw)
	defer func() { _ = src.Close() }()

	meta, err := AudioProcessor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !meta.HasCover || meta.CoverMime != "image/jpeg" {
		t.Errorf("HasCover=%v CoverMime=%q, want true/image/jpeg", meta.HasCover, meta.CoverMime)
	}
}

// --- comic containers -------------------------------------------------------

// A comic's cover type used to come from the archive entry's extension —
// a name whoever packed the archive chose. An entry called "cover.png"
// holding markup was persisted and served as image/png.
func TestComicExtract_CoverContentTypeIsSniffedNotTakenFromTheEntryName(t *testing.T) {
	raw := cbzWithEntry(t, "cover.png", []byte(`<script>alert(document.domain)</script>`))
	src := memSourceFromBytes(raw)
	defer func() { _ = src.Close() }()

	meta, err := ComicProcessor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v, want best-effort success", err)
	}
	if meta.HasCover || meta.CoverMime != "" {
		t.Errorf("expected no cover for non-image bytes, got CoverMime=%q CoverBytes=%q",
			meta.CoverMime, meta.CoverBytes)
	}
}

// --- the choke point --------------------------------------------------------

// ExtractBook is the one seam every processor's cover crosses on its way
// to the database, and it re-derives the type from the bytes rather than
// trusting whatever the processor put in Metadata.CoverMime.
//
// Driven through a stub processor rather than a real one on purpose:
// every processor in this package now sniffs at its own layer, so a test
// built on one of them would keep passing with the seam deleted. What is
// being pinned here is that a processor which does *not* sniff — a
// future one, or one that regresses — cannot get a declared type past
// this function.
func TestExtractBook_NormalizesWhateverAProcessorReturns(t *testing.T) {
	cases := []struct {
		name      string
		meta      Metadata
		wantCover bool
		wantMime  string
	}{
		{
			name: "markup declared as an image is dropped",
			meta: Metadata{
				HasCover:   true,
				CoverBytes: []byte(`<script>alert(document.domain)</script>`),
				CoverMime:  "image/png",
			},
		},
		{
			name: "a document type over a real image is re-typed",
			meta: Metadata{
				HasCover:   true,
				CoverBytes: fakeJPEG,
				CoverMime:  "text/html",
			},
			wantCover: true,
			wantMime:  "image/jpeg",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubProcessor(t, ".epub", tc.meta)

			src := memSourceFromBytes([]byte("the stub never reads this"))
			defer func() { _ = src.Close() }()

			res, err := ExtractBook(context.Background(), nil, src, "EPUB", "")
			if err != nil {
				t.Fatalf("ExtractBook: %v", err)
			}
			if res.HasCover != tc.wantCover || res.CoverMime != tc.wantMime {
				t.Errorf("HasCover=%v CoverMime=%q, want %v/%q",
					res.HasCover, res.CoverMime, tc.wantCover, tc.wantMime)
			}
			if !tc.wantCover && res.CoverBytes != nil {
				t.Errorf("CoverBytes not cleared: %q", res.CoverBytes)
			}
		})
	}
}

// coverStub is a Processor that answers with a fixed Metadata, standing
// in for one that types its cover from something the file's author wrote.
type coverStub struct{ meta Metadata }

func (p coverStub) Extract(context.Context, storage.Source) (Metadata, error) { return p.meta, nil }

// stubProcessor swaps the registry entry for ext for the length of the
// test. Safe because nothing in this package's tests runs in parallel.
func stubProcessor(t *testing.T, ext string, meta Metadata) {
	t.Helper()
	prev := processors[ext]
	processors[ext] = func() Processor { return coverStub{meta: meta} }
	t.Cleanup(func() { processors[ext] = prev })
}

// The same seam leaves a real cover alone, typed from its bytes.
func TestExtractBook_KeepsRealCoverWithSniffedType(t *testing.T) {
	raw := cbzWithEntry(t, "cover.png", fakeJPEG) // the extension lies; the bytes don't
	src := memSourceFromBytes(raw)
	defer func() { _ = src.Close() }()

	res, err := ExtractBook(context.Background(), nil, src, "CBZ", "ok.cbz")
	if err != nil {
		t.Fatalf("ExtractBook: %v", err)
	}
	if !res.HasCover {
		t.Fatal("expected HasCover")
	}
	if res.CoverMime != "image/jpeg" {
		t.Errorf("CoverMime = %q, want image/jpeg", res.CoverMime)
	}
}

// cbzWithEntry builds a one-page CBZ whose single entry has the given
// name and bytes.
func cbzWithEntry(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addZip(t, zw, name, data)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}
