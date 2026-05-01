package fileproc

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// makeMinimalEPUB returns the bytes of a minimal but spec-compliant
// EPUB: mimetype (uncompressed first), META-INF/container.xml, an
// OPF rootfile, one chapter, and a cover JPEG.
//
// Used by every embed test as a starting point; the test mutates
// the returned bytes via the embedder and re-extracts to verify.
func makeMinimalEPUB(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// 1) mimetype — first, uncompressed (EPUB spec requirement).
	mh := &zip.FileHeader{Name: "mimetype", Method: zip.Store}
	w, err := zw.CreateHeader(mh)
	if err != nil {
		t.Fatalf("create mimetype: %v", err)
	}
	if _, err := w.Write([]byte("application/epub+zip")); err != nil {
		t.Fatalf("write mimetype: %v", err)
	}

	// 2) META-INF/container.xml — points at OEBPS/content.opf.
	addZip(t, zw, "META-INF/container.xml", []byte(`<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`))

	// 3) OEBPS/content.opf — minimal package. Title/Author present;
	// no Subtitle/Series/Tags so the embedder's add-path is exercised.
	addZip(t, zw, "OEBPS/content.opf", []byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>Original Title</dc:title>
    <dc:creator opf:role="aut">Original Author</dc:creator>
    <dc:identifier id="bookid">urn:uuid:00000000-0000-0000-0000-000000000001</dc:identifier>
    <dc:language>en</dc:language>
  </metadata>
  <manifest>
    <item id="ch1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>
    <item id="cover-img" href="cover.jpg" media-type="image/jpeg" properties="cover-image"/>
  </manifest>
  <spine>
    <itemref idref="ch1"/>
  </spine>
</package>`))

	// 4) OEBPS/chapter1.xhtml — token chapter so the EPUB validates loosely.
	addZip(t, zw, "OEBPS/chapter1.xhtml", []byte(`<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Ch 1</title></head><body><p>Hello.</p></body></html>`))

	// 5) OEBPS/cover.jpg — fake JPEG bytes. The embed tests will
	// replace these with a sentinel pattern to verify the swap.
	addZip(t, zw, "OEBPS/cover.jpg", []byte("\xff\xd8\xff\xe0ORIGINAL_COVER_BYTES"))

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func addZip(t *testing.T, zw *zip.Writer, name string, data []byte) {
	t.Helper()
	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestFixture_Generate writes the minimal EPUB to disk so manual
// inspection is possible. Run:
//
//	EMBED_FIXTURE_UPDATE=1 go test ./internal/fileproc/ -run TestFixture_Generate
//
// to refresh testdata/minimal.epub on the filesystem. Skipped by
// default — all real tests build the fixture in-memory.
func TestFixture_Generate(t *testing.T) {
	if os.Getenv("EMBED_FIXTURE_UPDATE") == "" {
		t.Skip("set EMBED_FIXTURE_UPDATE=1 to refresh testdata/minimal.epub")
	}
	data := makeMinimalEPUB(t)
	dir := filepath.Join("testdata")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "minimal.epub"), data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}
