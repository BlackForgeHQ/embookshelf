// SPDX-License-Identifier: AGPL-3.0-or-later

package jobs

import (
	"context"
	"errors"
	"sync"
)

// ErrNoQueue is what a Deferred returns before it has been resolved.
var ErrNoQueue = errors.New("no queue configured")

// Deferred is an Enqueuer whose backing client arrives after the
// services holding it are constructed.
//
// This is the one irreducible knot in the composition root, and naming
// it once is the whole point. The queue's worker registry is assembled
// inside queue.New out of the very services that need to enqueue, so
// neither can be built first. Four modules used to each re-derive a
// workaround for that; now they take an ordinary Enqueuer and this
// holds the knot alone.
//
// The mutex is not decoration. queue.New calls river.Client.Start
// before it returns, so worker goroutines are already draining jobs
// while the composition root is still calling Resolve.
type Deferred struct {
	mu    sync.RWMutex
	inner Enqueuer
}

// Resolve supplies the real enqueuer. Called once, by the composition
// root, as soon as the queue exists.
func (d *Deferred) Resolve(e Enqueuer) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.inner = e
}

// Enqueue hands the job on, or refuses if the queue is not up yet.
//
// Refusing rather than dropping is deliberate: the caller decides what
// an unqueueable job means. A bookdrop intake logs it and lets the
// watcher retry; an audiobook run fails outright, because a run with no
// jobs shows 0% forever with no error to explain it.
func (d *Deferred) Enqueue(ctx context.Context, args Args) error {
	d.mu.RLock()
	inner := d.inner
	d.mu.RUnlock()
	if inner == nil {
		return ErrNoQueue
	}
	return inner.Enqueue(ctx, args)
}
