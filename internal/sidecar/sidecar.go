// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sidecar reads and writes per-book metadata files that live
// next to the book bytes on disk (or in object storage). Two formats:
// metadata.opf (Calibre-compatible XML, read-only) and
// <basename>.embookshelf.json (native, read+write, paired filename).
package sidecar

import "github.com/blackforge/embookshelf/internal/model"

// Sidecar is the editable metadata payload carried by a JSON sidecar
// file. Aliased to model.EditableMetadata so write-side EmbedInput
// and read-side Sidecar share one canonical shape.
type Sidecar = model.EditableMetadata

// IsZero reports whether s carries no information. Delegates to
// EditableMetadata.IsZero — kept as a package-level helper for
// callers that prefer the function form.
func IsZero(s Sidecar) bool {
	return s.IsZero()
}

// Merge overlays b on a: any non-zero field in b wins. Delegates to
// model.MergeEditable.
func Merge(a, b Sidecar) Sidecar {
	return model.MergeEditable(a, b)
}
