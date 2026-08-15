// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/blackforge/embookshelf/internal/model"
)

// libraryKeys is the key-arithmetic seam of the LibraryHandle: every
// question about *keys* — what this library's locations resolve to,
// what a walk lists under, what a location means on disk — answered by
// one pure value with no I/O (#346). An internal seam, not a caller
// surface: the handle's interface stays where it was, and this is the
// implementation concentrated where its own tests can hold it.
//
// objectStore is the one capability fact the arithmetic branches on,
// carried as a value so the questions stay answerable without a
// Storage in hand. It is decided by the handle (IsObjectStore — asked
// of the adapter, never of libraries.backend_id, #202).
type libraryKeys struct {
	lib         model.Library
	objectStore bool
}

// root is the prefix this library's own keys hang off, and whether it
// has one at all.
//
// An object store owns its own per-library prefix, so a stored location
// is already the key it answers to and an empty root is the correct
// answer. The local backend is rooted at "/" for the whole instance
// (ADR-0030 §1), so a location means nothing until it is joined onto
// the library's own root.
//
// Telling those two empties apart is why there is a second return
// value, and it is not a formality: for an object store an empty root
// is by design, while for a local library it means unconfigured, and
// every caller that took the location as a key anyway did real damage —
// a walk that did reported the whole library empty and flagged every
// row for the purge sweeper (#203), and a write that did puts a book's
// file at the filesystem root. Callers name that case in their own
// vocabulary (ErrNoWalkRoot, ErrNoPlaceRoot) because what it costs
// differs.
func (k libraryKeys) root() (string, bool) {
	if k.objectStore {
		return "", true
	}
	root := libraryLocalRoot(k.lib)
	return root, root != ""
}

// localPath resolves a library-relative location to an absolute path on
// a local library. Empty for object-store-backed libraries, which have
// no filesystem to resolve against, and for a local library with no
// root configured, which has nothing to resolve against yet.
func (k libraryKeys) localPath(location string) string {
	root, ok := k.root()
	if !ok || root == "" {
		return ""
	}
	return filepath.Join(root, filepath.FromSlash(location))
}

// storageKey turns a files.location into the key this library's Storage
// actually answers to.
//
// files.location is relative to the library root (CONTEXT, "Files
// row"), but a local install's LocalFS is rooted at "/" and expects
// absolute keys — it is deliberately not rooted per library, because
// the scan worker and bookdrop ingest hand it absolute paths. Nothing
// reconciled the two, so every read of a locally-placed book asked the
// filesystem for "/Author/Title/book.epub" and got nothing (#168,
// #201 — the job tier's private re-derivation missed both branches).
func (k libraryKeys) storageKey(location string) string {
	// Already absolute: a legacy row. books.path predates storage-v2,
	// and the storage-v2 backfill wrote files.location verbatim whenever
	// the library root was unknown at seed time
	// (migrator.seedFilesFromBooks, which its own tests pin). Such a
	// string is already the key a "/"-rooted LocalFS wants; joining it
	// onto the root asks for /lib/root/lib/root/… and finds nothing.
	if filepath.IsAbs(location) {
		return location
	}
	if abs := k.localPath(location); abs != "" {
		return abs
	}
	return location
}

// walkBase is the prefix in the shape the backend reports keys under
// it: cleaned, slash-separated, leading slash off, because a listing
// key is relative to the backend's own root.
func walkBase(prefix string) string {
	if prefix == "" {
		return ""
	}
	return strings.TrimPrefix(filepath.ToSlash(filepath.Clean(prefix)), "/")
}

// trimWalkBase turns a key the backend reported into a location
// relative to the walked prefix.
//
// It compares with the leading slash off both sides for the same reason
// the conformance suite does (storagetest.rebased.in): a "/"-rooted
// backend answers to "/a/b" but reports "a/b". A key that does not sit
// under the prefix at all comes back untouched — the backend only lists
// what is under the prefix it was given, so there is no case for this
// to paper over, and silently rewriting one would hide a backend that
// broke that contract.
func trimWalkBase(key, base string) string {
	if base == "" {
		return key
	}
	k := strings.TrimPrefix(key, "/")
	switch {
	case k == base:
		// The library root is itself a file. Degenerate, but it must
		// still name something.
		return path.Base(k)
	case strings.HasPrefix(k, base+"/"):
		return k[len(base)+1:]
	default:
		return key
	}
}
