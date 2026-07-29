// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/blackforge/embookshelf/internal/model"
)

// Moving a legacy flat-layout Book into the folder layout is a lazy
// migration, not an edit-side write step (ADR-0003 §5).
//
// It shared a function with the folder rename and shared nothing else
// with it: a rename moves a folder that exists, this one builds the
// first folder a Book has ever had, out of files sitting loose at the
// library root beside other Books' files. The two were one function
// because both are triggered by the same author-or-title edit (#212).

// migrateToFolderLayout gives a flat-layout Book its own folder.
//
// Only this Book's files move. Sibling files at the library root belong
// to other flat-layout Books and must not be swept along, which is the
// whole reason this cannot be a prefix move.
func (w *MetadataWriter) migrateToFolderLayout(
	ctx context.Context,
	b model.Book,
	handle *LibraryHandle,
	newFolder string,
) RenameOutcome {
	if handle.IsObjectStore() {
		// S3 BackendPlacer has always written {Author}/{Title} prefixes
		// (ADR-0003 §7), so a folder_path of "" on an S3-backed Book is
		// a pre-storage_v2 row or data corruption rather than a
		// flat-layout book. There is nothing to list and nothing to
		// migrate.
		return renameDeclined("object-store book has no flat layout to migrate")
	}
	libRoot := strings.TrimRight(handle.Library.Path, "/")
	if libRoot == "" {
		return renameDeclined("library has no root configured")
	}

	finalAbs := uniqueDirectory(filepath.Join(libRoot, newFolder))
	finalFolder := strings.TrimPrefix(finalAbs, libRoot+"/")
	if err := os.MkdirAll(finalAbs, 0o755); err != nil {
		slog.Warn("metadata writer: mkdir new folder",
			"book_id", b.ID, "dir", finalAbs, "err", err)
		return renameBroke(err)
	}
	if err := w.moveFlatFiles(ctx, b, libRoot, finalAbs); err != nil {
		return renameBroke(err)
	}
	if err := w.persistRename(ctx, b, finalFolder); err != nil {
		// The files moved and the DB still says otherwise. A known
		// soft-failure; scan reattach corrects it by content hash.
		return renameBroke(err)
	}
	return renameDone(finalFolder)
}

// moveFlatFiles handles the lazy-migration case for a legacy
// flat-layout Book. Moves every files row's on-disk entry from
// `{libRoot}/{basename}` into `{newDir}/{basename}` so we don't
// scoop up siblings that belong to other Books.
func (w *MetadataWriter) moveFlatFiles(
	ctx context.Context,
	b model.Book,
	libRoot, newDir string,
) error {
	files, err := w.deps.Files.ListByBook(ctx, b.ID)
	if err != nil {
		slog.Warn("metadata writer: list files for flat move",
			"book_id", b.ID, "err", err)
		return err
	}
	// No rows is not the same as no files: an un-scanned Book still has
	// its primary path, and that is what there is to move.
	if len(files) == 0 {
		return w.moveSingleFile(b, libRoot, newDir)
	}
	for _, f := range files {
		// Flat-layout files live directly under the library root
		// with location = filename. Bail if a non-flat file shows
		// up — should not happen but keeps us honest.
		if strings.Contains(f.Location, "/") {
			slog.Warn("metadata writer: skipping non-flat file row in flat-rename",
				"book_id", b.ID, "location", f.Location)
			continue
		}
		from := filepath.Join(libRoot, f.Location)
		to := filepath.Join(newDir, f.Location)
		if err := moveFile(from, to); err != nil {
			slog.Warn("metadata writer: move flat file",
				"book_id", b.ID, "from", from, "to", to, "err", err)
			return err
		}
	}
	return nil
}

// moveSingleFile is the no-files-repo fallback for the flat-layout
// rename: move just the Book's primary file (b.Path) into the new
// folder.

// moveSingleFile moves just the Book's primary file (b.Path) into the
// new folder, for a Book whose files have not been enumerated into rows
// yet.
//
// It used to double as the fallback for a nil Files repo. No production
// wiring produces one — internal/app always supplies it — so that branch
// was unreachable, and it was the only thing keeping the flat move able
// to proceed without the repo it needs (#212).
func (w *MetadataWriter) moveSingleFile(b model.Book, libRoot, newDir string) error {
	base := filepath.Base(b.Path)
	if base == "" || base == "." || base == "/" {
		return fmt.Errorf("book %s has no usable primary path %q", b.ID, b.Path)
	}
	from := filepath.Join(libRoot, b.Path)
	if filepath.IsAbs(b.Path) {
		from = b.Path
	}
	to := filepath.Join(newDir, base)
	if err := moveFile(from, to); err != nil {
		slog.Warn("metadata writer: move single flat file",
			"book_id", b.ID, "from", from, "to", to, "err", err)
		return err
	}
	return nil
}

// persistRename updates files.location for every files row of the
// Book and books.folder_path + books.path so the DB reflects the
// post-rename layout. Best-effort: per-file failures log and
// continue; the books-row update is the last write so a crash
// midway leaves only file rows pointing at stale locations, which
// scan reattach corrects on next pass.
