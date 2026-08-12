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

	"github.com/blackforge/embookshelf/internal/storage"
)

// The naming and moving primitives shared by the two modules that put
// book bytes where they belong: the Placer (naming a destination for a
// new arrival) and the FolderRenamer (moving an existing Book to match
// its metadata). Both need "this name, unless it is taken" and "move
// this file without clobbering"; neither should have to read the
// other's module to get them (#256).

// moveFile atomically moves src to dest, falling back to copy+remove
// when os.Rename can't cross filesystems. The destination directory
// is created as needed.
func moveFile(src, dest string) error {
	if src == dest {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if err := os.Rename(src, dest); err == nil {
		return nil
	}
	if err := copyFile(src, dest); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil {
		// Leave the copy in place so the DB row still points to a
		// valid file; log so the admin can reap the source manually.
		slog.Warn("copy succeeded but source remove failed",
			"src", src, "err", err)
	}
	return nil
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open src: %w", err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create dest: %w", err)
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("fsync: %w", err)
	}
	return nil
}

// uniqueDestination walks " (2)", " (3)", … suffixes until it finds a
// free name. Preserves the original extension. Returns the input
// unchanged if it doesn't already exist.
func uniqueDestination(dest string) string {
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		return dest
	}
	dir := filepath.Dir(dest)
	ext := filepath.Ext(dest)
	base := strings.TrimSuffix(filepath.Base(dest), ext)
	for i := 2; i < 10_000; i++ {
		cand := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
	return dest
}

// uniqueDirectory walks " (2)", " (3)", … suffixes on a directory path
// until it finds one that does not already exist — the guard against
// placing one Book's files into another Book's folder when
// `{Author}/{Title}/` collides between Books.
func uniqueDirectory(dir string) string {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return dir
	}
	parent := filepath.Dir(dir)
	base := filepath.Base(dir)
	for i := 2; i < 10_000; i++ {
		cand := filepath.Join(parent, fmt.Sprintf("%s (%d)", base, i))
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
	return dir
}

// uniqueDirectoryUnless is a variant of uniqueDirectory that returns
// the input unchanged when it equals the source (oldAbs). Used by the
// renamer to avoid bumping a target that is the same directory it is
// renaming from (a no-op rename) — though the pipeline's folder delta
// should have short-circuited that case already.
func uniqueDirectoryUnless(dest, oldAbs string) string {
	if dest == oldAbs {
		return dest
	}
	return uniqueDirectory(dest)
}

// uniqueBackendFolder is the object-store counterpart to
// uniqueDirectory. S3 has no native directories, so collision is
// detected by probing for any object under the candidate prefix via
// List(). The first prefix that returns no objects wins.
func uniqueBackendFolder(ctx context.Context, store storage.Storage, folder string) string {
	if !backendFolderHasObjects(ctx, store, folder) {
		return folder
	}
	for i := 2; i < 10_000; i++ {
		cand := folder + fmt.Sprintf(" (%d)", i)
		if !backendFolderHasObjects(ctx, store, cand) {
			return cand
		}
	}
	return folder
}

// backendFolderHasObjects returns true if any object exists under the
// given prefix. List errors are conservative — treat as "has objects"
// so we keep walking suffixes rather than overwrite. The trailing slash
// is added so the prefix matches a folder, not a basename.
func backendFolderHasObjects(ctx context.Context, store storage.Storage, folder string) bool {
	prefix := folder + "/"
	it, err := store.List(ctx, prefix)
	if err != nil {
		return true
	}
	defer func() { _ = it.Close() }()
	if _, err := it.Next(ctx); err == nil {
		return true
	} else if !errors.Is(err, io.EOF) {
		return true
	}
	return false
}
