// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/blackforge/embookshelf/internal/storage"
)

// EPUBProcessor parses EPUB 2/3 files to pull title, author, description,
// language, and (when present) the cover image bytes.
//
// The EPUB format is a ZIP archive containing:
//   - META-INF/container.xml, which points at a rootfile (OPF).
//   - The OPF, which contains a <metadata> block (Dublin Core) and a
//     <manifest> listing every resource. EPUB 3 marks the cover with
//     <item properties="cover-image">; EPUB 2 uses <meta name="cover" content="<id>"/>.
//
// We parse only what we need — the reader side does the actual rendering in
// epub.js on the client.
type EPUBProcessor struct{}

type containerXML struct {
	Rootfiles []struct {
		FullPath  string `xml:"full-path,attr"`
		MediaType string `xml:"media-type,attr"`
	} `xml:"rootfiles>rootfile"`
}

type opfPackage struct {
	XMLName  xml.Name `xml:"package"`
	Metadata opfMeta  `xml:"metadata"`
	Manifest opfMani  `xml:"manifest"`
}

type opfMeta struct {
	Title       []string     `xml:"title"`
	Creator     []opfCreator `xml:"creator"`
	Description []string     `xml:"description"`
	Language    []string     `xml:"language"`
	Meta        []opfMetaKV  `xml:"meta"`
}

type opfCreator struct {
	Role string `xml:"role,attr"`
	Name string `xml:",chardata"`
}

type opfMetaKV struct {
	Name       string `xml:"name,attr"`
	Content    string `xml:"content,attr"`
	Properties string `xml:"property,attr"`
	Value      string `xml:",chardata"`
}

type opfMani struct {
	Items []opfItem `xml:"item"`
}

type opfItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	MediaType  string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
}

func (EPUBProcessor) Extract(ctx context.Context, src storage.Source) (Metadata, error) {
	_ = ctx

	zr, err := zip.NewReader(src, src.Size())
	if err != nil {
		return Metadata{}, fmt.Errorf("open epub: %w", err)
	}
	// zr is *zip.Reader (not *zip.ReadCloser); no Close needed.
	// The caller is responsible for closing the Source.

	opfPath, err := rootfilePath(zr)
	if err != nil {
		return Metadata{}, err
	}
	opfBytes, err := readZipFile(zr, opfPath)
	if err != nil {
		return Metadata{}, fmt.Errorf("read opf: %w", err)
	}
	var pkg opfPackage
	if err := xml.Unmarshal(opfBytes, &pkg); err != nil {
		return Metadata{}, fmt.Errorf("parse opf: %w", err)
	}

	m := Metadata{Format: "EPUB"}
	if len(pkg.Metadata.Title) > 0 {
		m.Title = strings.TrimSpace(pkg.Metadata.Title[0])
	}
	if len(pkg.Metadata.Creator) > 0 {
		m.Author = strings.TrimSpace(pkg.Metadata.Creator[0].Name)
	}
	if len(pkg.Metadata.Description) > 0 {
		m.Description = strings.TrimSpace(pkg.Metadata.Description[0])
	}
	if len(pkg.Metadata.Language) > 0 {
		m.Language = strings.TrimSpace(pkg.Metadata.Language[0])
	}

	// Cover extraction is best-effort: a malformed or missing cover never
	// fails the whole extraction.
	if href, mime := findCover(pkg); href != "" {
		if bytes, mt, err := readCover(zr, opfPath, href, mime); err == nil {
			m.HasCover = true
			m.CoverBytes = bytes
			m.CoverMime = mt
		}
	}

	return m, nil
}

// findCover returns the href (relative to the OPF) and declared MIME type of
// the cover image. Either may be empty.
func findCover(pkg opfPackage) (href, mime string) {
	// EPUB 3: item with properties="cover-image".
	for _, it := range pkg.Manifest.Items {
		if strings.Contains(it.Properties, "cover-image") {
			return it.Href, it.MediaType
		}
	}
	// EPUB 2: <meta name="cover" content="<id>"/> pointing at a manifest item.
	var coverID string
	for _, m := range pkg.Metadata.Meta {
		if m.Name == "cover" {
			coverID = m.Content
			break
		}
	}
	if coverID != "" {
		for _, it := range pkg.Manifest.Items {
			if it.ID == coverID && strings.HasPrefix(it.MediaType, "image/") {
				return it.Href, it.MediaType
			}
		}
	}
	return "", ""
}

// readCover resolves href against the OPF's directory and reads the image
// bytes out of the archive. MIME type falls back to an extension guess if
// the manifest didn't declare one.
func readCover(zr *zip.Reader, opfPath, href, declaredMime string) ([]byte, string, error) {
	opfDir := path.Dir(opfPath)
	entry := path.Clean(path.Join(opfDir, href))
	bytes, err := readZipFile(zr, entry)
	if err != nil {
		return nil, "", err
	}
	mime := strings.TrimSpace(declaredMime)
	if mime == "" {
		mime = mimeFromExt(path.Ext(entry))
	}
	return bytes, mime, nil
}

// mimeFromExt covers the image formats EPUBs actually ship with.
func mimeFromExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	}
	return "application/octet-stream"
}

func rootfilePath(zr *zip.Reader) (string, error) {
	b, err := readZipFile(zr, "META-INF/container.xml")
	if err != nil {
		return "", fmt.Errorf("read container.xml: %w", err)
	}
	var c containerXML
	if err := xml.Unmarshal(b, &c); err != nil {
		return "", fmt.Errorf("parse container.xml: %w", err)
	}
	for _, rf := range c.Rootfiles {
		if strings.HasSuffix(rf.FullPath, ".opf") {
			return rf.FullPath, nil
		}
	}
	return "", fmt.Errorf("no .opf rootfile found")
}

func readZipFile(zr *zip.Reader, name string) ([]byte, error) {
	name = path.Clean(name)
	for _, f := range zr.File {
		if path.Clean(f.Name) == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer func() { _ = rc.Close() }()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("%s not in archive", name)
}
