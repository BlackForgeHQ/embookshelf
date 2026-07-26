// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ingest scans the BookDrop directory and enqueues processing jobs
// for new files. Uses polling (simple, robust across NFS/SMB mounts) rather
// than fsnotify.
package ingest

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/blackforge/embookshelf/internal/queue"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/task"
)

// Watcher polls a directory and enqueues new files into the BookDrop pipeline.
type Watcher struct {
	Path     string
	Interval time.Duration
	Svc      *service.BookDropService
}

// Run blocks until ctx is canceled. On each tick it walks the configured path
// and hands off any unseen supported files to the ingest service.
func (w *Watcher) Run(ctx context.Context) {
	if w.Path == "" {
		slog.Info("bookdrop watcher disabled (empty path)")
		return
	}
	if w.Interval <= 0 {
		w.Interval = 5 * time.Second
	}

	if err := os.MkdirAll(w.Path, 0o755); err != nil {
		slog.Warn("bookdrop mkdir failed — watcher disabled", "path", w.Path, "err", err)
		return
	}

	slog.Info("bookdrop watcher running", "path", w.Path, "interval", w.Interval)
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()

	// Scan once immediately so already-present files are picked up on boot.
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

func (w *Watcher) scan(ctx context.Context) {
	err := filepath.WalkDir(w.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip unreadable entries — don't abort the whole scan.
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// Intake owns the rest: the supported-format gate, the stat, the
		// row, and the worker handoff — all under the wipe lock, taken per
		// file so a wipe isn't blocked for the length of the walk.
		if _, _, err := w.Svc.Intake(ctx, path); err != nil {
			if !errors.Is(err, service.ErrUnsupportedFormat) {
				slog.Warn("bookdrop intake failed", "path", path, "err", err)
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Warn("bookdrop scan failed", "err", err)
	}
}

// DiscoverOnStartup re-queues items that were persisted in 'discovered' or
// 'processing' state but whose river job didn't complete before shutdown.
// Call once after the queue.Client is ready.
func DiscoverOnStartup(ctx context.Context, bdropRepo *repo.BookDropRepo, q queue.Client) {
	items, err := bdropRepo.List(ctx)
	if err != nil {
		slog.Warn("bookdrop re-discover failed", "err", err)
		return
	}
	for _, it := range items {
		if it.State == "discovered" || it.State == "processing" {
			if err := q.Enqueue(ctx, task.BookDropIngestArgs{ItemID: it.ID}); err != nil {
				slog.Warn("bookdrop re-enqueue failed", "id", it.ID, "err", err)
			}
		}
	}
}
