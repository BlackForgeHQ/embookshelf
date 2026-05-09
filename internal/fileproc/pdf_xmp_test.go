// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"bytes"
	"context"
	"testing"
)

// xmpPacket is built so the UTF-8 BOM (\xef\xbb\xbf) sits between the begin
// quotes — exactly how Adobe-style XMP packets are wrapped in real PDFs.
// We construct it via concatenation because Go source files can't contain a
// raw BOM mid-file.
var xmpPacket = "<?xpacket begin=\"\xef\xbb\xbf\" id=\"W5M0MpCehiHzreSzNTczkc9d\"?>\n" +
	`<x:xmpmeta xmlns:x="adobe:ns:meta/">
 <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
  <rdf:Description rdf:about=""
    xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title><rdf:Alt><rdf:li xml:lang="x-default">XMP Title</rdf:li></rdf:Alt></dc:title>
    <dc:creator><rdf:Seq><rdf:li>Alice</rdf:li><rdf:li>Bob</rdf:li></rdf:Seq></dc:creator>
    <dc:description><rdf:Alt><rdf:li xml:lang="x-default">desc here</rdf:li></rdf:Alt></dc:description>
    <dc:language><rdf:Bag><rdf:li>en</rdf:li></rdf:Bag></dc:language>
  </rdf:Description>
 </rdf:RDF>
</x:xmpmeta>
<?xpacket end="r"?>`

func TestExtractXMPPacket_FindsBetweenMarkers(t *testing.T) {
	blob := []byte("%PDF-1.4\n... noise ...\n" + xmpPacket + "\n... more noise ...\n%%EOF")
	got, ok := extractXMPPacket(blob)
	if !ok {
		t.Fatal("xpacket not found")
	}
	if !bytes.Contains(got, []byte("dc:title")) {
		t.Fatalf("packet payload missing dc:title")
	}
}

func TestExtractXMPPacket_NoneFound(t *testing.T) {
	if _, ok := extractXMPPacket([]byte("plain bytes")); ok {
		t.Fatal("expected miss")
	}
}

func TestParseXMP_DublinCore(t *testing.T) {
	x, err := parseXMP([]byte(xmpPacket))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if x.Title != "XMP Title" {
		t.Fatalf("Title=%q", x.Title)
	}
	want := []string{"Alice", "Bob"}
	if len(x.Creators) != 2 || x.Creators[0] != want[0] || x.Creators[1] != want[1] {
		t.Fatalf("Creators=%v want %v", x.Creators, want)
	}
	if x.Description != "desc here" {
		t.Fatalf("Description=%q", x.Description)
	}
	if x.Language != "en" {
		t.Fatalf("Language=%q", x.Language)
	}
}

func TestParseXMP_IdentifierBagISBN(t *testing.T) {
	pkt := `<x:xmpmeta xmlns:x="adobe:ns:meta/">
  <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
    <rdf:Description rdf:about="" xmlns:xmp="http://ns.adobe.com/xap/1.0/">
      <xmp:Identifier>
        <rdf:Bag>
          <rdf:li scheme="isbn">978-0-441-17271-9</rdf:li>
          <rdf:li scheme="amazon">B00001</rdf:li>
        </rdf:Bag>
      </xmp:Identifier>
    </rdf:Description>
  </rdf:RDF>
</x:xmpmeta>`
	x, err := parseXMP([]byte(pkt))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if x.ISBN != "978-0-441-17271-9" {
		t.Fatalf("ISBN=%q", x.ISBN)
	}
}

func TestCleanAndValidateISBN_13Digit(t *testing.T) {
	got := cleanAndValidateISBN("978-0-441-17271-9")
	if got != "9780441172719" {
		t.Fatalf("got %q want %q", got, "9780441172719")
	}
}

func TestCleanAndValidateISBN_10WithX(t *testing.T) {
	got := cleanAndValidateISBN("0-441-17271-X")
	if got != "044117271X" {
		t.Fatalf("got %q", got)
	}
}

func TestCleanAndValidateISBN_StripsURNPrefix(t *testing.T) {
	got := cleanAndValidateISBN("urn:isbn:9780441172719")
	if got != "9780441172719" {
		t.Fatalf("got %q", got)
	}
}

func TestCleanAndValidateISBN_RejectsShort(t *testing.T) {
	if got := cleanAndValidateISBN("12345"); got != "" {
		t.Fatalf("got %q want empty", got)
	}
}

func TestCleanAndValidateISBN_RejectsLong(t *testing.T) {
	if got := cleanAndValidateISBN("12345678901234"); got != "" {
		t.Fatalf("got %q want empty", got)
	}
}

func TestParseXMP_IdentifierBagURNNoSchemeISBN(t *testing.T) {
	pkt := `<x:xmpmeta xmlns:x="adobe:ns:meta/">
  <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
    <rdf:Description rdf:about="" xmlns:xmp="http://ns.adobe.com/xap/1.0/">
      <xmp:Identifier>
        <rdf:Bag>
          <rdf:li>urn:isbn:9780441172719</rdf:li>
          <rdf:li>not-an-isbn</rdf:li>
        </rdf:Bag>
      </xmp:Identifier>
    </rdf:Description>
  </rdf:RDF>
</x:xmpmeta>`
	x, err := parseXMP([]byte(pkt))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if x.ISBN != "urn:isbn:9780441172719" {
		t.Fatalf("ISBN=%q", x.ISBN)
	}
}

func TestParseXMP_IdentifierBagSkipsNonISBNNoScheme(t *testing.T) {
	pkt := `<x:xmpmeta xmlns:x="adobe:ns:meta/">
  <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
    <rdf:Description rdf:about="" xmlns:xmp="http://ns.adobe.com/xap/1.0/">
      <xmp:Identifier>
        <rdf:Bag>
          <rdf:li>not-an-isbn</rdf:li>
          <rdf:li>also-not</rdf:li>
        </rdf:Bag>
      </xmp:Identifier>
    </rdf:Description>
  </rdf:RDF>
</x:xmpmeta>`
	x, err := parseXMP([]byte(pkt))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if x.ISBN != "" {
		t.Fatalf("ISBN=%q want empty", x.ISBN)
	}
}

func TestPDFProcessor_XMPOverridesDocInfoAndExtractsISBN(t *testing.T) {
	body := []byte("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n" +
		"1 0 obj\n<< /Title (DocInfo Title) /Author (DocInfo Author) >>\nendobj\n")
	body = append(body, []byte(xmpPacket)...)
	body = append(body, []byte("\ntrailer << /Info 1 0 R >>\n%%EOF\n")...)
	src := memSourceFromBytes(body)

	m, err := (PDFProcessor{}).Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if m.Title != "XMP Title" {
		t.Fatalf("Title=%q (XMP must override DocInfo)", m.Title)
	}
	if m.Author != "Alice, Bob" {
		t.Fatalf("Author=%q want %q", m.Author, "Alice, Bob")
	}
	if m.Description != "desc here" {
		t.Fatalf("Description=%q", m.Description)
	}
	if m.Language != "en" {
		t.Fatalf("Language=%q", m.Language)
	}
}

func TestPDFProcessor_AuthorSplitFromDocInfo(t *testing.T) {
	raw := []byte("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n" +
		"1 0 obj\n<< /Author (Smith, J. & Doe, A.) >>\nendobj\n" +
		"trailer << /Info 1 0 R >>\n%%EOF\n")
	src := memSourceFromBytes(raw)
	m, err := (PDFProcessor{}).Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if m.Author != "Smith, J., Doe, A." {
		t.Fatalf("Author=%q (split [,&], trim, rejoin with ', ')", m.Author)
	}
}

func TestPDFProcessor_XMPISBNFromIdentifierBag(t *testing.T) {
	wrappedBody := []byte("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n1 0 obj\n<< >>\nendobj\n")
	wrappedBody = append(wrappedBody, []byte(`<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>`+"\n")...)
	wrappedBody = append(wrappedBody, []byte(`<x:xmpmeta xmlns:x="adobe:ns:meta/">
  <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
    <rdf:Description rdf:about="" xmlns:xmp="http://ns.adobe.com/xap/1.0/">
      <xmp:Identifier>
        <rdf:Bag>
          <rdf:li scheme="isbn">978-0-441-17271-9</rdf:li>
        </rdf:Bag>
      </xmp:Identifier>
    </rdf:Description>
  </rdf:RDF>
</x:xmpmeta>`)...)
	wrappedBody = append(wrappedBody, []byte("\n<?xpacket end=\"r\"?>\ntrailer << /Info 1 0 R >>\n%%EOF\n")...)

	src := memSourceFromBytes(wrappedBody)
	m, err := (PDFProcessor{}).Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if m.ISBN != "9780441172719" {
		t.Fatalf("ISBN=%q want %q (must be cleanAndValidate'd)", m.ISBN, "9780441172719")
	}
}
