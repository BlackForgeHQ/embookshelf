// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"bytes"
	"encoding/xml"
	"regexp"
	"strings"
)

var nonISBNChar = regexp.MustCompile(`[^0-9Xx]`)

// cleanAndValidateISBN strips separators (including `urn:isbn:` prefixes
// implicitly — non-`[0-9Xx]` chars), uppercases the trailing X, and
// returns the value only when it is exactly 10 or 13 chars after
// cleaning. Anything else returns "".
func cleanAndValidateISBN(s string) string {
	cleaned := nonISBNChar.ReplaceAllString(s, "")
	cleaned = strings.ToUpper(cleaned)
	if len(cleaned) != 10 && len(cleaned) != 13 {
		return ""
	}
	return cleaned
}

// XMPMetadata is the subset of XMP fields the PDF processor consumes.
// Title, Creators (in Seq order), Description, Language come from
// Dublin Core; ISBN comes from xmp:Identifier/rdf:Bag with a
// "scheme=isbn" attribute (case-insensitive substring match).
type XMPMetadata struct {
	Title       string
	Creators    []string
	Description string
	Language    string
	ISBN        string // raw value before clean+validate
}

// extractXMPPacket finds the first uncompressed XMP packet in raw bytes.
// XMP packets are wrapped in `<?xpacket begin=… ?>` / `<?xpacket end=…?>`
// markers — designed for raw scanning even inside binary containers.
// Returns the inner XML payload (between the markers) on success.
func extractXMPPacket(b []byte) ([]byte, bool) {
	beginMarker := []byte("<?xpacket begin=")
	endMarker := []byte("<?xpacket end=")
	bi := bytes.Index(b, beginMarker)
	if bi < 0 {
		return nil, false
	}
	headEnd := bytes.Index(b[bi:], []byte("?>"))
	if headEnd < 0 {
		return nil, false
	}
	payloadStart := bi + headEnd + 2
	if payloadStart >= len(b) {
		return nil, false
	}
	ei := bytes.Index(b[payloadStart:], endMarker)
	if ei < 0 {
		return nil, false
	}
	return b[payloadStart : payloadStart+ei], true
}

// xmpDoc mirrors the bits of <x:xmpmeta>/<rdf:RDF>/<rdf:Description>
// the extractor cares about. xml.Unmarshal matches by local name when
// no XMLNS is bound for that prefix on the struct tags, so dc:* and
// rdf:* prefixed elements all match by their local part.
type xmpDoc struct {
	XMLName     xml.Name         `xml:"xmpmeta"`
	Description []xmpDescription `xml:"RDF>Description"`
}

type xmpDescription struct {
	Title       xmpAlt   `xml:"title"`
	Creator     xmpSeq   `xml:"creator"`
	Description xmpAlt   `xml:"description"`
	Language    xmpBag   `xml:"language"`
	Identifier  xmpIdent `xml:"Identifier"`
}

type xmpAlt struct {
	Items []string `xml:"Alt>li"`
}

type xmpSeq struct {
	Items []string `xml:"Seq>li"`
}

type xmpBag struct {
	Items []string `xml:"Bag>li"`
}

type xmpIdent struct {
	Items []xmpIdentItem `xml:"Bag>li"`
}

type xmpIdentItem struct {
	Scheme string `xml:"scheme,attr"`
	Value  string `xml:",chardata"`
}

// parseXMP unmarshals an XMP packet payload into an XMPMetadata.
// Tolerant: missing fields are left zero. Unknown elements ignored.
func parseXMP(payload []byte) (XMPMetadata, error) {
	var doc xmpDoc
	if err := xml.Unmarshal(payload, &doc); err != nil {
		return XMPMetadata{}, err
	}
	var out XMPMetadata
	for _, d := range doc.Description {
		if out.Title == "" && len(d.Title.Items) > 0 {
			out.Title = strings.TrimSpace(d.Title.Items[0])
		}
		if out.Description == "" && len(d.Description.Items) > 0 {
			out.Description = strings.TrimSpace(d.Description.Items[0])
		}
		if out.Language == "" && len(d.Language.Items) > 0 {
			out.Language = strings.TrimSpace(d.Language.Items[0])
		}
		for _, c := range d.Creator.Items {
			if v := strings.TrimSpace(c); v != "" {
				out.Creators = append(out.Creators, v)
			}
		}
		if out.ISBN == "" {
			for _, ident := range d.Identifier.Items {
				// Explicit scheme match wins regardless of value shape.
				if strings.Contains(strings.ToLower(ident.Scheme), "isbn") {
					out.ISBN = strings.TrimSpace(ident.Value)
					break
				}
				// No scheme attribute: Calibre + ADE often emit
				// `urn:isbn:9780…` with the scheme implied. Accept the
				// value only when it cleans to a valid 10/13-char ISBN.
				if ident.Scheme == "" && cleanAndValidateISBN(ident.Value) != "" {
					out.ISBN = strings.TrimSpace(ident.Value)
					break
				}
			}
		}
	}
	return out, nil
}
