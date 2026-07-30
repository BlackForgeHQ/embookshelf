// SPDX-License-Identifier: AGPL-3.0-or-later

// Package recovery holds one-shot repairs for damage a shipped bug left
// on disk. Nothing here runs on its own: each repair is a subcommand an
// operator invokes deliberately, reports before it changes anything, and
// is safe to run twice.
package recovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// Kind is what the tool concluded about one catalog row.
type Kind string

const (
	// KindRecovered — the bytes at the filesystem root belong to this
	// row and nothing occupies its real location. Moved under --apply.
	KindRecovered Kind = "recovered"
	// KindOccupied — a file already sits at the correct location, so the
	// operator has re-imported this book and the copy at the root is a
	// duplicate. Reported, never touched.
	KindOccupied Kind = "occupied"
	// KindMismatch — something is at the suspect path but its bytes are
	// not the ones this row records. Reported, never touched.
	KindMismatch Kind = "mismatch"
	// KindAmbiguous — two books name the same library-relative key, so
	// one file at the root cannot be attributed. Reported, never touched.
	KindAmbiguous Kind = "ambiguous"
	// KindFailed — this row was recoverable and the move (or the files
	// row it needed) failed. The sweep continues; the operator sees why.
	KindFailed Kind = "failed"
)

// Finding is one row's verdict, in the vocabulary an operator can act
// on: which book, which paths, and what the tool did or refused to do.
type Finding struct {
	Library  string
	BookID   string
	Title    string
	Author   string
	Location string
	// Suspect is where the broken placer put the bytes.
	Suspect string
	// Correct is where this library says they belong.
	Correct string
	Kind    Kind
	Detail  string
	// FileRowRecreated records that the move also put back a files row
	// the missing-purge had deleted.
	FileRowRecreated bool
}

// Report is the whole sweep. Applied distinguishes "did" from "would do"
// for every KindRecovered in it.
type Report struct {
	Applied            bool
	Root               string
	LibrariesInspected int
	LibrariesSkipped   int
	BooksInspected     int
	Findings           []Finding
}

// Count returns how many findings carry the given kind.
func (r Report) Count(k Kind) int {
	n := 0
	for _, f := range r.Findings {
		if f.Kind == k {
			n++
		}
	}
	return n
}

// Options configures one sweep.
type Options struct {
	// Apply moves bytes. False — the default — reports and changes
	// nothing.
	Apply bool

	// FSRoot is the directory the broken placer resolved library-relative
	// keys against: "/" in production, because every local backend is
	// constructed rooted there (ADR-0030 §1) and that is precisely how a
	// library-relative key ended up naming a path outside the library.
	//
	// It is a parameter rather than a constant for one reason, and it is
	// not decoration: a test for this tool has to put a file at the root
	// the tool searches, and no test may write to the real "/". Pointing
	// FSRoot at t.TempDir() is the only thing that makes the sweep
	// exercisable at all. Empty means "/".
	FSRoot string
}

// Deps are the catalog and the library seam. Store is asked the same
// question every other tier asks — LibraryHandle.IsObjectStore, and
// LocalPath for the library's own root — rather than reading
// libraries.backend_id, because reading that column instead of asking
// the adapter is the bug this package exists to clean up (#265).
type Deps struct {
	Libraries *repo.LibraryRepo
	Books     *repo.BookRepo
	Files     *repo.FileRepo
	Store     service.LibraryStore
}

// Run sweeps every affected library and reports what it found. With
// opts.Apply false it changes nothing, and the findings are exactly what
// an --apply run would act on.
//
// It never walks a filesystem. Every path it stats is derived from the
// catalog — `filepath.Join(FSRoot, location)` for a key the catalog
// already holds — so nothing that no book names is even looked at.
func Run(ctx context.Context, deps Deps, opts Options) (Report, error) {
	if deps.Libraries == nil || deps.Books == nil || deps.Files == nil || deps.Store == nil {
		return Report{}, errors.New("recovery: incomplete dependencies")
	}
	root := opts.FSRoot
	if root == "" {
		root = "/"
	}
	rep := Report{Applied: opts.Apply, Root: root}

	libs, err := deps.Libraries.List(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("list libraries: %w", err)
	}

	// One file at the root can belong to at most one book. Claims are
	// tracked so a dry run and an --apply run give the same verdict for
	// two books that share a library-relative key: without this the
	// second book reads as recoverable until the first move makes it
	// vanish.
	claimed := map[string]string{}

	for _, lib := range libs {
		// A library with no backend row was created after the storage-v2
		// migration and always had the right placer. This is the one read
		// of the column, and it is the affected-install signature, not the
		// local-or-object question — that one goes to the handle below.
		if lib.BackendID == nil {
			rep.LibrariesSkipped++
			continue
		}
		h, err := deps.Store.For(ctx, lib.ID)
		if err != nil {
			return Report{}, fmt.Errorf("library %s: %w", lib.Name, err)
		}
		// Genuine object-store libraries were never affected: their keys
		// are keys in a bucket and have no filesystem root to escape.
		if h.IsObjectStore() {
			rep.LibrariesSkipped++
			continue
		}
		// LocalPath("") is the library's own root, or "" when it has none
		// — a local library with nothing to be moved into.
		if h.LocalPath("") == "" {
			rep.LibrariesSkipped++
			continue
		}
		rep.LibrariesInspected++

		placements, err := deps.Books.ListPlacements(ctx, lib.ID)
		if err != nil {
			return Report{}, fmt.Errorf("library %s: list placements: %w", lib.Name, err)
		}
		for _, cand := range candidates(placements, &rep) {
			f, ok := inspect(ctx, deps, h, lib, cand, root, claimed, opts.Apply)
			if ok {
				rep.Findings = append(rep.Findings, f)
			}
		}
	}
	return rep, nil
}

// candidate is one (book, key) pair worth checking, carrying the files
// row when one survives.
type candidate struct {
	place repo.Placement
	// location is the key to check. It is the files row's location, or
	// books.path when the row is gone.
	location string
}

// candidates turns the placement rows of a library into the keys to
// check. Every surviving files row contributes its location; a book
// whose row for its own path was purged contributes books.path, which
// holds the same key an approve wrote to the row.
func candidates(placements []repo.Placement, rep *Report) []candidate {
	byBook := map[string][]repo.Placement{}
	order := []string{}
	for _, p := range placements {
		if _, seen := byBook[p.BookID]; !seen {
			order = append(order, p.BookID)
		}
		byBook[p.BookID] = append(byBook[p.BookID], p)
	}
	rep.BooksInspected += len(order)

	out := make([]candidate, 0, len(placements))
	for _, bookID := range order {
		rows := byBook[bookID]
		locations := map[string]bool{}
		for _, p := range rows {
			if !p.HasFileRow() {
				continue
			}
			locations[p.Location] = true
			out = append(out, candidate{place: p, location: p.Location})
		}
		// books.path is the last record that the bytes were placed once
		// the purge has taken the row. A book whose surviving rows are for
		// other artifacts (a narration, say) still needs its own path
		// checked, so this is keyed on the location, not on row count.
		bare := rows[0]
		if bare.BookPath != "" && !locations[bare.BookPath] {
			stripped := bare
			stripped.FileID = ""
			stripped.Location = bare.BookPath
			stripped.Size = 0
			stripped.ContentHash = nil
			out = append(out, candidate{place: stripped, location: bare.BookPath})
		}
	}
	return out
}

// inspect decides one candidate. The second return is false when there
// is nothing to say — the overwhelmingly common case on a healthy
// install, where the suspect path simply holds nothing.
func inspect(
	ctx context.Context,
	deps Deps,
	h *service.LibraryHandle,
	lib model.Library,
	cand candidate,
	root string,
	claimed map[string]string,
	apply bool,
) (Finding, bool) {
	location := cand.location
	if location == "" {
		return Finding{}, false
	}
	// An absolute location is a legacy row seeded by the storage-v2
	// backfill (ADR-0030). The bug needed a library-relative key to
	// escape the library, so such a row was never affected — and joining
	// it onto the root would name itself.
	if filepath.IsAbs(location) {
		return Finding{}, false
	}
	suspect := filepath.Join(root, filepath.FromSlash(location))
	correct := h.LocalPath(location)
	if correct == "" || suspect == correct {
		return Finding{}, false
	}

	f := Finding{
		Library:  lib.Name,
		BookID:   cand.place.BookID,
		Title:    cand.place.Title,
		Author:   cand.place.Author,
		Location: location,
		Suspect:  suspect,
		Correct:  correct,
	}

	if other, taken := claimed[suspect]; taken {
		if other == cand.place.BookID {
			return Finding{}, false
		}
		f.Kind = KindAmbiguous
		f.Detail = fmt.Sprintf("book %s names the same key; not attributable", other)
		return f, true
	}

	st, err := os.Stat(suspect)
	if err != nil || !st.Mode().IsRegular() {
		return Finding{}, false
	}
	claimed[suspect] = cand.place.BookID

	if _, err := os.Stat(correct); err == nil {
		f.Kind = KindOccupied
		f.Detail = "a file is already at the correct location; the copy at the root is a duplicate — remove it yourself"
		return f, true
	}

	// Content check. The hash is the authoritative identity (CONTEXT.md,
	// Content hash); size is the weaker fallback for a row the hash
	// backfill has not reached. A purged row leaves neither, and the
	// finding says so rather than implying a check that did not happen.
	switch {
	case len(cand.place.ContentHash) > 0:
		sum, herr := hashFile(suspect)
		if herr != nil {
			f.Kind = KindFailed
			f.Detail = fmt.Sprintf("hash %s: %v", suspect, herr)
			return f, true
		}
		if !bytes.Equal(sum, cand.place.ContentHash) {
			f.Kind = KindMismatch
			f.Detail = "bytes at the root do not match this book's content hash; left alone"
			return f, true
		}
	case cand.place.HasFileRow():
		if cand.place.Size != st.Size() {
			f.Kind = KindMismatch
			f.Detail = fmt.Sprintf("size %d at the root, %d recorded (no content hash to check); left alone",
				st.Size(), cand.place.Size)
			return f, true
		}
	default:
		f.Detail = "no files row survives to verify the bytes against"
	}

	f.Kind = KindRecovered
	if !apply {
		return f, true
	}

	if err := moveFile(suspect, correct); err != nil {
		f.Kind = KindFailed
		f.Detail = err.Error()
		return f, true
	}
	if !cand.place.HasFileRow() {
		if err := recreateFileRow(ctx, deps.Files, lib.ID, cand.place, location, correct); err != nil {
			// The bytes are in the right place; only the row is missing.
			// Say exactly that — an operator who re-runs will find nothing
			// at the root and needs this line to know why the book is
			// still not readable.
			f.Kind = KindFailed
			f.Detail = fmt.Sprintf("bytes moved to %s but the files row could not be recreated: %v", correct, err)
			return f, true
		}
		f.FileRowRecreated = true
		return f, true
	}
	// The row survived, and the scan will have flagged it missing while
	// the bytes were gone. Leaving the flag set hands the file straight
	// back to the 24h purge that created the purged-row case above.
	if err := deps.Files.ClearMissing(ctx, cand.place.FileID); err != nil {
		f.Detail = fmt.Sprintf("moved, but clearing the missing flag failed: %v", err)
	}
	return f, true
}

// recreateFileRow puts back the row the missing-purge deleted. Without
// it the move lands bytes in a library where nothing points at them and
// the book stays unreadable — a scan will not adopt them (ADR-0018).
// Size, mtime and hash are read from the file that was just moved, which
// is the only remaining source of truth for them.
func recreateFileRow(
	ctx context.Context,
	files *repo.FileRepo,
	libraryID string,
	place repo.Placement,
	location, moved string,
) error {
	st, err := os.Stat(moved)
	if err != nil {
		return fmt.Errorf("stat moved file: %w", err)
	}
	sum, err := hashFile(moved)
	if err != nil {
		return fmt.Errorf("hash moved file: %w", err)
	}
	_, err = files.Insert(ctx, model.File{
		LibraryID:   libraryID,
		BookID:      place.BookID,
		Location:    location,
		Size:        st.Size(),
		Mtime:       st.ModTime(),
		ContentHash: sum,
		Format:      place.Format,
		LastScanned: time.Now().UTC(),
	})
	return err
}

// moveFile moves src onto dest, creating the destination's parents.
// os.Rename first; "/" and a library root are routinely different
// mounts, so the copy fallback is the normal case rather than the
// exception. On a copy failure the source is left exactly as it was.
func moveFile(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dest), err)
	}
	if err := os.Rename(src, dest); err == nil {
		return nil
	}
	if err := copyFile(src, dest); err != nil {
		_ = os.Remove(dest)
		return err
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("copied to %s but could not remove %s: %w", dest, src, err)
	}
	return nil
}

func copyFile(src, dest string) error {
	in, err := os.Open(src) //nolint:gosec // path derived from the catalog
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dest) //nolint:gosec // path derived from the catalog
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy to %s: %w", dest, err)
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("fsync %s: %w", dest, err)
	}
	return nil
}

// hashFile reads the bytes off the filesystem directly rather than
// through the library's Storage. The suspect path is not a key in any
// library — it is where the bytes went when a key was resolved against
// the wrong root — and the tool has to work on an install whose backend
// row will not resolve at all.
func hashFile(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // path derived from the catalog
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}
