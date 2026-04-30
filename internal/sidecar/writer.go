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
type Writer struct {
	locks sync.Map // map[string]*sync.Mutex
}

func NewWriter() *Writer { return &Writer{} }

func (w *Writer) keyLock(key string) *sync.Mutex {
	actual, _ := w.locks.LoadOrStore(key, &sync.Mutex{})
	return actual.(*sync.Mutex)
}

// Write encodes s as a v1 JSON envelope and stores it at key. mode and
// format describe the envelope; readers use them only for diagnostics
// and unknown-version handling.
func (w *Writer) Write(
	ctx context.Context,
	store storage.Storage,
	key string,
	s Sidecar,
	mode WriteMode,
	format string,
) error {
	data, err := EncodeJSON(s, mode, format)
	if err != nil {
		return err
	}
	mu := w.keyLock(key)
	mu.Lock()
	defer mu.Unlock()
	_, err = store.Put(ctx, key, bytes.NewReader(data), storage.WithContentType("application/json"))
	return err
}
