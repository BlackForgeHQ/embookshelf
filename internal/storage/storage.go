// SPDX-License-Identifier: AGPL-3.0-or-later

// Package storage defines a backend-agnostic interface for reading and
// writing book bytes and sidecar files. Implementations live in
// subpackages (local, s3). The DB layer never touches files directly —
// it calls this interface.
//
// Keys are slash-separated paths relative to the backend's configured
// root. The interface is intentionally minimal; capability-gated
// extensions (presigned URLs, storage class, change notifications) are
// declared via Capability bits and live on backend-specific types
// returned by type assertion.
package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// Source is the random-access byte view of an object. Returned by
// Storage.Open. Distinct from Storage.Get (which returns a sequential
// io.ReadCloser) — Source is for callers that need to seek to read a
// container format's directory or footer.
//
// Size is the total object size in bytes. Implementations must return
// the same value for repeated calls.
type Source interface {
	io.ReaderAt
	io.Closer
	Size() int64
}

// ObjectInfo is the metadata returned by List and Head.
type ObjectInfo struct {
	// Key is the object's key relative to the backend root.
	Key string
	// Size is the object size in bytes. -1 means unknown.
	Size int64
	// ETag is an opaque change token. "" when the backend does not
	// expose one (e.g., LocalFS). Never use ETag as a content hash.
	ETag string
	// ModTime is the object's last-modified time.
	ModTime time.Time
	// ContentType is best-effort. "" when unknown. Backends may not
	// persist this faithfully.
	ContentType string
}

// Capability is a bitset of optional features a backend may advertise.
// Callers gate optional code paths with Storage.Capabilities() & Cap*.
type Capability uint32

const (
	// CapPresign indicates the backend can issue presigned URLs.
	CapPresign Capability = 1 << iota
	// CapStorageClass indicates objects can be tagged with a storage
	// class (S3 standard / IA / glacier).
	CapStorageClass
	// CapVersioning indicates the backend stores prior versions of
	// overwritten objects.
	CapVersioning
	// CapNotify indicates the backend can stream change events.
	CapNotify
	// CapConditional indicates the backend supports If-Match /
	// If-None-Match preconditions on Put.
	CapConditional
	// CapRange indicates the backend supports byte-range reads on Get.
	CapRange
)

// PutResult is returned by Storage.Put.
type PutResult struct {
	ETag      string
	VersionID string
}

// CopyResult is returned by Storage.Copy.
type CopyResult struct {
	ETag string
}

// Iterator yields objects from List. Callers must Close it.
type Iterator interface {
	// Next returns the next object. Returns io.EOF when the iteration
	// is exhausted. Returning a non-EOF error invalidates the iterator;
	// callers should still Close.
	Next(ctx context.Context) (ObjectInfo, error)
	// Close releases iterator resources. Safe to call multiple times.
	Close() error
}

// Storage is the backend-agnostic interface. All keys are relative to
// the backend's configured root and use forward slashes regardless of
// host OS.
type Storage interface {
	// Capabilities reports which optional features this backend supports.
	Capabilities() Capability

	// List walks the backend recursively under prefix. An empty prefix
	// lists from the root. Iteration order is unspecified.
	List(ctx context.Context, prefix string) (Iterator, error)

	// Head returns metadata for a single key. Returns ErrNotFound when
	// the key does not exist.
	Head(ctx context.Context, key string) (ObjectInfo, error)

	// Get returns a stream for the given key. The returned ReadCloser
	// must be Closed by the caller. Returns ErrNotFound when missing.
	Get(ctx context.Context, key string, opts ...GetOption) (io.ReadCloser, error)

	// Put writes r to key. The reader is consumed in full (no length
	// hint required). Conditional options (WithIfMatch / WithIfNoneMatch)
	// return ErrPreconditionFailed when the precondition is not met,
	// or ErrUnsupportedOption when the backend lacks CapConditional.
	Put(ctx context.Context, key string, r io.Reader, opts ...PutOption) (PutResult, error)

	// Delete removes a key. Removing a missing key is not an error.
	Delete(ctx context.Context, key string, opts ...DeleteOption) error

	// Copy duplicates srcKey to dstKey. On LocalFS this is rename(2)
	// when src and dst share a filesystem, falling back to copy + unlink.
	// On S3 it is a server-side copy.
	Copy(ctx context.Context, srcKey, dstKey string) (CopyResult, error)

	// Open returns a random-access view of the object at key. Returns
	// ErrNotFound when missing. Callers must Close the returned Source.
	Open(ctx context.Context, key string) (Source, error)
}

// Sentinel errors. Backends wrap their underlying error with
// errors.Join(ErrXxx, original) so callers can use errors.Is.
var (
	ErrNotFound           = errors.New("storage: not found")
	ErrPreconditionFailed = errors.New("storage: precondition failed")
	ErrUnsupportedOption  = errors.New("storage: unsupported option for this backend")
	ErrInvalidKey         = errors.New("storage: invalid key")
)
