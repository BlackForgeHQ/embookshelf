// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"time"

	"github.com/blackforge/embookshelf/internal/storage"
)

// bookDelivery is the delivery seam of the LibraryHandle: the one
// answer to "how do these bytes reach the client", over the keys seam
// and the presign config (#346). The HTTP tier is its only consumer —
// through the handle's BookSource/FileSource, whose interface this
// carve leaves untouched.
type bookDelivery struct {
	store           storage.Storage
	keys            libraryKeys
	presignTTL      time.Duration
	presignFallback string
}

// fileSource is the delivery decision for one stored location, which is
// the whole of the decision — the primary-file question sits above it
// (LibraryHandle.BookSource).
//
// Split from the book-level answer because a book has more than one
// thing to serve. The reader dispatches on the rendition the user
// picked rather than on books.format (ADR-0025 §3), so the generated
// narration is served by location while the EPUB is served by
// primaryFile — and the narration path once answered the delivery
// question a second time, hardcoding a stream: an install that turned
// presign on redirected its EPUBs and streamed its half-gigabyte MP3s,
// which is the case presign exists for. One selector must not mean two
// delivery policies.
//
// The key rule is not restated either: the keys seam answers it for
// both kinds of backend, so an object store gets the location it
// already answers to and a local library gets it rooted (#168).
func (d bookDelivery) fileSource(ctx context.Context, location string) (BookSource, error) {
	if location == "" {
		return BookSource{}, errors.New("file source: no location")
	}
	if d.store == nil {
		// No Storage resolved: the local filesystem is all that is left,
		// and only a local library has a path to offer.
		if path := d.keys.localPath(location); path != "" {
			return BookSource{Kind: BookDeliveryLocal, Path: path}, nil
		}
		return BookSource{}, errors.New("file source: library has no storage")
	}

	key := d.keys.storageKey(location)
	if d.presignFallback == BookDeliveryPresign && d.store.Capabilities()&storage.CapPresign != 0 {
		if ps, ok := d.store.(Presigner); ok {
			// A failed signature falls through to streaming rather than
			// failing the read: the bytes are reachable either way, and
			// the redirect is an optimisation.
			if url, err := ps.PresignGet(ctx, key, d.presignTTL); err == nil {
				return BookSource{Kind: BookDeliveryPresign, URL: url, TTL: d.presignTTL}, nil
			}
		}
	}
	return BookSource{Kind: BookDeliveryStream, Storage: d.store, Key: key}, nil
}
