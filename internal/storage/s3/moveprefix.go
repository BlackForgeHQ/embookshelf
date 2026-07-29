// SPDX-License-Identifier: AGPL-3.0-or-later

package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/storage"
)

// CopyRetryAttempts is the bounded retry budget for a single CopyObject
// inside MovePrefix. Three attempts with exponential backoff rides out
// transient 5xx / throttle responses without spinning on a genuinely
// broken backend.
//
// This lives with the adapter rather than with the rename caller because
// it is resilience against one specific failure mode of one specific
// backend. LocalFS has no equivalent and needs none.
const CopyRetryAttempts = 3

// CopyRetryBaseDelay is the first backoff delay between copy retries.
// Doubles each attempt: 200ms, 400ms, 800ms.
const CopyRetryBaseDelay = 200 * time.Millisecond

// MovePrefix relocates every object under oldPrefix to newPrefix.
//
// S3 has no rename, so this is a list plus a server-side copy per key,
// and the sources are deliberately left alone: deleting them here would
// break every presigned URL a client is already holding for a key under
// the old prefix. The source keys come back in MoveResult.Reclaim so the
// caller can defer the delete behind its own grace window — pending
// orphans, per ADR-0005.
//
// On a copy failure the error is returned together with the destinations
// written so far, so the caller can reclaim the partial write rather
// than leak it.
func (b *Backend) MovePrefix(ctx context.Context, oldPrefix, newPrefix string) (storage.MoveResult, error) {
	return movePrefix(ctx, b, oldPrefix, newPrefix, b.copyWithRetry)
}

// prefixLister is the slice of storage.Storage movePrefix needs. Named
// so the copy loop can be exercised without an S3 service behind it.
type prefixLister interface {
	List(ctx context.Context, prefix string) (storage.Iterator, error)
}

// movePrefix is the mechanics of MovePrefix with the copy step injected.
func movePrefix(
	ctx context.Context,
	lister prefixLister,
	oldPrefix, newPrefix string,
	copyOne func(ctx context.Context, src, dst string) error,
) (storage.MoveResult, error) {
	src := folderPrefix(oldPrefix)
	dst := folderPrefix(newPrefix)
	if src == "" || dst == "" {
		// An empty prefix is the whole backend. Moving it is never what
		// a caller meant and would rewrite every library in the bucket.
		return storage.MoveResult{}, errors.Join(storage.ErrInvalidKey,
			fmt.Errorf("s3: MovePrefix needs a non-empty prefix on both sides"))
	}
	srcKeys, err := listPrefix(ctx, lister, src)
	if err != nil {
		return storage.MoveResult{}, err
	}
	if len(srcKeys) == 0 {
		return storage.MoveResult{}, fmt.Errorf("%w: no object under %q",
			storage.ErrNotFound, oldPrefix)
	}

	res := storage.MoveResult{Written: make([]string, 0, len(srcKeys))}
	for _, key := range srcKeys {
		dstKey := dst + strings.TrimPrefix(key, src)
		if err := copyOne(ctx, key, dstKey); err != nil {
			// Written travels with the error: the caller is the only one
			// who can decide these are garbage.
			return res, fmt.Errorf("s3: copy %q to %q: %w", key, dstKey, err)
		}
		res.Written = append(res.Written, dstKey)
	}
	res.Reclaim = srcKeys
	return res, nil
}

// folderPrefix normalizes a prefix to exactly one trailing slash so a
// caller may pass "Author/Title" or "Author/Title/" and get the same
// match set — without the slash, "Author/Title" would also sweep up
// "Author/Title Revisited/…". Returns "" for a prefix that is empty
// once the slashes are trimmed.
func folderPrefix(p string) string {
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	return p + "/"
}

// listPrefix walks lister under prefix and collects every key. Keys are
// backend-relative and keep the prefix, so callers rewrite them with a
// plain strings.TrimPrefix.
func listPrefix(ctx context.Context, lister prefixLister, prefix string) ([]string, error) {
	it, err := lister.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	defer func() { _ = it.Close() }()
	var keys []string
	for {
		obj, err := it.Next(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		keys = append(keys, obj.Key)
	}
	return keys, nil
}

// copyWithRetry runs Copy with bounded exponential backoff. Context
// cancellation aborts immediately; a missing source is terminal, since
// there is nothing to retry into existence.
func (b *Backend) copyWithRetry(ctx context.Context, src, dst string) error {
	delay := CopyRetryBaseDelay
	var lastErr error
	for attempt := 0; attempt < CopyRetryAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, err := b.Copy(ctx, src, dst)
		if err == nil {
			return nil
		}
		if errors.Is(err, storage.ErrNotFound) {
			return err
		}
		lastErr = err
		if attempt+1 < CopyRetryAttempts {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
			delay *= 2
		}
	}
	return lastErr
}
