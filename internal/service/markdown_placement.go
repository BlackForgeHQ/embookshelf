// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"

	"github.com/blackforge/embookshelf/internal/storage"
)

// OpenMarkdown opens a Markdown rendition by its tracking-row location.
//
// Through StorageKey, not Storage.Open directly: the row stores the
// library-relative location PlaceAt returned, and the local backend is
// rooted at "/" (ADR-0030), so the bare location would be read relative
// to nowhere and miss. Backend-backed libraries pass through unchanged —
// their keys are already object keys. Mirrors what OpenBook does for
// files rows.
//
// Placement itself is PlaceDerived (book_ops.go): the markdown
// rendition is one of the three derived artifacts sharing a key
// derivation and a placement entry point.
func (h *LibraryHandle) OpenMarkdown(ctx context.Context, location string) (storage.Source, error) {
	if h.Storage == nil {
		return nil, errors.New("library handle: no storage")
	}
	return h.Storage.Open(ctx, h.StorageKey(location))
}
