package fileproc

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

// CBZProcessor extracts metadata and cover image from a CBZ comic archive.
//
// CBZ is a ZIP of page images, sorted by filename. Some scene-released
// archives also embed a `ComicInfo.xml` with series/issue/year/summary —
// when present we surface those into the regular metadata fields so the
// library UI shows useful info without manual enrichment.
//
// Cover = the first page after natural sort, OR a file matching `cover.*`
// at the archive root if present.
type CBZProcessor struct{}

type comicInfoXML struct {
	XMLName     xml.Name `xml:"ComicInfo"`
	Title       string   `xml:"Title"`
	Series      string   `xml:"Series"`
	Number      string   `xml:"Number"`
	Year        string   `xml:"Year"`
	Summary     string   `xml:"Summary"`
	Writer      string   `xml:"Writer"`
	Penciller   string   `xml:"Penciller"`
	Inker       string   `xml:"Inker"`
	Colorist    string   `xml:"Colorist"`
	LanguageISO string   `xml:"LanguageISO"`
	PageCount   string   `xml:"PageCount"`
}

func (CBZProcessor) Extract(ctx context.Context, filePath string) (Metadata, error) {
	_ = ctx

	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return Metadata{}, fmt.Errorf("open cbz: %w", err)
	}
	defer func() { _ = zr.Close() }()

	pages := comicPages(&zr.Reader)
	if len(pages) == 0 {
		return Metadata{}, fmt.Errorf("cbz contains no images")
	}

	m := Metadata{Format: "CBZ"}

	// ComicInfo.xml is optional. Match case-insensitively because some
	// authoring tools save as `comicinfo.xml`.
	for _, f := range zr.File {
		base := strings.ToLower(path.Base(f.Name))
		if base != "comicinfo.xml" {
			continue
		}
		if b, err := readZipFile(&zr.Reader, f.Name); err == nil {
			var info comicInfoXML
			if xml.Unmarshal(b, &info) == nil {
				applyComicInfo(&m, info)
			}
		}
		break
	}

	// Cover: prefer a top-level `cover.*` if present, otherwise first
	// page after natural sort.
	coverName := preferredCoverName(&zr.Reader)
	if coverName == "" {
		coverName = pages[0]
	}
	if b, err := readZipFile(&zr.Reader, coverName); err == nil {
		m.HasCover = true
		m.CoverBytes = b
		m.CoverMime = mimeFromExt(path.Ext(coverName))
	}

	return m, nil
}

func applyComicInfo(m *Metadata, info comicInfoXML) {
	title := strings.TrimSpace(info.Title)
	series := strings.TrimSpace(info.Series)
	number := strings.TrimSpace(info.Number)
	switch {
	case title != "":
		m.Title = title
	case series != "" && number != "":
		m.Title = fmt.Sprintf("%s #%s", series, number)
	case series != "":
		m.Title = series
	}
	// "Writer" is the closest to "author" for comics; fall back to penciller.
	if w := strings.TrimSpace(info.Writer); w != "" {
		m.Author = w
	} else if p := strings.TrimSpace(info.Penciller); p != "" {
		m.Author = p
	}
	if s := strings.TrimSpace(info.Summary); s != "" {
		m.Description = s
	}
	if l := strings.TrimSpace(info.LanguageISO); l != "" {
		m.Language = l
	}
}

// comicPages returns a naturally-sorted list of image entry names inside
// the archive. ComicInfo.xml and other non-image entries are filtered out.
func comicPages(zr *zip.Reader) []string {
	var pages []string
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if !isImageExt(path.Ext(f.Name)) {
			continue
		}
		pages = append(pages, f.Name)
	}
	sort.Slice(pages, func(i, j int) bool {
		return naturalLess(pages[i], pages[j])
	})
	return pages
}

// preferredCoverName returns the archive entry name for a top-level
// `cover.{jpg,jpeg,png,webp}` file, if present. Empty string otherwise.
func preferredCoverName(zr *zip.Reader) string {
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		base := strings.ToLower(path.Base(f.Name))
		// Only a top-level file (no directory component) qualifies; we
		// don't want to grab a chapter-internal "cover.jpg".
		if path.Dir(f.Name) != "." && path.Dir(f.Name) != "" {
			continue
		}
		switch base {
		case "cover.jpg", "cover.jpeg", "cover.png", "cover.webp":
			return f.Name
		}
	}
	return ""
}

func isImageExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
		return true
	}
	return false
}

// naturalLess compares two strings such that embedded numeric runs sort
// numerically (so "page2.jpg" < "page10.jpg"). Falls back to byte order
// outside of digit runs.
func naturalLess(a, b string) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		ai, aj := a[i], b[j]
		if isDigit(ai) && isDigit(aj) {
			// Walk both digit runs and compare as integers (without
			// converting — leading-zero-safe).
			is, ie := i, i
			for ie < len(a) && isDigit(a[ie]) {
				ie++
			}
			js, je := j, j
			for je < len(b) && isDigit(b[je]) {
				je++
			}
			ar := stripLeadingZeros(a[is:ie])
			br := stripLeadingZeros(b[js:je])
			if len(ar) != len(br) {
				return len(ar) < len(br)
			}
			if ar != br {
				return ar < br
			}
			i, j = ie, je
			continue
		}
		if ai != aj {
			return ai < aj
		}
		i++
		j++
	}
	return len(a) < len(b)
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func stripLeadingZeros(s string) string {
	for i := 0; i < len(s)-1; i++ {
		if s[i] != '0' {
			return s[i:]
		}
	}
	if len(s) > 0 && s[len(s)-1] == '0' && len(s) > 1 {
		return "0"
	}
	return s
}

// CBZPages opens the archive at filePath and returns the page entry names
// in natural sort order. Used by the page-streaming handler so the reader
// can resolve "page n" to a real archive entry without re-listing on every
// request (callers are expected to cache the slice).
func CBZPages(filePath string) ([]string, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, fmt.Errorf("open cbz: %w", err)
	}
	defer func() { _ = zr.Close() }()
	return comicPages(&zr.Reader), nil
}

// CBZPage opens the archive at filePath and copies the n-th page (0-indexed,
// natural sort order) into w. Returns the resolved MIME type so the caller
// can set the response Content-Type. Returns os.ErrNotExist (wrapped) when
// n is out of range.
func CBZPage(filePath string, n int, w io.Writer) (mime string, err error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return "", fmt.Errorf("open cbz: %w", err)
	}
	defer func() { _ = zr.Close() }()

	pages := comicPages(&zr.Reader)
	if n < 0 || n >= len(pages) {
		return "", fmt.Errorf("page %d out of range (0..%d)", n, len(pages)-1)
	}
	name := pages[n]
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("open page: %w", err)
		}
		defer func() { _ = rc.Close() }()
		if _, err := io.Copy(w, rc); err != nil {
			return "", fmt.Errorf("stream page: %w", err)
		}
		return mimeFromExt(path.Ext(name)), nil
	}
	return "", fmt.Errorf("page %d entry %q vanished", n, name)
}
