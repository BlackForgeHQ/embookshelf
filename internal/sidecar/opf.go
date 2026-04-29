package sidecar

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// opfPackage is the root <package> element of a metadata.opf file.
// We only consume the metadata block; manifest/spine are EPUB-only.
type opfPackage struct {
	XMLName  xml.Name    `xml:"package"`
	Metadata opfMetadata `xml:"metadata"`
}

type opfMetadata struct {
	Title       string       `xml:"title"`
	Creator     []opfCreator `xml:"creator"`
	Description string       `xml:"description"`
	Language    string       `xml:"language"`
	Publisher   string       `xml:"publisher"`
	Date        string       `xml:"date"`
	Identifier  []opfIdent   `xml:"identifier"`
	Subject     []string     `xml:"subject"`
	Meta        []opfMetaKV  `xml:"meta"`
}

type opfCreator struct {
	Role  string `xml:"role,attr"`
	Value string `xml:",chardata"`
}

type opfIdent struct {
	Scheme string `xml:"scheme,attr"`
	Value  string `xml:",chardata"`
}

type opfMetaKV struct {
	Name    string `xml:"name,attr"`
	Content string `xml:"content,attr"`
}

// ParseOPF deserializes an OPF metadata file (the standalone sibling
// kind, not the one embedded in an .epub) into a Sidecar.
func ParseOPF(data []byte) (Sidecar, error) {
	var pkg opfPackage
	if err := xml.Unmarshal(data, &pkg); err != nil {
		return Sidecar{}, fmt.Errorf("sidecar: parse opf: %w", err)
	}
	s := Sidecar{
		Title:         strings.TrimSpace(pkg.Metadata.Title),
		Description:   strings.TrimSpace(pkg.Metadata.Description),
		Language:      strings.TrimSpace(pkg.Metadata.Language),
		Publisher:     strings.TrimSpace(pkg.Metadata.Publisher),
		PublishedDate: strings.TrimSpace(pkg.Metadata.Date),
	}
	// Author = first creator with role="aut" or, failing that, the
	// first creator entry.
	for _, c := range pkg.Metadata.Creator {
		if c.Role == "aut" && c.Value != "" {
			s.Author = c.Value
			break
		}
	}
	if s.Author == "" {
		for _, c := range pkg.Metadata.Creator {
			if c.Value != "" {
				s.Author = c.Value
				break
			}
		}
	}
	// ISBN = first identifier whose scheme contains "isbn" (case-insensitive).
	// This matches "ISBN", "isbn", "ISBN-13", "isbn-10", etc.
	for _, i := range pkg.Metadata.Identifier {
		if strings.Contains(strings.ToLower(i.Scheme), "isbn") && i.Value != "" {
			s.ISBN = i.Value
			break
		}
	}
	// Tags = <subject> entries.
	if len(pkg.Metadata.Subject) > 0 {
		s.Tags = append(s.Tags, pkg.Metadata.Subject...)
	}
	// Calibre encodes series via <meta name="calibre:series" content="…"/>.
	for _, m := range pkg.Metadata.Meta {
		switch m.Name {
		case "calibre:series":
			s.Series = m.Content
		case "calibre:series_index":
			// Best-effort parse; silently drop on error.
			var idx int
			_, _ = fmt.Sscanf(m.Content, "%d", &idx)
			s.SeriesIndex = idx
		case "calibre:title_sort":
			s.TitleSort = m.Content
		}
	}
	return s, nil
}
