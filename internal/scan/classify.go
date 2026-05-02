package scan

import (
	"sort"
	"strings"
)

// LeafBook is a folder under the library root that holds ≥1 supported
// file. Per ADR-0003 §3 / docs/spec/library-layout.spec.md §4.1, the
// scanner treats one LeafBook as one Book and uses its Files as the
// `files` rows under one `book_id`.
//
// Mixed is true when the folder contains BOTH supported files at its
// own level AND subdirectories that themselves contain supported files.
// Spec rule: treat the top level as a LeafBook with a depth-1 sweep
// (Files holds only the files at this level, not the subtree). Callers
// log a warning in this case and surface it in the admin scan-failures
// view.
type LeafBook struct {
	Folder string
	Files  []WalkEntry
	Mixed  bool
}

// Classification is the output of Classify: the WalkEntry slice split
// into root-depth Flat files (legacy single-file Books, lazy migration
// per ADR-0003 §5) and LeafBooks (the new per-folder grouping).
type Classification struct {
	// Flat are supported files sitting directly under the library
	// root with no enclosing folder. Each becomes its own legacy
	// single-file Book.
	Flat []WalkEntry
	// LeafBooks are folder-as-Book groupings. Order is deterministic
	// (lexicographic by folder path) so callers comparing scans see
	// stable diffs.
	LeafBooks []LeafBook
}

// Classify partitions entries into Flat files and LeafBooks per the
// rules in docs/spec/library-layout.spec.md §4.1. isSupported gates
// which files count toward LeafBook membership; non-supported entries
// (covers, sidecars, READMEs) are ignored for classification but the
// LeafBook caller can still find them on disk.
//
// Two-phase rule (top-down):
//   - Container: directory holds NO supported files at this level, only
//     subdirectories with supported files in their subtree. Recurse
//     into each subdirectory as a LeafBook candidate.
//   - LeafBook (pure): directory holds ≥1 supported file AND no
//     subdirectory in its subtree holds supported files. Files are all
//     supported files in the subtree (any depth).
//   - LeafBook (mixed): directory holds ≥1 supported file at this level
//     AND ≥1 subdirectory whose subtree holds supported files. Mixed=true.
//     Files are the supported files at this level only. Subdirectories
//     are then descended as their own LeafBook candidates.
//
// Inputs use forward slashes regardless of host OS (matches storage.Storage
// convention).
func Classify(entries []WalkEntry, isSupported func(loc string) bool) Classification {
	var supported []WalkEntry
	for _, e := range entries {
		if isSupported(e.Location) {
			supported = append(supported, e)
		}
	}

	// byDir maps a directory to the supported files DIRECTLY in it
	// (depth-1 children, no recursion).
	byDir := map[string][]WalkEntry{}
	for _, f := range supported {
		byDir[parentDir(f.Location)] = append(byDir[parentDir(f.Location)], f)
	}

	// dirHasFilesInSubtree[d] is true iff some descendant dir of d
	// (or d itself) directly holds supported files. Walk every dir
	// that holds direct files and mark its ancestors.
	dirHasFilesInSubtree := map[string]bool{}
	for d := range byDir {
		dirHasFilesInSubtree[d] = true
		for cur := parentDir(d); cur != ""; cur = parentDir(cur) {
			dirHasFilesInSubtree[cur] = true
		}
	}
	// Root sentinel ("") may or may not have direct files; handled below.

	out := Classification{Flat: byDir[""]}
	delete(byDir, "")

	// Sort dirs lex so traversal is deterministic and parents come
	// before children.
	dirs := make([]string, 0, len(dirHasFilesInSubtree))
	for d := range dirHasFilesInSubtree {
		if d == "" {
			continue
		}
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	consumed := map[string]bool{} // dirs already attached to a parent LeafBook
	for _, d := range dirs {
		if consumed[d] {
			continue
		}
		// Is any STRICT descendant of d a dir-with-direct-files?
		hasNestedFiles := false
		for child := range byDir {
			if child == d {
				continue
			}
			if isStrictDescendant(child, d) {
				hasNestedFiles = true
				break
			}
		}

		direct := byDir[d]
		switch {
		case len(direct) == 0 && hasNestedFiles:
			// Container — let the descendants be picked up as their
			// own LeafBooks. Nothing to emit here.
		case len(direct) > 0 && !hasNestedFiles:
			// Pure LeafBook: collect files at any depth under d.
			files := append([]WalkEntry{}, direct...)
			for child, dchildren := range byDir {
				if child == d || !isStrictDescendant(child, d) {
					continue
				}
				files = append(files, dchildren...)
				consumed[child] = true
				// Mark all descendants of `child` as consumed too —
				// they're part of this LeafBook.
				for inner := range byDir {
					if isStrictDescendant(inner, child) {
						consumed[inner] = true
					}
				}
			}
			sortEntries(files)
			out.LeafBooks = append(out.LeafBooks, LeafBook{Folder: d, Files: files})
			consumed[d] = true
		case len(direct) > 0 && hasNestedFiles:
			// Mixed LeafBook: depth-1 sweep only.
			files := append([]WalkEntry{}, direct...)
			sortEntries(files)
			out.LeafBooks = append(out.LeafBooks, LeafBook{
				Folder: d, Files: files, Mixed: true,
			})
			// Don't mark descendants consumed — they become their
			// own LeafBooks via subsequent iterations.
		}
	}

	sort.Slice(out.LeafBooks, func(i, j int) bool {
		return out.LeafBooks[i].Folder < out.LeafBooks[j].Folder
	})
	sortEntries(out.Flat)
	return out
}

// parentDir returns the directory portion of loc, using forward
// slashes. Returns "" when loc has no slash (root-depth file).
func parentDir(loc string) string {
	idx := strings.LastIndex(loc, "/")
	if idx < 0 {
		return ""
	}
	return loc[:idx]
}

// isStrictDescendant reports whether path d sits strictly under
// ancestor a. Strict means a != d. Both inputs are slash-separated
// directory paths, no trailing slash.
func isStrictDescendant(d, a string) bool {
	if d == a {
		return false
	}
	if a == "" {
		return true
	}
	return strings.HasPrefix(d, a+"/")
}

// sortEntries orders entries lex by Location for deterministic output.
func sortEntries(es []WalkEntry) {
	sort.Slice(es, func(i, j int) bool { return es[i].Location < es[j].Location })
}
