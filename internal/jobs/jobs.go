// SPDX-License-Identifier: AGPL-3.0-or-later

// Package jobs is the vocabulary both the service tier and the queue
// tier speak: what a job payload is, and what it means to enqueue one.
//
// It exists to be a leaf. internal/queue imports internal/service, so
// the service tier cannot name a queue client — which is why four
// separate modules each grew their own function-typed dispatcher and
// the same comment explaining it. Declaring the seam somewhere both
// tiers can depend on turns those into one ordinary argument.
package jobs

import "context"

// Args is one job's payload: a JSON-serializable struct that names its
// own kind.
//
// The kind is a stored value, not a derived one — River persists it
// alongside the encoded args — so renaming a Go type here is safe and
// changing a Kind() string orphans every in-flight job of that type.
type Args interface {
	Kind() string
}

// Queued is the optional half of Args: a job that names a queue runs
// there instead of the default one. Declared as an interface rather
// than a field so the name travels with the payload exactly as Kind
// does.
type Queued interface {
	Queue() string
}

// Enqueuer hands a job to the worker pool.
//
// One method for every job type: the kind travels with the payload, so
// adding a job does not widen this interface.
type Enqueuer interface {
	Enqueue(ctx context.Context, args Args) error
}
