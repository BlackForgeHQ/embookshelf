package task

import (
	"context"
	"log/slog"
	"time"

	"github.com/blackforge/embookshelf/internal/repo"
)

// MissingTTL is the grace period before missing files are purged.
// Spec §5.3 sets this at 24h to ride out unmounted drives, S3 region
// blips, and IAM hiccups.
const MissingTTL = 24 * time.Hour

// RunMissingPurge deletes files rows whose missing_since is older
// than MissingTTL. Returns the count purged.
func RunMissingPurge(ctx context.Context, files *repo.FileRepo) (int64, error) {
	if files == nil {
		return 0, nil
	}
	return files.DeleteMissingOlderThan(ctx, MissingTTL)
}

// LoopMissingPurge runs RunMissingPurge on a ticker until ctx is
// cancelled. Errors are logged but do not stop the loop.
func LoopMissingPurge(ctx context.Context, files *repo.FileRepo, every time.Duration) {
	if every <= 0 {
		every = time.Hour
	}
	if files == nil {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := RunMissingPurge(ctx, files)
			if err != nil {
				slog.Warn("missing purge", "err", err)
				continue
			}
			if n > 0 {
				slog.Info("missing purge", "deleted", n)
			}
		}
	}
}
