package sidecar

import (
	"bytes"
	"context"
	"sync"

	"github.com/blackforge/embookshelf/internal/storage"
)

// Writer serializes sidecar writes per-key. Two writes targeting the
// same key block one after the other; writes to different keys run
// concurrently. The underlying storage.Put is already atomic
// (write-temp-then-rename on LocalFS); the Writer's job is to
// linearize multiple in-flight writes from the same process.
//
// Multi-process / multi-instance coordination is out of scope for
// Plan D; conditional PUT lands in Plan F.
type Writer struct {
	locks sync.Map // map[string]*sync.Mutex
}

// NewWriter constructs a fresh writer.
func NewWriter() *Writer { return &Writer{} }

// keyLock returns the per-key mutex, creating it on first reference.
func (w *Writer) keyLock(key string) *sync.Mutex {
	actual, _ := w.locks.LoadOrStore(key, &sync.Mutex{})
	return actual.(*sync.Mutex)
}

// Write encodes s as TOML and stores it at key. Concurrent calls for
// the same key are serialized; calls for different keys run in
// parallel. The encoded bytes are written via storage.Put so the
// underlying backend's atomic-write semantics apply.
func (w *Writer) Write(ctx context.Context, store storage.Storage, key string, s Sidecar) error {
	data, err := EncodeTOML(s)
	if err != nil {
		return err
	}
	mu := w.keyLock(key)
	mu.Lock()
	defer mu.Unlock()
	_, err = store.Put(ctx, key, bytes.NewReader(data), storage.WithContentType("application/toml"))
	return err
}
