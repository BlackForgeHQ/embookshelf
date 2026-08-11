// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
)

// dropStore is the slice of storage.Storage the S3 watcher needs.
type dropStore interface {
	List(ctx context.Context, prefix string) (storage.Iterator, error)
	Open(ctx context.Context, key string) (storage.Source, error)
	Delete(ctx context.Context, key string, opts ...storage.DeleteOption) error
}

// S3Watcher polls an S3 prefix and pulls supported objects through the
// existing BookDrop Accept seam: List → Open → Accept (which stages the
// bytes locally and registers the row under the wipe lock) → Delete.
//
// The delete happens only after Accept returns, so a crash in the
// window re-downloads the object next tick and stages a duplicate —
// loss is worse than duplication. Unsupported objects are left in the
// bucket and skipped silently, the local watcher's exact rule:
// deleting user files we don't understand is not our call.
//
// S3 makes this simpler than the local walk in one way: an object
// appears in List only when its PUT completed, so the growing-file
// race the local watcher tolerates cannot happen here.
type S3Watcher struct {
	Store    dropStore
	Prefix   string
	Interval time.Duration
	// Accept is BookDropService.Accept — the bytes-arriving-over-HTTP
	// seam, reused verbatim.
	Accept func(ctx context.Context, filename string, src io.Reader) (model.BookDropItem, error)

	// disabledReason is why the wiring decided this watcher must not
	// run — a prefix collision, a failed backend construction. Set via
	// Disable so the cause reaches Run's own "disabled" line instead of
	// living only in a log written hundreds of lines away (#304).
	disabledReason string
}

// Disable records why the watcher will not run. Run still starts and
// still logs its "disabled" line — carrying this reason — which is what
// lets the wiring always construct the watcher and keep the wiring
// parity test's every-field-non-nil property.
func (w *S3Watcher) Disable(reason string) { w.disabledReason = reason }

// Run blocks until ctx is canceled.
func (w *S3Watcher) Run(ctx context.Context) {
	if w.disabledReason != "" {
		slog.Info("s3 bookdrop watcher disabled", "reason", w.disabledReason)
		return
	}
	if w.Prefix == "" || w.Store == nil || w.Accept == nil {
		slog.Info("s3 bookdrop watcher disabled", "reason", "not configured")
		return
	}
	if w.Interval <= 0 {
		w.Interval = 60 * time.Second
	}
	slog.Info("s3 bookdrop watcher running", "prefix", w.Prefix, "interval", w.Interval)

	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()

	w.scan(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.scan(ctx)
		}
	}
}

func (w *S3Watcher) scan(ctx context.Context) {
	it, err := w.Store.List(ctx, w.Prefix)
	if err != nil {
		slog.Warn("s3 bookdrop list failed", "prefix", w.Prefix, "err", err)
		return
	}
	defer func() { _ = it.Close() }()
	for {
		if ctx.Err() != nil {
			return
		}
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			slog.Warn("s3 bookdrop list failed", "prefix", w.Prefix, "err", err)
			return
		}
		name := path.Base(obj.Key)
		if !fileproc.IsSupported(name) {
			continue
		}
		w.pull(ctx, obj.Key, name)
	}
}

// pull moves one object into local staging. Each step's failure leaves
// the object in place for the next tick — the only destructive step is
// last, after the bytes and the row both exist.
func (w *S3Watcher) pull(ctx context.Context, key, name string) {
	src, err := w.Store.Open(ctx, key)
	if err != nil {
		slog.Warn("s3 bookdrop open failed", "key", key, "err", err)
		return
	}
	_, err = w.Accept(ctx, name, io.NewSectionReader(src, 0, src.Size()))
	_ = src.Close()
	if err != nil {
		slog.Warn("s3 bookdrop accept failed", "key", key, "err", err)
		return
	}
	if err := w.Store.Delete(ctx, key); err != nil {
		// The bytes are staged and the row exists; the object will be
		// re-pulled next tick and staged again as a duplicate item. Say
		// so — a silent delete failure would look like a re-upload.
		slog.Warn("s3 bookdrop delete failed — expect a duplicate item next tick",
			"key", key, "err", err)
		return
	}
	slog.Info("s3 bookdrop pulled", "key", key)
}

// DropPrefixCollides reports whether the drop prefix would overlap a
// library prefix in the same bucket — the self-eating loop where the
// watcher ingests a library's own files as new drops, or a library
// scan adopts half-pulled drops. Either containment direction is a
// collision.
func DropPrefixCollides(dropPrefix, libraryPrefix string) bool {
	d := strings.Trim(dropPrefix, "/")
	l := strings.Trim(libraryPrefix, "/")
	if d == "" {
		return false
	}
	if l == "" {
		// A library rooted at the whole bucket contains any drop prefix.
		return true
	}
	return strings.HasPrefix(d+"/", l+"/") || strings.HasPrefix(l+"/", d+"/")
}
