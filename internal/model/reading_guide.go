// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import "time"

// GuideSource records what a ReadingGuide was built from. It is stored
// and shown to the reader: a metadata-only guide for an obscure title
// leans on the model's prior knowledge rather than on the book, and that
// is a materially different thing to trust. ADR-0024 §2.
type GuideSource string

const (
	// GuideSourceFullText — the model read the book. EPUB only today;
	// PDF needs a dependency that still fails on scans, and CBZ and audio
	// have no text at all.
	GuideSourceFullText GuideSource = "full_text"
	// GuideSourceMetadata — the model saw title, author, blurb, genres.
	GuideSourceMetadata GuideSource = "metadata"
)

// ReadingGuideText is the guide's prose: the four questions a reader has
// before committing to a book. Separated from the provenance fields
// because an edit replaces the text and nothing else.
type ReadingGuideText struct {
	About    string
	Audience string
	NotFor   string
	Problems string
}

// ReadingGuide is an LLM-written orientation for one book (ADR-0024).
//
// **Distinct** from Book.Description, which is the publisher blurb: that
// arrives from a metadata provider or the EPUB OPF, is lock-protected,
// and is mirrored into the sidecar and the file itself. A ReadingGuide is
// derived text that deliberately never enters the metadata write-back
// pipeline — hence its own table rather than columns on books.
type ReadingGuide struct {
	BookID string
	ReadingGuideText

	SourceKind GuideSource
	// Model that produced the text, kept so guides written by a weaker
	// model can be found and regenerated later.
	Model string
	// Language the guide is written in. One per instance, configured;
	// changing the setting means regenerating (ADR-0024 §6).
	Language    string
	GeneratedAt time.Time
	// EditedByUser marks text a human wrote. A bulk Guide run skips these
	// so it cannot erase hand-written work; the per-book button still
	// overwrites, because there the user sees what they are replacing.
	EditedByUser bool
}
