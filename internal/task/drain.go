// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"context"
	"log/slog"
	"time"
)

// DrainConfig configures a Drain run.
type DrainConfig struct {
	Name      string        // log tag (e.g. "files", "covers")
	BatchSize int           // 0 → 100
	Sleep     time.Duration // pause between batches; 0 = none
	// Logger receives this drain's own output. Nil means the process
	// default, which is what every production caller wants.
	//
	// It exists so a test can read back what a drain logged without
	// installing a handler globally: slog.SetDefault is a process global,
	// and a parallel test writing it races every other test in the
	// package that logs — the one failure that stopped `go test -race
	// ./internal/task/` being a gate, and so hid any genuine race behind
	// known noise (#186).
	Logger *slog.Logger
}

// log is the drain's own logger, falling back to the process default so
// no caller has to supply one.
func (c DrainConfig) log() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

// Drain is the loop shape shared by boot-time backfills:
//
//  1. Call list to fetch the next batch of pending rows (predicate query).
//  2. Run process on each row not already in the in-run skip set.
//  3. If process returns an error, log it (with the cfg.Name tag and
//     the result of keyOf(item)) and add the key to the skip set.
//     The error is otherwise swallowed — Drain never aborts the run
//     on a per-item failure.
//  4. Stop when list returns an empty batch (predicate exhausted) OR
//     when no row in a batch made forward progress (every row was
//     either skipped or just failed). The second guard prevents
//     infinite loops when every row is a persistent failure.
//
// Returns the count of rows that were processed without error.
func Drain[T any](
	ctx context.Context,
	cfg DrainConfig,
	list func(ctx context.Context, batchSize int) ([]T, error),
	keyOf func(T) string,
	process func(ctx context.Context, item T) error,
) (int, error) {
	if list == nil || keyOf == nil || process == nil {
		return 0, nil
	}
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}
	skipped := make(map[string]bool)
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		batch, err := list(ctx, batchSize)
		if err != nil {
			return total, err
		}
		if len(batch) == 0 {
			if total > 0 {
				cfg.log().Info("drain complete", "drainer", cfg.Name, "processed", total)
			}
			return total, nil
		}

		progress := 0
		for _, item := range batch {
			if err := ctx.Err(); err != nil {
				return total, err
			}
			k := keyOf(item)
			if skipped[k] {
				continue
			}
			if err := process(ctx, item); err != nil {
				cfg.log().Warn("drain: process failed",
					"drainer", cfg.Name, "key", k, "err", err)
				skipped[k] = true
				continue
			}
			total++
			progress++
		}
		if progress == 0 {
			// Every row in this batch was either already skipped or
			// failed during this iteration. The predicate query will
			// keep returning the same set; another iteration would
			// loop forever. Stop and let the next boot retry.
			cfg.log().Info("drain stopped: no progress in batch",
				"drainer", cfg.Name, "processed", total, "batch_size", len(batch))
			return total, nil
		}
		if cfg.Sleep > 0 {
			select {
			case <-time.After(cfg.Sleep):
			case <-ctx.Done():
				return total, ctx.Err()
			}
		}
	}
}
