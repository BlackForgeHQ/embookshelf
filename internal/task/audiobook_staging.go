// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/blackforge/embookshelf/internal/config"
)

// Staging is the audiobook staging area: the local disk where one run's
// per-segment MP3s live between the engine call that bought them and the
// finalize step that joins them into a library artifact. Outside
// storage.Storage, following the coverstore precedent for derived bytes
// (ADR-0028).
//
// It exists because the area was previously a concept and not a thing.
// The directory had a helper; the segment filename, the write, the
// per-run clean, the hourly sweep and an inline delete at the
// composition root did not, so five files each knew a piece of the
// convention. Two consequences are worth remembering. The composition
// root's delete bypassed the helper carrying the unset-root guard and
// survived only because os.RemoveAll("") happens to return nil — an
// unconfigured deployment and an empty staging area were indistinguishable
// (#251). And before the guard existed at all, joining an empty root gave
// the *relative* path audiobooks/{book_id}, which the segment worker
// created under the process working directory and wrote hundreds of
// megabytes into, while the sweeper meant to reclaim it had already
// concluded there was nothing to reclaim (#207).
//
// Whether staging is configured at all is settled here, once, by the root
// the value was built from — not re-derived by each caller from a string
// it tests for emptiness.
type Staging struct {
	root config.DataRoot
}

// NewStaging builds the staging area over a data root. A zero root is
// accepted and yields a staging area that refuses every operation with
// config.ErrDataRootUnset: "no staging configured" is a state the value
// carries, not a check each caller remembers to make.
func NewStaging(root config.DataRoot) Staging { return Staging{root: root} }

// Configured reports whether this deployment has a staging area at all.
// For callers deciding whether to run — a sweep with nothing to sweep —
// rather than for callers about to touch it, which should ask for the
// path and handle the error.
func (s Staging) Configured() bool { return s.root.IsSet() }

// Dir is where one book's staged segments live.
func (s Staging) Dir(bookID string) (string, error) {
	return s.root.AudiobookStaging(bookID)
}

// SegmentPath is where one segment of one book lands. The filename
// convention lives here and nowhere else: finalize reads segments back by
// the path recorded on the row, but a test or a future reader asking
// "what is a staged segment called" has one answer to find.
func (s Staging) SegmentPath(bookID string, seq int) (string, error) {
	dir, err := s.Dir(bookID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "seg-"+strconv.Itoa(seq)+".mp3"), nil
}

// WriteSegment stages one segment's frames and returns where they landed.
// Creates the run's directory on the way, so a caller never has to know
// whether this is the first segment of the run.
func (s Staging) WriteSegment(bookID string, seq int, frames []byte) (string, error) {
	path, err := s.SegmentPath(bookID, seq)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create staging dir: %w", err)
	}
	if err := os.WriteFile(path, frames, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// Clean removes a book's staged segments. The one deleter: cancel,
// finalize and the sweep all arrive here, and an unconfigured staging
// area removes nothing rather than removing an empty path and calling it
// success.
//
// Silent by design apart from the log. Every caller reaches it on a path
// where the run is already concluded and there is nothing left to fail.
func (s Staging) Clean(bookID string) {
	dir, err := s.Dir(bookID)
	if err != nil {
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		slog.Warn("audiobook: clean staging", "book", bookID, "err", err)
	}
}

// StaleStagingTTL is how long an abandoned failed or cancelled run keeps
// its staging before the sweeper reclaims it. Long enough that a Retry
// the next morning is still free; short enough that a run nobody comes
// back to does not park gigabytes forever.
const StaleStagingTTL = 7 * 24 * time.Hour

// stagingLister is the one thing a sweep asks of a run store: which runs
// have been dead weight long enough to reclaim.
type stagingLister interface {
	ListStaleStaging(ctx context.Context, olderThanDays int) ([]string, error)
}

// Sweep removes staging for runs whose staged segments have been dead
// weight for longer than StaleStagingTTL, and reports how many it
// reclaimed. Which runs those are is ListStaleStaging's judgement, not
// this loop's — in particular a run that failed recently keeps every
// paid-for segment, because Retry re-enqueues only what never finished
// (ADR-0028 §6).
func (s Staging) Sweep(ctx context.Context, runs stagingLister) (int, error) {
	if !s.Configured() || runs == nil {
		return 0, nil
	}
	ids, err := runs.ListStaleStaging(ctx, int(StaleStagingTTL/(24*time.Hour)))
	if err != nil {
		return 0, err
	}
	swept := 0
	for _, id := range ids {
		dir, err := s.Dir(id)
		if err != nil {
			return swept, err
		}
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		s.Clean(id)
		swept++
	}
	return swept, nil
}

// LoopSweep runs the sweep hourly, matching the shape of the
// missing-file and orphaned-key sweepers.
func (s Staging) LoopSweep(ctx context.Context, runs stagingLister) {
	if !s.Configured() || runs == nil {
		return
	}
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := s.Sweep(ctx, runs)
			if err != nil {
				slog.Warn("audiobook staging sweep", "err", err)
				continue
			}
			if n > 0 {
				slog.Info("audiobook staging sweep", "swept", n)
			}
		}
	}
}
