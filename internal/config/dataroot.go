// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrDataRootUnset says the deployment has no data root. It is the only
// spelling of that fact: consumers ask a DataRoot for a location and
// handle this error, rather than each testing a string for emptiness and
// each deciding what emptiness means. Before the type there were four
// such decisions and they did not agree — the segment worker joined the
// empty root and wrote hundreds of megabytes to a relative path while
// the sweeper meant to reclaim it had already concluded there was
// nothing to reclaim (#207).
var ErrDataRootUnset = errors.New("data root is not configured (DATA_PATH)")

// DataRoot is the absolute directory under which embookshelf keeps
// everything it derives rather than stores: managed local libraries,
// cover art, audiobook staging.
//
// It has exactly two states. A root built by NewDataRoot is absolute —
// construction resolves a relative value or fails, so no consumer
// downstream can be handed one that is not. The zero value is unset, and
// every location on it returns ErrDataRootUnset instead of a path; that
// is why the zero value cannot produce a relative path the way an empty
// string could.
//
// Absolute matters because local libraries are rooted at "/"
// (storageloader, ADR-0030): the path written to the libraries row is
// read back as an absolute filesystem key. Left relative the two
// disagree, the library is created, the books import, and every file
// fetch 403s with "no such file or directory /data/...".
type DataRoot struct {
	// path is absolute whenever it is non-empty. Unexported so the
	// invariant cannot be sidestepped by constructing the struct.
	path string
}

// NewDataRoot resolves p against the process working directory and
// returns the root. It refuses an empty p: "unset" is a state you
// declare (var root DataRoot), never one you fall into by passing a
// string that happened to be empty.
func NewDataRoot(p string) (DataRoot, error) {
	if strings.TrimSpace(p) == "" {
		return DataRoot{}, ErrDataRootUnset
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return DataRoot{}, fmt.Errorf("resolve data root %q: %w", p, err)
	}
	return DataRoot{path: abs}, nil
}

// IsSet reports whether a root was configured. For callers deciding
// whether to run at all — a sweeper with nothing to sweep — rather than
// for callers about to derive a location, which should ask for the
// location and handle ErrDataRootUnset.
func (r DataRoot) IsSet() bool { return r.path != "" }

// Path is the root itself, for the rare caller that must hand the whole
// directory to something outside these sub-locations.
func (r DataRoot) Path() (string, error) {
	if !r.IsSet() {
		return "", ErrDataRootUnset
	}
	return r.path, nil
}

// String is for logs and for display to an operator; it renders an unset
// root as the empty string. Not a way to get a path to build on — that
// is Path, which makes the unset case impossible to ignore.
func (r DataRoot) String() string { return r.path }

// Library is where a managed kind=local library's files live (ADR-0002).
func (r DataRoot) Library(slug string) (string, error) {
	return r.join("libraries", slug)
}

// Covers is where cover art extracted from books is cached.
func (r DataRoot) Covers() (string, error) {
	return r.join("covers")
}

// AudiobookStaging is where one book's per-segment MP3s live until
// finalize. Local disk, outside storage.Storage, following the
// coverstore precedent for derived bytes.
func (r DataRoot) AudiobookStaging(bookID string) (string, error) {
	return r.join("audiobooks", bookID)
}

func (r DataRoot) join(parts ...string) (string, error) {
	if !r.IsSet() {
		return "", ErrDataRootUnset
	}
	return filepath.Join(append([]string{r.path}, parts...)...), nil
}
