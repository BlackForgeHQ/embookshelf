// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"context"
	"time"
)

// The capability extensions: interfaces a backend satisfies beyond
// Storage, reached by type assertion at the call site that wants the
// extra behaviour. Declared here — in the package the adapters import —
// so an adapter can assert its own conformance at compile time (#345).
// Presigner used to live in the service tier, satisfied by an adapter
// that could not see it: signature drift there was silent, because the
// failed assertion fell through to streaming and read as a performance
// regression rather than an error.

// Presigner is the CapPresign extension: a backend that can issue a
// URL a browser fetches directly, bypassing the app server.
type Presigner interface {
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// PrefixMover is the copy-based-move extension. A backend with no
// atomic rename (S3) relocates a prefix by copying and reports what the
// move left behind in a MoveResult, so a caller with a database
// transaction can reclaim Written on rollback and Reclaim on commit
// (ADR-0005). An atomic backend (LocalFS) has nothing to report and
// does not implement this — the interface's MovePrefix is the whole
// story there.
type PrefixMover interface {
	MovePrefixDetailed(ctx context.Context, oldPrefix, newPrefix string) (MoveResult, error)
}
