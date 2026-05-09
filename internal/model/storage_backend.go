// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import "time"

// StorageBackend identifies a backend (filesystem or S3-compatible)
// that one or more libraries can be rooted under. Config is the
// JSON-encoded backend-specific configuration. For kind="local" it
// has a single key {"root": "/abs/path"}; for "s3" it carries
// {bucket, region, endpoint, prefix} (Plan F).
type StorageBackend struct {
	ID        string
	Kind      string // "local" | "s3"
	Config    map[string]any
	CreatedAt time.Time
}
