// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"sort"
	"strings"
)

// The comic pass, once, for the three containers a comic ships in: ZIP
// (.cbz), RAR (.cbr) and 7z (.cb7). All three hold the same thing — page
// images named so that sorting them is the page order, plus an optional
// ComicInfo.xml — and the model table folds all three extensions onto the
// one CBZ format (model.FormatSpecs). A comic is what it is regardless of
// which archiver squeezed it, so the rules that decide what the cover is
// and what the metadata says live here, above the container, and each
// container supplies only the two things it alone can answer: what the
// entries are called, and what a named entry's bytes are.
//
// Written this way because the alternative was three copies of the
// natural sort, the cover preference and the ComicInfo mapping, which is
// exactly the shape the format table replaced one tier down (#308): the
// day the cover rule changes, it must change once.

// comicArchive is a comic's container, reduced to what the pass above
// needs from it.
//
// Deliberately not an fs.FS. The pass reads at most two entries out of an
// archive that may hold hundreds, and the RAR container is sequential —
// it can serve one walk cheaply and a random access not at all — so the
// read is asked for every wanted entry at once, letting that backend
// answer with a single pass while the seekable two answer with two direct
// reads.
type comicArchive interface {
	// entries returns the archive's file entries in archive order,
	// directories excluded. Order matters: the ComicInfo and cover
	// preferences below both take the first match in archive order, which
	// is the order the archive was written in, not sorted order.
	entries() []string
	// read returns the bytes of the named entries, each bounded by the
	// byte cap the caller gives for it.
	//
	// An entry that cannot be read is absent from the result rather than
	// an error: a comic whose cover entry is broken still has good
	// metadata, the same best-effort contract EPUB, FB2 and MOBI covers
	// have. The error return is for the archive as a whole — encrypted,
	// or a container failure that makes every remaining read meaningless.
	//
	// ctx is honoured because a read is not always cheap: walking a solid
	// RAR to reach its cover decodes everything ahead of it, which is an
	// amount of work the archive chose, not this package.
	read(ctx context.Context, want map[string]int64) (map[string][]byte, error)
}

const (
	// comicMaxCoverBytes bounds the one page this pass buffers. A cover
	// is one scanned page; 32 MiB is far above any real one (a 4000px
	// colour scan lands in single-digit MB) and far below what a
	// decompression bomb wants, which matters for the two compressed
	// containers where a few KB of archive can declare gigabytes of
	// output.
	comicMaxCoverBytes int64 = 32 << 20
	// comicMaxComicInfoBytes bounds ComicInfo.xml, which is a few hundred
	// bytes of series/issue/credits in every real archive.
	comicMaxComicInfoBytes int64 = 1 << 20
	// comicMaxEntries bounds the entry list itself, for the two
	// containers whose listing this package builds rather than receives
	// already sized from archive/zip. Long-running manga volumes reach a
	// few thousand pages; nothing legitimate is near this.
	comicMaxEntries = 65536
)

// errEncryptedArchive is the answer for a comic whose pages are locked
// behind a password. RAR and 7z both encrypt, and the distinction matters
// to whoever reads the failure on the BookDrop row: a corrupt archive is
// a bad file to replace, an encrypted one is a good file we will not ask
// for the password to. Wrapped with the container's name by each backend
// and matched with errors.Is.
var errEncryptedArchive = errors.New("the archive is password-protected")

// comicInfoXML is the ComicInfo.xml schema, as far as this reads it.
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

// extractComic is the whole comic metadata pass. kind names the container
// in error messages (.cbz, .cbr, .cb7); the format stamped is CBZ for all
// three, because that is the one format row the table folds them onto and
// books.format is what this field feeds.
func extractComic(ctx context.Context, kind string, a comicArchive) (Metadata, error) {
	names := a.entries()

	pages := comicPages(names)
	if len(pages) == 0 {
		return Metadata{}, fmt.Errorf("%s contains no images", kind)
	}

	// Cover: a top-level `cover.*` if the archive ships one, otherwise the
	// first page after natural sort.
	coverName := preferredCoverName(names)
	if coverName == "" {
		coverName = pages[0]
	}
	infoName := comicInfoName(names)

	want := map[string]int64{coverName: comicMaxCoverBytes}
	if infoName != "" {
		want[infoName] = comicMaxComicInfoBytes
	}
	got, err := a.read(ctx, want)
	if err != nil {
		return Metadata{}, err
	}

	m := Metadata{Format: "CBZ"}

	// ComicInfo.xml is optional, and so is its being well-formed: an
	// unparseable one leaves the book with a cover and a filename-derived
	// title rather than failing the import.
	if infoName != "" {
		var info comicInfoXML
		if b, ok := got[infoName]; ok && xml.Unmarshal(b, &info) == nil {
			applyComicInfo(&m, info)
		}
	}

	// The entry's extension picked which page is the cover; it does not
	// get to say what that page is. An archive's entry names are chosen
	// by whoever packed it, and the cover route serves the resulting type
	// back as a Content-Type (#330), so the type comes from the bytes and
	// a "page" that is not an image leaves the book cover-less — the same
	// refusal epub.go, audio.go, fb2.go and mobi.go make at their own
	// layer.
	if b, ok := got[coverName]; ok {
		if mime := SniffImageMime(b); mime != "" {
			m.HasCover = true
			m.CoverBytes = b
			m.CoverMime = mime
		}
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

// comicPages returns the image entries among names, naturally sorted.
// ComicInfo.xml and other non-image entries are filtered out.
func comicPages(names []string) []string {
	var pages []string
	for _, name := range names {
		if !isImageExt(path.Ext(name)) {
			continue
		}
		pages = append(pages, name)
	}
	sort.Slice(pages, func(i, j int) bool {
		return naturalLess(pages[i], pages[j])
	})
	return pages
}

// preferredCoverName returns the entry name for a top-level
// `cover.{jpg,jpeg,png,webp}`, if the archive has one. Empty otherwise.
func preferredCoverName(names []string) string {
	for _, name := range names {
		base := strings.ToLower(path.Base(name))
		// Only a top-level file (no directory component) qualifies; we
		// don't want to grab a chapter-internal "cover.jpg".
		if dir := path.Dir(name); dir != "." && dir != "" {
			continue
		}
		switch base {
		case "cover.jpg", "cover.jpeg", "cover.png", "cover.webp":
			return name
		}
	}
	return ""
}

// comicInfoName returns the archive's ComicInfo.xml entry, matched
// case-insensitively on the base name because some authoring tools save
// it as `comicinfo.xml` and some put it in a subdirectory. Empty when the
// archive has none.
func comicInfoName(names []string) string {
	for _, name := range names {
		if strings.ToLower(path.Base(name)) == "comicinfo.xml" {
			return name
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

// readCappedEntry reads an archive entry whole, refusing one that runs
// past max rather than buffering it. Reads one byte past the cap so a
// file landing exactly on it is not mistaken for one over it — the shape
// FB2's whole-file read uses, applied per entry here because a comic's
// bytes arrive one page at a time.
//
// This is the bound that stands between a decompression bomb and this
// process: the declared size in an archive header is a number the archive
// chose, so the limit has to sit on the read itself.
//
// Refusing here loses a field silently as far as the row is concerned —
// the book arrives with no cover and nothing on it says why — so the cap
// says so in the log. It is a warning rather than an error because a
// bomb-shaped page and a genuinely enormous scan look identical from
// here, and one of them is a real book someone dropped.
func readCappedEntry(r io.Reader, name string, max int64) ([]byte, error) {
	b, err := io.ReadAll(&io.LimitedReader{R: r, N: max + 1})
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		slog.Warn("comic entry over the read cap, dropped", "entry", name, "capBytes", max)
		return nil, fmt.Errorf("entry %q expands past the %d byte cap", name, max)
	}
	return b, nil
}
