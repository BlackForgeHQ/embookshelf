// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/blackforge/embookshelf/internal/model"
)

// bookFiles is the files-table seam of the LibraryHandle: the four
// lookups that turn a book into its rows, over the narrow lister, with
// the absence policy stated once instead of decided at each method
// (#346 — a nil lister used to answer "no files" four separately-written
// ways, indistinguishable from the truth at every one).
//
// The one policy: a nil lister is an install whose files table is not
// wired (LibraryStoreDeps.Files nil), and it answers "this book has no
// rows" everywhere — the degrade every caller already carries for a
// legacy book that genuinely has none (books.path only). A lookup that
// *failed* is never folded into that answer where the caller can act on
// the difference: primary keeps its error, locations keeps its error;
// only byID and primaryHash fold, because their callers treat the
// answer as advisory (a serve falls through to not-found, a hash reads
// as fresh).
type bookFiles struct {
	lister BookFileLister
}

// locations lists the storage keys belonging to a book. A nil error
// with no keys is the normal answer for a book whose files were never
// backfilled.
func (bf bookFiles) locations(ctx context.Context, bookID string) ([]string, error) {
	if bf.lister == nil {
		return nil, nil
	}
	list, err := bf.lister.ListByBook(ctx, bookID)
	if err != nil {
		return nil, fmt.Errorf("list files for book %s: %w", bookID, err)
	}
	out := make([]string, 0, len(list))
	for _, f := range list {
		if f.Location != "" {
			out = append(out, f.Location)
		}
	}
	return out, nil
}

// byID returns one of a book's files rows by id.
func (bf bookFiles) byID(ctx context.Context, bookID, fileID string) (model.File, bool) {
	if bf.lister == nil || fileID == "" {
		return model.File{}, false
	}
	list, err := bf.lister.ListByBook(ctx, bookID)
	if err != nil {
		return model.File{}, false
	}
	for _, f := range list {
		if f.ID == fileID {
			return f, true
		}
	}
	return model.File{}, false
}

// primaryRow is primary for the callers that want the pre-backfill
// state as an error rather than a bool — the byte-open and delivery
// paths, whose next move is a books.path fallback either way. A nil
// lister errors like a book with no rows: both mean "nothing to open
// through the files table".
func (bf bookFiles) primaryRow(ctx context.Context, book model.Book) (model.File, error) {
	if bf.lister == nil {
		return model.File{}, errors.New("files table not wired")
	}
	return primaryFile(ctx, bf.lister, book)
}

// primary returns the book's own files row — the one whose format
// matches books.format. found separates "no rows at all" (a legacy row
// carrying only books.path, which callers degrade for) from an error,
// which is a lookup that broke and must not be read as an absent file.
func (bf bookFiles) primary(ctx context.Context, book model.Book) (model.File, bool, error) {
	if bf.lister == nil {
		return model.File{}, false, nil
	}
	return lookUpPrimaryFile(ctx, bf.lister, book)
}

// primaryHash is the hash of the book's own file — the thing a
// narration is made from rather than the narration itself. Folds every
// absence into nil because the hash is advisory: callers read an empty
// hash as fresh (model.Stale).
func (bf bookFiles) primaryHash(ctx context.Context, book model.Book) []byte {
	if bf.lister == nil {
		return nil
	}
	f, err := primaryFile(ctx, bf.lister, book)
	if err != nil {
		return nil
	}
	return f.ContentHash
}
