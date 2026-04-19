package service

import (
	"context"
	"io"

	"github.com/blackforge/embookshelf/internal/model"
)

// DeviceDriver is the per-kind strategy for pairing + pushing books. One
// implementation per DeviceKind. Pair returns the device-shaped fields to
// store (secret + config); the service builds the row around it.
type DeviceDriver interface {
	Kind() model.DeviceKind

	// Pair runs the one-off handshake. `params` is driver-specific —
	// reMarkable takes the one-time-code and an app-generated UUID; a
	// Kindle driver would take an email address; etc. The returned
	// device carries Name/Secret/Config ready to be persisted.
	Pair(ctx context.Context, params map[string]any) (model.Device, error)

	// Send pushes a single book file to the paired device. `content` is
	// the book bytes, `meta` is the metadata (title/author/format) the
	// driver may need to name the upload on the remote side.
	Send(ctx context.Context, device model.Device, content io.Reader, meta BookMeta) error
}

// BookMeta is the slice of book state a device driver cares about.
type BookMeta struct {
	Title  string
	Author string
	Format string
	Size   int64
}
