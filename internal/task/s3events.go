package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// SQSReceiver is the slice of the SQS API the loop needs.
// Defined as an interface so tests can stub it.
type SQSReceiver interface {
	ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessageBatch(ctx context.Context, in *sqs.DeleteMessageBatchInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageBatchOutput, error)
}

// S3EventLoopDeps wires the loop's collaborators.
type S3EventLoopDeps struct {
	SQS      SQSReceiver
	QueueURL string
	Files    *repo.FileRepo
	// BucketToLibrary maps bucket name → library id (resolved at boot).
	// The loop uses it to map an S3 event's bucket to a library for
	// FileRepo lookups. One library per bucket is the common case;
	// multi-library-per-bucket installs are not yet handled (TODO:
	// longest-prefix match on library root).
	BucketToLibrary map[string]string
	PollInterval    time.Duration
}

// RunS3EventLoop polls SQS in a loop until ctx is cancelled.
// Errors are logged; the loop never exits except via cancellation.
// When QueueURL is empty or SQS/Files is nil the function returns
// immediately, so callers can always call it unconditionally.
func RunS3EventLoop(ctx context.Context, deps S3EventLoopDeps) {
	if deps.SQS == nil || deps.QueueURL == "" || deps.Files == nil {
		return
	}
	iv := deps.PollInterval
	if iv <= 0 {
		iv = 30 * time.Second
	}

	backoff := time.Second
	counter := 0 // monotonic ID counter for DeleteMessageBatch entries

	for {
		if err := ctx.Err(); err != nil {
			return
		}

		out, err := deps.SQS.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            &deps.QueueURL,
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     20, // long-poll saves API calls
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			slog.Warn("s3 events: receive", "err", err)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			if backoff < time.Minute {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second

		if len(out.Messages) == 0 {
			// Quiet poll — wait the configured interval before next receive.
			select {
			case <-time.After(iv):
			case <-ctx.Done():
				return
			}
			continue
		}

		deletes := make([]sqstypes.DeleteMessageBatchRequestEntry, 0, len(out.Messages))
		for _, m := range out.Messages {
			if m.Body == nil {
				continue
			}
			if err := dispatchEvent(ctx, deps, []byte(*m.Body)); err != nil {
				slog.Warn("s3 events: dispatch", "err", err)
				// Do NOT delete — SQS will redeliver after visibility timeout.
				continue
			}
			id := fmt.Sprintf("%d", counter)
			counter++
			deletes = append(deletes, sqstypes.DeleteMessageBatchRequestEntry{
				Id:            &id,
				ReceiptHandle: m.ReceiptHandle,
			})
		}

		if len(deletes) > 0 {
			if _, err := deps.SQS.DeleteMessageBatch(ctx, &sqs.DeleteMessageBatchInput{
				QueueUrl: &deps.QueueURL,
				Entries:  deletes,
			}); err != nil {
				slog.Warn("s3 events: delete batch", "err", err)
			}
		}
		// Non-empty batch: go straight back to receive — no sleep.
	}
}

// s3Event is the trimmed payload we care about. Real S3 events include
// far more fields; we deserialize only what we need.
type s3Event struct {
	Records []struct {
		EventName string `json:"eventName"`
		S3        struct {
			Bucket struct {
				Name string `json:"name"`
			} `json:"bucket"`
			Object struct {
				Key  string `json:"key"`
				Size int64  `json:"size"`
				ETag string `json:"eTag"`
			} `json:"object"`
		} `json:"s3"`
	} `json:"Records"`
}

func dispatchEvent(ctx context.Context, deps S3EventLoopDeps, body []byte) error {
	var ev s3Event
	if err := json.Unmarshal(body, &ev); err != nil {
		return fmt.Errorf("unmarshal s3 event: %w", err)
	}

	for _, r := range ev.Records {
		libID, ok := deps.BucketToLibrary[r.S3.Bucket.Name]
		if !ok {
			continue
		}

		// S3 event keys are bucket-relative; FileRepo wants library-relative.
		// For the one-library-per-bucket case the key is already correct.
		// Multi-library-per-bucket would need longest-prefix logic (TODO).
		loc := r.S3.Object.Key

		switch {
		case strings.HasPrefix(r.EventName, "ObjectCreated"):
			existing, err := deps.Files.GetByLocation(ctx, libID, loc)
			if errors.Is(err, repo.ErrNotFound) {
				// New file — insert a stub row; content_hash computed by
				// the next scan / boot backfill worker.
				if _, err = deps.Files.Insert(ctx, model.File{
					LibraryID: libID,
					Location:  loc,
					Size:      r.S3.Object.Size,
					ETag:      r.S3.Object.ETag,
					Format:    formatFromExt(loc),
					Mtime:     time.Now(),
				}); err != nil {
					return fmt.Errorf("insert file %q: %w", loc, err)
				}
			} else if err != nil {
				return fmt.Errorf("get by location %q: %w", loc, err)
			} else if existing.MissingSince != nil {
				// File reappeared — clear the missing flag.
				if err := deps.Files.ClearMissing(ctx, existing.ID); err != nil {
					return fmt.Errorf("clear missing %q: %w", loc, err)
				}
			}
			// else: row already present and not missing — nothing to do.

		case strings.HasPrefix(r.EventName, "ObjectRemoved"):
			f, err := deps.Files.GetByLocation(ctx, libID, loc)
			if errors.Is(err, repo.ErrNotFound) {
				continue // unknown file — no-op
			}
			if err != nil {
				return fmt.Errorf("get by location %q: %w", loc, err)
			}
			if err := deps.Files.MarkMissing(ctx, f.ID, time.Now()); err != nil {
				return fmt.Errorf("mark missing %q: %w", loc, err)
			}
		}
	}
	return nil
}

// formatFromExt guesses the book format from the file extension.
func formatFromExt(loc string) string {
	switch strings.ToLower(filepath.Ext(loc)) {
	case ".epub":
		return "EPUB"
	case ".pdf":
		return "PDF"
	case ".cbz":
		return "CBZ"
	case ".mp3":
		return "MP3"
	case ".m4a", ".m4b":
		return "M4B"
	}
	return ""
}
