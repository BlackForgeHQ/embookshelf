// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

var (
	// ErrUnsupportedFormat is returned when a file's extension is not one
	// the processor can read. Checked before anything is written or
	// inserted.
	ErrUnsupportedFormat = errors.New("unsupported format")
	// ErrBookDropDisabled is returned by Accept when no staging directory
	// is configured — there is nowhere to put an upload.
	ErrBookDropDisabled = errors.New("bookdrop is disabled (no BOOKDROP_PATH configured)")
)

// bookdropInserter is the slice of BookDropRepo the intake path touches.
// Narrow so the whole sequence — lock, validate, write, insert, dispatch —
// is testable against a temp dir and a fake, with no database.
type bookdropInserter interface {
	Insert(ctx context.Context, path, format string, size int64) (model.BookDropItem, error)
}

// Intake registers a file that is already sitting in the staging directory.
// The watcher's path: it walks the tree and hands over whatever it finds.
//
// Returns created=false with a nil error when the file is already tracked,
// which is the common case on every tick after the first.
//
// The wipe read-lock is held for the whole call — stat, insert and dispatch —
// so a wipe cannot delete the bytes out from under a row being written. Taken
// per file rather than per scan: the invariant that matters is "a row's file
// exists at the moment the row is inserted", and holding it across an entire
// directory walk would block a wipe for the length of the walk.
func (s *BookDropService) Intake(ctx context.Context, path string) (model.BookDropItem, bool, error) {
	s.wipeMu.RLock()
	defer s.wipeMu.RUnlock()

	if !fileproc.IsSupported(path) {
		return model.BookDropItem{}, false, fmt.Errorf("%q: %w", filepath.Base(path), ErrUnsupportedFormat)
	}
	// Stat under the lock rather than trusting a size the caller measured
	// earlier: between the caller's look and here the file may have been
	// wiped, or may still be growing.
	info, err := os.Stat(path)
	if err != nil {
		return model.BookDropItem{}, false, fmt.Errorf("stat staged file: %w", err)
	}
	if info.IsDir() {
		return model.BookDropItem{}, false, fmt.Errorf("%q: %w", filepath.Base(path), ErrUnsupportedFormat)
	}
	return s.register(ctx, path, info.Size())
}

// Accept saves an uploaded file into the staging directory and registers it.
// The HTTP path: bytes arrive from a client and have nowhere to live yet.
//
// The write and the insert happen under one hold of the wipe read-lock. Doing
// the write first and locking afterwards would leave a window in which a wipe
// deletes the file and finds no row to sweep, so the row lands pointing at
// nothing — an item that can never process and that the user cannot clear.
//
// filename is attacker-controlled and is treated as a suggestion only: it is
// reduced to its base, stripped of leading dots, and stamped, so the bytes
// always land directly in the staging directory under a fresh name.
func (s *BookDropService) Accept(ctx context.Context, filename string, src io.Reader) (model.BookDropItem, error) {
	s.wipeMu.RLock()
	defer s.wipeMu.RUnlock()

	if s.bookdropPath == "" {
		return model.BookDropItem{}, ErrBookDropDisabled
	}
	name := filepath.Base(filename)
	if !fileproc.IsSupported(name) {
		return model.BookDropItem{}, fmt.Errorf("%q: %w", name, ErrUnsupportedFormat)
	}
	if err := os.MkdirAll(s.bookdropPath, 0o755); err != nil {
		return model.BookDropItem{}, fmt.Errorf("create staging dir: %w", err)
	}
	dest, size, err := writeStaged(s.bookdropPath, name, src)
	if err != nil {
		return model.BookDropItem{}, err
	}
	item, _, err := s.register(ctx, dest, size)
	if err != nil {
		// Bytes with no row are invisible in the UI but still occupy the
		// staging directory, and the watcher would later adopt them as an
		// item the user never asked for.
		if rmErr := os.Remove(dest); rmErr != nil {
			slog.Warn("remove orphaned upload", "path", dest, "err", rmErr)
		}
		return model.BookDropItem{}, err
	}
	return item, nil
}

// register inserts the row and hands the item to the worker pool. Callers
// must already hold the wipe read-lock.
func (s *BookDropService) register(ctx context.Context, path string, size int64) (model.BookDropItem, bool, error) {
	format := fileproc.FormatForExt(filepath.Ext(path))
	item, err := s.inserter().Insert(ctx, path, format, size)
	if err != nil {
		if errors.Is(err, repo.ErrAlreadyExists) {
			return item, false, nil
		}
		return item, false, err
	}
	s.broadcast(item.ID)
	// The row is committed. Losing the job only delays processing; the
	// watcher re-dispatches on its next tick, and DiscoverOnStartup
	// catches anything still stranded at boot.
	if err := s.enq.Enqueue(ctx, jobs.BookDropIngestArgs{ItemID: item.ID}); err != nil {
		slog.Error("dispatch bookdrop ingest", "item_id", item.ID, "err", err)
	}
	return item, true, nil
}

// inserter returns the narrow insert seam, defaulting to the real repo.
func (s *BookDropService) inserter() bookdropInserter {
	if s.intake != nil {
		return s.intake
	}
	return s.bdrop
}

// stagingStem is the part of a client-supplied name reused for the staged
// file. Leading dots are stripped so an upload called ".epub" becomes
// "upload-<stamp>.epub" rather than a dotfile the operator never sees in the
// staging directory, and an empty stem falls back to a placeholder.
func stagingStem(name, ext string) string {
	stem := strings.TrimLeft(strings.TrimSuffix(name, ext), ".")
	if stem == "" {
		return "upload"
	}
	return stem
}

// writeStaged writes src to a fresh file in dir and reports the path and the
// number of bytes actually written — not any size the client claimed. The
// name is only a suggestion: the file always lands directly in dir under a
// stamped name, so a traversal attempt in the original cannot escape.
func writeStaged(dir, name string, src io.Reader) (string, int64, error) {
	ext := strings.ToLower(filepath.Ext(name))
	stem := stagingStem(name, filepath.Ext(name))
	dest := filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, time.Now().UnixNano(), ext))

	dst, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", 0, fmt.Errorf("create staged file: %w", err)
	}
	n, err := io.Copy(dst, src)
	if err != nil {
		_ = dst.Close()
		_ = os.Remove(dest)
		return "", 0, fmt.Errorf("write staged file: %w", err)
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(dest)
		return "", 0, fmt.Errorf("close staged file: %w", err)
	}
	return dest, n, nil
}
