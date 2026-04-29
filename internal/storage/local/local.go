// Package local implements storage.Storage against a local filesystem
// rooted at a configurable absolute path. Keys are interpreted as
// slash-separated paths relative to the root; the implementation
// translates them to filesystem paths via filepath.FromSlash and
// guards against parent traversal.
package local

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/blackforge/embookshelf/internal/storage"
)

// LocalFS is a Storage backed by an OS filesystem.
type LocalFS struct {
	root string
}

// New returns a LocalFS rooted at root. root must be absolute.
func New(root string) (*LocalFS, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("local: root must be absolute, got %q", root)
	}
	return &LocalFS{root: filepath.Clean(root)}, nil
}

// Capabilities reports the features LocalFS supports. None of the
// optional capabilities are implemented in Plan A.
func (fs *LocalFS) Capabilities() storage.Capability { return 0 }

// resolve translates a slash-separated key into an absolute filesystem
// path under fs.root, rejecting anything that would escape the root.
func (fs *LocalFS) resolve(key string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(key))
	abs := filepath.Join(fs.root, clean)
	rel, err := filepath.Rel(fs.root, abs)
	if err != nil || rel == ".." || (len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator)) {
		return "", storage.ErrInvalidKey
	}
	return abs, nil
}

// Stub implementations follow in subsequent tasks.
func (fs *LocalFS) List(ctx context.Context, prefix string) (storage.Iterator, error) {
	return nil, fmt.Errorf("not implemented")
}
func (fs *LocalFS) Head(ctx context.Context, key string) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, fmt.Errorf("not implemented")
}
func (fs *LocalFS) Get(ctx context.Context, key string, opts ...storage.GetOption) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not implemented")
}
// Put writes r to key atomically using write-temp-then-rename.
// LocalFS does not support conditional writes (CapConditional is off);
// passing WithIfMatch or WithIfNoneMatch returns ErrUnsupportedOption.
func (fs *LocalFS) Put(ctx context.Context, key string, r io.Reader, opts ...storage.PutOption) (storage.PutResult, error) {
	o := storage.ApplyPut(opts)
	if o.IfMatchSet || o.IfNoneMatchSet {
		return storage.PutResult{}, storage.ErrUnsupportedOption
	}
	abs, err := fs.resolve(key)
	if err != nil {
		return storage.PutResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return storage.PutResult{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), filepath.Base(abs)+".*.tmp")
	if err != nil {
		return storage.PutResult{}, err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		return storage.PutResult{}, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return storage.PutResult{}, err
	}
	if err := tmp.Close(); err != nil {
		return storage.PutResult{}, err
	}
	if err := os.Rename(tmpName, abs); err != nil {
		return storage.PutResult{}, err
	}
	return storage.PutResult{}, nil
}
func (fs *LocalFS) Delete(ctx context.Context, key string, opts ...storage.DeleteOption) error {
	return fmt.Errorf("not implemented")
}
func (fs *LocalFS) Copy(ctx context.Context, srcKey, dstKey string) (storage.CopyResult, error) {
	return storage.CopyResult{}, fmt.Errorf("not implemented")
}
