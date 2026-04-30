# S3 Events + Lifecycle — Implementation Plan (Plan H of 8)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the ops-layer pieces of the spec: SQS-poll worker that consumes S3 event notifications and reconciles them against the `files` table, and an admin tool to apply storage-class tags so a bucket lifecycle rule can transition cold books to cheaper tiers. Both features are opt-in — running embookshelf without them changes nothing.

**Architecture:** A new `internal/task/s3events.go` runs a polling loop that pulls messages from a configured SQS queue, decodes the `s3:ObjectCreated:*` / `s3:ObjectRemoved:*` payloads, and dispatches into the existing scan reconciler primitives (insert / mark missing / clear missing) — no new code paths in the DB layer, just a faster reconciliation than the periodic full walk. A separate `cmd/embookshelf-tag` admin CLI (or a periodic job) reads `books.last_read_at`, classifies each book into hot/warm/cold, and writes `x-amz-tagging: tier=...` on the corresponding S3 object. The bucket's lifecycle rule (configured outside the app via Terraform / `aws s3api`) keys transitions off that tag.

**Tech Stack:** Reuses Plan F's `aws-sdk-go-v2/service/s3`. Adds `aws-sdk-go-v2/service/sqs`. Optionally `aws-sdk-go-v2/service/s3control` if we want to manage lifecycle rules from inside the app — **not in scope for Plan H**, lifecycle stays admin-managed.

**Companion spec:** [`docs/spec/storage.spec.md`](../../spec/storage.spec.md) §5.4 (change notification), §8.3 (storage class and lifecycle).

**Locked decisions:**
- SQS poll loop. Native S3-events → SNS → Lambda → app HTTP webhook is over-engineered for self-hosted. Polling is what self-hosters can actually configure.
- Tier tag values: `hot` (read in last 90 days), `warm` (90–365), `cold` (>365 or never). Lifecycle rule keys off `tag:tier=cold`.
- The `last_read_at` per book already exists in `user_book_progress` (max across users) — read-only data, no migration.
- Admin CLI: `cmd/embookshelf-tag` runs as a one-shot or in cron. Simpler than a long-lived worker.
- Without `EMBOOKSHELF_S3_EVENT_QUEUE` set, the SQS worker is disabled and the existing periodic full-walk handles reconciliation as today.
- Without explicit invocation, the tagger is a no-op.

**Depends on:** Plan F (S3 backend + Resolver). Plan C (scan reconciliation primitives).

**Out of scope:**
- Lifecycle rule creation from inside the app — admin-managed via Terraform / `aws cli`. We document the recommended config in this plan.
- SNS / EventBridge fan-out — pure SQS poll.
- Multi-region or cross-account event flows.
- A web UI for viewing tagged books or manually re-tiering — admin CLI only.
- Hot-tier tagging on read — too chatty. Tagger is run periodically, not on every read.

---

## File Structure

### Files created

| Path | Responsibility |
|---|---|
| `internal/task/s3events.go` | `RunS3EventLoop(ctx, deps)` — polls SQS, dispatches events to FileRepo. |
| `internal/task/s3events_test.go` | Unit tests with a fake SQS client. |
| `cmd/embookshelf-tag/main.go` | Admin CLI to walk books and apply tier tags. |
| `internal/tagging/tagger.go` | Pure tier classifier + S3 PutObjectTagging call. |
| `internal/tagging/tagger_test.go` | Tests for the classifier. |
| `docs/ops/s3-lifecycle.md` | Recommended bucket lifecycle JSON + IAM doc. |

### Files modified

| Path | Change |
|---|---|
| `cmd/embookshelf/main.go` | Launch `task.RunS3EventLoop` in a goroutine when `EMBOOKSHELF_S3_EVENT_QUEUE` is set. |
| `internal/config/config.go` | Add `S3EventQueueURL string`, `S3EventQueueRegion string`, `S3EventPollInterval time.Duration` (default 30s). |
| `internal/storageloader/loader.go` | Expose the SQS client / region per backend so the event loop can pick the right one when multiple S3 backends exist (most installs have one). |
| `Makefile` | Add `make tag` to build and run the embookshelf-tag binary. |
| `go.mod` / `go.sum` | Add `aws-sdk-go-v2/service/sqs`. |

---

## Phase 1 — SQS Event Loop

### Task 1: SQS poll skeleton + event decoder

**Files:**
- Create: `internal/task/s3events.go`
- Create: `internal/task/s3events_test.go`

```go
package task

import (
    "context"
    "encoding/json"
    "errors"
    "log/slog"
    "time"

    "github.com/aws/aws-sdk-go-v2/service/sqs"
    sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

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
    SQS        SQSReceiver
    QueueURL   string
    Files      *repo.FileRepo
    Libraries  *repo.LibraryRepo
    // Backends maps storage_backend.id → bucket name; the loop uses
    // it to map an S3 event's bucket to a library_id for FileRepo
    // lookups.
    BucketToLibrary map[string]string // bucket name → library id (resolved at boot)
    PollInterval    time.Duration
}

// RunS3EventLoop polls SQS in a loop until ctx is cancelled.
// Errors are logged; the loop never exits except via cancellation.
func RunS3EventLoop(ctx context.Context, deps S3EventLoopDeps) {
    if deps.SQS == nil || deps.QueueURL == "" || deps.Files == nil {
        return
    }
    iv := deps.PollInterval
    if iv <= 0 { iv = 30 * time.Second }
    backoff := time.Second
    for {
        if err := ctx.Err(); err != nil { return }
        out, err := deps.SQS.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
            QueueUrl:            &deps.QueueURL,
            MaxNumberOfMessages: 10,
            WaitTimeSeconds:     20, // long-poll
        })
        if err != nil {
            slog.Warn("s3 events: receive", "err", err)
            select {
            case <-time.After(backoff):
            case <-ctx.Done(): return
            }
            if backoff < time.Minute { backoff *= 2 }
            continue
        }
        backoff = time.Second
        if len(out.Messages) == 0 {
            select {
            case <-time.After(iv):
            case <-ctx.Done(): return
            }
            continue
        }
        deletes := make([]sqstypes.DeleteMessageBatchRequestEntry, 0, len(out.Messages))
        for i, m := range out.Messages {
            if m.Body == nil { continue }
            if err := dispatchEvent(ctx, deps, []byte(*m.Body)); err != nil {
                slog.Warn("s3 events: dispatch", "err", err)
                continue
            }
            id := fmt.Sprintf("%d", i)
            deletes = append(deletes, sqstypes.DeleteMessageBatchRequestEntry{
                Id:            &id,
                ReceiptHandle: m.ReceiptHandle,
            })
        }
        if len(deletes) > 0 {
            _, _ = deps.SQS.DeleteMessageBatch(ctx, &sqs.DeleteMessageBatchInput{
                QueueUrl: &deps.QueueURL,
                Entries:  deletes,
            })
        }
    }
}

// s3Event is the trimmed payload we care about. Real S3 events include
// far more fields; we deserialize only what we need.
type s3Event struct {
    Records []struct {
        EventName string `json:"eventName"`
        S3 struct {
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
        return err
    }
    for _, r := range ev.Records {
        libID, ok := deps.BucketToLibrary[r.S3.Bucket.Name]
        if !ok { continue }
        // Strip the library prefix from the key — S3 event keys are
        // bucket-relative; FileRepo wants library-relative.
        loc := r.S3.Object.Key
        // For simplicity: assume one library per bucket. Multi-library-
        // per-bucket installs would need to find the right library by
        // longest-prefix match on l.root.
        switch {
        case strings.HasPrefix(r.EventName, "ObjectCreated"):
            // Insert or update files row. content_hash stays NULL —
            // the boot worker (or next scan) computes it.
            existing, err := deps.Files.GetByLocation(ctx, libID, loc)
            if errors.Is(err, repo.ErrNotFound) {
                _, err = deps.Files.Insert(ctx, model.File{
                    LibraryID: libID, Location: loc,
                    Size: r.S3.Object.Size, ETag: r.S3.Object.ETag,
                    Format: formatFromExt(loc),
                    Mtime:  time.Now(),
                })
                if err != nil { return err }
            } else if err != nil {
                return err
            } else if existing.MissingSince != nil {
                // File reappeared — clear missing flag.
                _ = deps.Files.ClearMissing(ctx, existing.ID)
            }
        case strings.HasPrefix(r.EventName, "ObjectRemoved"):
            f, err := deps.Files.GetByLocation(ctx, libID, loc)
            if errors.Is(err, repo.ErrNotFound) { continue }
            if err != nil { return err }
            _ = deps.Files.MarkMissing(ctx, f.ID, time.Now())
        }
    }
    return nil
}

func formatFromExt(loc string) string {
    ext := strings.ToLower(filepath.Ext(loc))
    switch ext {
    case ".epub": return "EPUB"
    case ".pdf":  return "PDF"
    case ".cbz":  return "CBZ"
    case ".mp3":  return "MP3"
    case ".m4a", ".m4b": return "M4B"
    }
    return ""
}
```

Tests:
- Stub `SQSReceiver` returning a single ObjectCreated message → `Files.Insert` called.
- Stub returning ObjectRemoved for an existing row → `MarkMissing` called.
- Stub returning ObjectRemoved for an unknown row → no-op.
- Stub returning ObjectCreated for an existing row that is `MissingSince != nil` → `ClearMissing` called.
- Empty Records → nothing happens.
- ctx cancellation exits the loop cleanly.

Commit:
```bash
git commit -m "feat(task): SQS-poll loop reconciles S3 events into files table"
```

---

## Phase 2 — Tagger

### Task 2: Tier classifier

**Files:**
- Create: `internal/tagging/tagger.go`
- Create: `internal/tagging/tagger_test.go`

```go
// Package tagging classifies books into hot/warm/cold tiers based on
// recency of last-read events and writes the result back to S3 via
// PutObjectTagging.
package tagging

import (
    "context"
    "fmt"
    "time"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Tier is the classification a lifecycle rule keys off.
type Tier string

const (
    TierHot  Tier = "hot"
    TierWarm Tier = "warm"
    TierCold Tier = "cold"
)

// Classify returns the tier for a book given its last-read time.
func Classify(now, lastRead time.Time) Tier {
    if lastRead.IsZero() {
        return TierCold
    }
    age := now.Sub(lastRead)
    switch {
    case age <= 90*24*time.Hour:
        return TierHot
    case age <= 365*24*time.Hour:
        return TierWarm
    default:
        return TierCold
    }
}

// TagWriter is the S3 surface the Apply path needs.
type TagWriter interface {
    PutObjectTagging(ctx context.Context, in *s3.PutObjectTaggingInput, optFns ...func(*s3.Options)) (*s3.PutObjectTaggingOutput, error)
}

// Apply writes tier=<value> on the object at key.
func Apply(ctx context.Context, tw TagWriter, bucket, key string, tier Tier) error {
    _, err := tw.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{
        Bucket: &bucket,
        Key:    &key,
        Tagging: &types.Tagging{
            TagSet: []types.Tag{{
                Key:   aws.String("tier"),
                Value: aws.String(string(tier)),
            }},
        },
    })
    if err != nil {
        return fmt.Errorf("tagging: put %q: %w", key, err)
    }
    return nil
}
```

Tests cover the classifier corner cases (zero time, exactly-90d boundary, exactly-365d boundary).

Commit:
```bash
git commit -m "feat(tagging): tier classifier + S3 PutObjectTagging helper"
```

---

### Task 3: `cmd/embookshelf-tag` admin CLI

**Files:**
- Create: `cmd/embookshelf-tag/main.go`

```go
// Command embookshelf-tag walks every book in the configured library,
// classifies it into hot/warm/cold tiers based on the latest
// user_book_progress.updated_at, and writes the tier tag onto the
// corresponding S3 object via PutObjectTagging.
//
// Usage: embookshelf-tag [-dry-run]
package main

import (
    "context"
    "flag"
    "fmt"
    "log/slog"
    "os"
    "time"

    "github.com/blackforge/embookshelf/internal/config"
    "github.com/blackforge/embookshelf/internal/db"
    "github.com/blackforge/embookshelf/internal/repo"
    "github.com/blackforge/embookshelf/internal/storage"
    "github.com/blackforge/embookshelf/internal/storage/s3"
    "github.com/blackforge/embookshelf/internal/storageloader"
    "github.com/blackforge/embookshelf/internal/tagging"
)

func main() {
    var dryRun bool
    flag.BoolVar(&dryRun, "dry-run", false, "log decisions but don't call PutObjectTagging")
    flag.Parse()

    ctx := context.Background()
    cfg, err := config.Load()
    if err != nil { fail("config", err) }
    dbh, err := db.Open(ctx, cfg)
    if err != nil { fail("db", err) }

    libRepo := repo.NewLibraryRepo(dbh)
    backendRepo := repo.NewStorageBackendRepo(dbh)
    fileRepo := repo.NewFileRepo(dbh)
    statsRepo := repo.NewReadingSessionRepo(dbh)

    resolver, err := storageloader.LoadStorageBackends(ctx, backendRepo, dbh.Dialect)
    if err != nil { fail("storage", err) }

    libs, err := libRepo.List(ctx)
    if err != nil { fail("list libraries", err) }

    now := time.Now()
    total := 0
    for _, lib := range libs {
        if lib.BackendID == nil { continue }
        backend, err := resolver.Resolve(*lib.BackendID)
        if err != nil { continue }
        s3b, ok := backend.(*s3.Backend)
        if !ok { continue }
        bucket := s3b.Bucket() // expose via accessor; add to Backend if needed

        files, err := fileRepo.ListByLibrary(ctx, lib.ID)
        if err != nil { fail("list files", err) }

        for _, f := range files {
            lastRead, _ := statsRepo.LatestForBook(ctx, f.BookID) // returns time.Time, zero on no rows
            tier := tagging.Classify(now, lastRead)
            if dryRun {
                slog.Info("dry-run", "book", f.BookID, "tier", tier, "key", f.Location)
                continue
            }
            if err := tagging.Apply(ctx, s3b.Client(), bucket, s3b.Prefix()+f.Location, tier); err != nil {
                slog.Warn("tag failed", "book", f.BookID, "err", err)
                continue
            }
            total++
        }
    }
    slog.Info("tagging done", "tagged", total, "libraries", len(libs))
}

func fail(stage string, err error) {
    fmt.Fprintf(os.Stderr, "embookshelf-tag: %s: %v\n", stage, err)
    os.Exit(1)
}
```

Implementer notes:
- `s3.Backend` may need to expose `Bucket()`, `Prefix()`, `Client()` accessors. Add them as small methods if absent.
- `repo.ReadingSessionRepo.LatestForBook` may not exist — add it (`SELECT MAX(updated_at) FROM user_book_progress WHERE book_id = $1`). If `user_book_progress` doesn't have `updated_at`, use `MAX(progress_updated_at)` or whichever timestamp the schema actually has.
- The `Makefile` adds:

```makefile
.PHONY: tag
tag:
	go build -o ./tmp/embookshelf-tag ./cmd/embookshelf-tag
	./tmp/embookshelf-tag $(ARGS)
```

Commit:
```bash
git commit -m "feat: cmd/embookshelf-tag admin CLI"
```

---

## Phase 3 — Boot Wiring + Docs

### Task 4: Wire SQS loop in main.go + config

`internal/config/config.go`:

```go
S3EventQueueURL    string
S3EventQueueRegion string
S3EventPollInterval time.Duration
```

Defaults: queue URL empty (worker disabled), region "us-east-1", interval 30s.

`cmd/embookshelf/main.go`: when `cfg.S3EventQueueURL != ""`, build an SQS client and launch `RunS3EventLoop` in a goroutine. Source the bucket-to-library map from `libRepo.List(ctx)` + `backendRepo.Get(libBackendID)` — for each S3 backend, map `bucket → library_id`.

`docs/ops/s3-lifecycle.md`:

```markdown
# S3 Lifecycle Setup

Apply this lifecycle JSON to your library bucket:

\`\`\`json
{
  "Rules": [
    {
      "Id": "embookshelf-tier-warm",
      "Status": "Enabled",
      "Filter": { "Tag": { "Key": "tier", "Value": "warm" } },
      "Transitions": [{"Days": 1, "StorageClass": "STANDARD_IA"}]
    },
    {
      "Id": "embookshelf-tier-cold",
      "Status": "Enabled",
      "Filter": { "Tag": { "Key": "tier", "Value": "cold" } },
      "Transitions": [{"Days": 1, "StorageClass": "GLACIER_IR"}]
    }
  ]
}
\`\`\`

Apply with: `aws s3api put-bucket-lifecycle-configuration --bucket <name> --lifecycle-configuration file://lifecycle.json`.

Run `make tag` (or set up cron) to refresh the tier tags daily.
```

Commit:
```bash
git commit -m "feat: SQS loop boot-wiring + lifecycle ops docs"
```

---

## Phase 4 — Verification

### Task 5: Verify and PR

- [ ] `make ci-local` green.
- [ ] `make build` succeeds (the new `cmd/embookshelf-tag` binary compiles).
- [ ] Push, open PR.

---

## Self-Review

**Spec coverage:**
- §5.4 change notification (S3 events → SQS poll) → covered.
- §5.4 periodic full walk safety net → already exists; the SQS loop is additive.
- §8.3 storage class + lifecycle (tier tags) → covered for the app side; lifecycle rules themselves are admin-managed and documented.

**Risks:**
- The SQS loop uses long-polling (20s). It blocks one goroutine per app instance. Acceptable.
- The bucket-to-library map is built once at startup. Adding a new library while running won't be picked up until restart. Acceptable for the self-hosted use case.
- The tagger CLI re-tags every book on every run. For a library of 100k books this is 100k PutObjectTagging calls (~5min at API rate limits). Acceptable for a daily cron; not for inline use.
- A rapidly-renamed file would emit ObjectRemoved + ObjectCreated. The dispatch handles each independently → MarkMissing then Insert. Hash-based reattach (Plan C) reconciles when the next scan runs.
- If `EMBOOKSHELF_S3_EVENT_QUEUE` points at the wrong queue, dispatchEvent silently no-ops on every message because BucketToLibrary won't match. Add a startup log line confirming the configured queue + bucket mapping for ops sanity.

**Type consistency:** `S3EventLoopDeps`, `RunS3EventLoop`, `dispatchEvent`, `Tier`, `Classify`, `Apply`, `TagWriter`, `SQSReceiver` consistent across files.
