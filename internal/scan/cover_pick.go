package scan

import (
	"path"
	"sort"
	"strings"
)

// CoverSource enumerates the precedence layers for picking a cover
// during scan-import per ADR-0003 §9 / docs/spec/library-layout.spec.md
// §5.3. Higher numeric value = higher precedence.
type CoverSource int

const (
	// CoverNone means no cover candidate exists for this LeafBook.
	CoverNone CoverSource = iota
	// CoverSidecarJSON marks the JSON sidecar's cover_b64 field as
	// the chosen source (full-mirror sidecars only).
	CoverSidecarJSON
	// CoverEmbeddedCompanion marks an in-file embedded cover from
	// a companion file (not the primary).
	CoverEmbeddedCompanion
	// CoverEmbeddedPrimary marks an in-file embedded cover from
	// the primary file (highest format priority among siblings).
	CoverEmbeddedPrimary
	// CoverFolderImage marks a `cover.{ext}` image at the LeafBook
	// folder root.
	CoverFolderImage
	// CoverLocked means the Book has cover_locked=true; keep the
	// current cover regardless of any other signal.
	CoverLocked
)

func (c CoverSource) String() string {
	switch c {
	case CoverFolderImage:
		return "folder_image"
	case CoverEmbeddedPrimary:
		return "embedded_primary"
	case CoverEmbeddedCompanion:
		return "embedded_companion"
	case CoverSidecarJSON:
		return "sidecar_json"
	case CoverLocked:
		return "locked"
	}
	return "none"
}

// CoverChoice carries the picker's verdict: which source wins and the
// path/key to read it from when applicable. Source==CoverLocked or
// CoverNone leave Path empty — the caller has no read to do.
type CoverChoice struct {
	Source CoverSource
	// Path is library-relative (matches storage key shape) when the
	// source is a discrete file: CoverFolderImage points at the
	// `cover.{ext}` location, CoverEmbeddedPrimary/Companion at the
	// owning book file (extractor opens it). Empty when not applicable.
	Path string
}

// CoverFolderImageNames lists the basenames Scan looks for at a
// LeafBook folder root, in alphabetical-by-extension-priority order
// (per spec §5.3): jpg first because the format is universal and
// smaller-on-disk than png/webp.
var CoverFolderImageNames = []string{"cover.jpg", "cover.jpeg", "cover.png", "cover.webp"}

// CoverInputs is what the picker needs to know about a LeafBook:
//   - Files: every entry from the LeafBook's Files (supported book
//     files only; Classify already filtered).
//   - FolderEntries: every WalkEntry that lives directly at the
//     LeafBook's Folder level (covers + sidecars + non-supported
//     siblings). Used to find cover.{ext} images.
//   - Locked: whether the Book has cover_locked=true.
//   - SidecarHasCover: whether the merged sidecar carries a
//     non-empty cover_b64 field (full-mirror only).
//   - PrimaryFormat: which format counts as the LeafBook's primary
//     (drives Embedded-primary vs companion classification).
type CoverInputs struct {
	Files           []WalkEntry
	FolderEntries   []WalkEntry
	Locked          bool
	SidecarHasCover bool
	PrimaryFormat   string
}

// PickCover returns the highest-precedence cover source for a
// LeafBook per ADR-0003 §9. Pure function — no I/O. The caller
// reads bytes for whichever Source the result names.
//
// Precedence (highest wins):
//  1. CoverLocked      (cover_locked=true)
//  2. CoverFolderImage (cover.{ext} at folder root, jpg→jpeg→png→webp)
//  3. CoverEmbeddedPrimary (highest-priority file's in-file cover)
//  4. CoverEmbeddedCompanion (some other sibling's in-file cover)
//  5. CoverSidecarJSON (full-mirror sidecar cover_b64)
//  6. CoverNone
//
// CoverEmbeddedPrimary/Companion only return Path when there is at
// least one supported file in Files. The picker cannot tell whether a
// given format actually carries an embedded cover — that's an extract-
// time fact. Treat the choice as "ask the extractor about this file
// first;" the extractor falls through to lower-precedence sources if
// the file has no cover.
func PickCover(in CoverInputs) CoverChoice {
	if in.Locked {
		return CoverChoice{Source: CoverLocked}
	}
	if path := pickFolderImage(in.FolderEntries); path != "" {
		return CoverChoice{Source: CoverFolderImage, Path: path}
	}
	primary := pickPrimary(in.Files, in.PrimaryFormat)
	if primary != "" {
		return CoverChoice{Source: CoverEmbeddedPrimary, Path: primary}
	}
	if companion := pickCompanion(in.Files, in.PrimaryFormat); companion != "" {
		return CoverChoice{Source: CoverEmbeddedCompanion, Path: companion}
	}
	if in.SidecarHasCover {
		return CoverChoice{Source: CoverSidecarJSON}
	}
	return CoverChoice{Source: CoverNone}
}

// pickFolderImage returns the library-relative path of the highest-
// priority `cover.{ext}` at the folder root, or "" if none exist.
// Comparison is case-insensitive on the filename so `Cover.JPG`
// counts; the on-disk basename in the entry is preserved in the
// returned path so callers don't open a different case.
func pickFolderImage(entries []WalkEntry) string {
	if len(entries) == 0 {
		return ""
	}
	preferred := map[string]int{}
	for i, name := range CoverFolderImageNames {
		preferred[name] = i
	}
	best := -1
	bestPath := ""
	for _, e := range entries {
		base := strings.ToLower(path.Base(e.Location))
		idx, ok := preferred[base]
		if !ok {
			continue
		}
		if best < 0 || idx < best {
			best = idx
			bestPath = e.Location
		}
	}
	return bestPath
}

// pickPrimary returns the location of the file that matches
// primaryFormat, or "" if none of the LeafBook's files do.
func pickPrimary(files []WalkEntry, primaryFormat string) string {
	if primaryFormat == "" {
		return ""
	}
	for _, f := range files {
		if formatForPath(f.Location) == primaryFormat {
			return f.Location
		}
	}
	return ""
}

// pickCompanion returns the location of the highest-priority non-
// primary file. Order matches the format priority list used by Plan B
// for primary-format selection; we return the first companion that
// matches the highest non-primary slot.
func pickCompanion(files []WalkEntry, primaryFormat string) string {
	priority := []string{"EPUB", "PDF", "CBZ", "AZW3", "MOBI", "FB2", "M4B", "MP3"}
	companions := make([]WalkEntry, 0, len(files))
	for _, f := range files {
		if formatForPath(f.Location) != primaryFormat {
			companions = append(companions, f)
		}
	}
	if len(companions) == 0 {
		return ""
	}
	rank := func(loc string) int {
		f := formatForPath(loc)
		for i, p := range priority {
			if p == f {
				return i
			}
		}
		return len(priority)
	}
	sort.SliceStable(companions, func(i, j int) bool {
		ri, rj := rank(companions[i].Location), rank(companions[j].Location)
		if ri != rj {
			return ri < rj
		}
		return companions[i].Location < companions[j].Location
	})
	return companions[0].Location
}

// formatForPath maps an extension to its books.format slug. Mirrors
// fileproc.FormatForExt without importing fileproc — keeps the scan
// package import-graph small. Lower-cased extension lookup.
func formatForPath(loc string) string {
	ext := strings.ToLower(path.Ext(loc))
	switch ext {
	case ".epub":
		return "EPUB"
	case ".pdf":
		return "PDF"
	case ".cbz":
		return "CBZ"
	case ".cbr":
		return "CBR"
	case ".cb7":
		return "CB7"
	case ".azw3":
		return "AZW3"
	case ".mobi":
		return "MOBI"
	case ".fb2":
		return "FB2"
	case ".m4b":
		return "M4B"
	case ".mp3":
		return "MP3"
	}
	return ""
}
