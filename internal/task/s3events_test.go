package task_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/task"
)

// fakeSQS implements task.SQSReceiver for unit tests. It returns
// responses from a preset queue and records which receipt handles were
// deleted.
type fakeSQS struct {
	responses  []*awssqs.ReceiveMessageOutput
	respondIdx int
	deleted    []string
}

func (f *fakeSQS) ReceiveMessage(_ context.Context, _ *awssqs.ReceiveMessageInput, _ ...func(*awssqs.Options)) (*awssqs.ReceiveMessageOutput, error) {
	if f.respondIdx >= len(f.responses) {
		return &awssqs.ReceiveMessageOutput{}, nil
	}
	out := f.responses[f.respondIdx]
	f.respondIdx++
	return out, nil
}

func (f *fakeSQS) DeleteMessageBatch(_ context.Context, in *awssqs.DeleteMessageBatchInput, _ ...func(*awssqs.Options)) (*awssqs.DeleteMessageBatchOutput, error) {
	for _, e := range in.Entries {
		if e.ReceiptHandle != nil {
			f.deleted = append(f.deleted, *e.ReceiptHandle)
		}
	}
	return &awssqs.DeleteMessageBatchOutput{}, nil
}

// sqsMsg builds a single SQS message with the given body and receipt handle.
func sqsMsg(body, receiptHandle string) sqstypes.Message {
	return sqstypes.Message{
		Body:          strPtr(body),
		ReceiptHandle: strPtr(receiptHandle),
	}
}

func strPtr(s string) *string { return &s }

// makeBody builds a fake S3 event body JSON for a single record.
func makeBody(bucket, key, eventName string, size int64) string {
	type s3record struct {
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
	}
	var r s3record
	r.EventName = eventName
	r.S3.Bucket.Name = bucket
	r.S3.Object.Key = key
	r.S3.Object.Size = size
	r.S3.Object.ETag = "etag123"

	type envelope struct {
		Records []s3record `json:"Records"`
	}
	b, _ := json.Marshal(envelope{Records: []s3record{r}})
	return string(b)
}

// newEventTestRepo creates a FileRepo + LibraryRepo wired to a fresh SQLite DB.
func newEventTestRepo(t *testing.T) (*repo.FileRepo, *repo.LibraryRepo) {
	t.Helper()
	d := repotest.NewWithDialect(t, "sqlite")
	return repo.NewFileRepo(d), repo.NewLibraryRepo(d)
}

// baseDeps returns a S3EventLoopDeps suitable for single-batch tests. The
// loop runs with a very short PollInterval so the ctx.Done path is reached
// quickly after the preset responses are exhausted.
func baseDeps(fq *fakeSQS, fr *repo.FileRepo, bucketToLib map[string]string) task.S3EventLoopDeps {
	return task.S3EventLoopDeps{
		SQS:             fq,
		QueueURL:        "https://sqs.us-east-1.amazonaws.com/123/q",
		Files:           fr,
		BucketToLibrary: bucketToLib,
		PollInterval:    5 * time.Millisecond,
	}
}

// TestS3EventLoop_ObjectCreated_Insert verifies that an ObjectCreated event
// for an unknown file results in a FileRepo.Insert call.
func TestS3EventLoop_ObjectCreated_Insert(t *testing.T) {
	fr, lr := newEventTestRepo(t)
	ctx := context.Background()

	lib, err := lr.CreateLibrary(ctx, "Lib", "lib", "/tmp/lib", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	const bucket = "my-bucket"
	const key = "books/novel.epub"

	fq := &fakeSQS{
		responses: []*awssqs.ReceiveMessageOutput{
			{Messages: []sqstypes.Message{sqsMsg(makeBody(bucket, key, "ObjectCreated:Put", 1024), "receipt-1")}},
		},
	}

	loopCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()

	task.RunS3EventLoop(loopCtx, baseDeps(fq, fr, map[string]string{bucket: lib.ID}))

	files, err := fr.ListByLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ListByLibrary: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	if files[0].Location != key {
		t.Errorf("location: got %q, want %q", files[0].Location, key)
	}
	if len(fq.deleted) != 1 || fq.deleted[0] != "receipt-1" {
		t.Errorf("deleted receipts: %v, want [receipt-1]", fq.deleted)
	}
}

// TestS3EventLoop_ObjectRemoved_MarkMissing verifies that an ObjectRemoved
// event for an existing file sets missing_since.
func TestS3EventLoop_ObjectRemoved_MarkMissing(t *testing.T) {
	fr, lr := newEventTestRepo(t)
	ctx := context.Background()

	lib, err := lr.CreateLibrary(ctx, "Lib2", "lib2", "/tmp/lib2", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	f, err := fr.Insert(ctx, model.File{
		LibraryID:   lib.ID,
		Location:    "gone.epub",
		Size:        512,
		Mtime:       now,
		Format:      "EPUB",
		LastScanned: now,
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	const bucket2 = "bucket2"
	fq := &fakeSQS{
		responses: []*awssqs.ReceiveMessageOutput{
			{Messages: []sqstypes.Message{sqsMsg(makeBody(bucket2, f.Location, "ObjectRemoved:Delete", 0), "receipt-2")}},
		},
	}

	loopCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()

	task.RunS3EventLoop(loopCtx, baseDeps(fq, fr, map[string]string{bucket2: lib.ID}))

	files, err := fr.ListByLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ListByLibrary: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	if files[0].MissingSince == nil {
		t.Error("want MissingSince non-nil after ObjectRemoved")
	}
}

// TestS3EventLoop_ObjectRemoved_UnknownFile verifies that an ObjectRemoved
// event for a file not in the DB is a no-op (no error, message deleted).
func TestS3EventLoop_ObjectRemoved_UnknownFile(t *testing.T) {
	fr, lr := newEventTestRepo(t)
	ctx := context.Background()

	lib, err := lr.CreateLibrary(ctx, "Lib3", "lib3", "/tmp/lib3", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	const bucket3 = "bucket3"
	fq := &fakeSQS{
		responses: []*awssqs.ReceiveMessageOutput{
			{Messages: []sqstypes.Message{sqsMsg(makeBody(bucket3, "does-not-exist.epub", "ObjectRemoved:Delete", 0), "receipt-3")}},
		},
	}

	loopCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()

	task.RunS3EventLoop(loopCtx, baseDeps(fq, fr, map[string]string{bucket3: lib.ID}))

	files, err := fr.ListByLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ListByLibrary: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("want 0 files, got %d", len(files))
	}
	// ObjectRemoved for unknown file is a no-op → dispatchEvent returns nil → message deleted.
	if len(fq.deleted) != 1 {
		t.Errorf("want 1 deleted receipt, got %d", len(fq.deleted))
	}
}

// TestS3EventLoop_ObjectCreated_ClearMissing verifies that an ObjectCreated
// event for a previously-missing file clears missing_since.
func TestS3EventLoop_ObjectCreated_ClearMissing(t *testing.T) {
	fr, lr := newEventTestRepo(t)
	ctx := context.Background()

	lib, err := lr.CreateLibrary(ctx, "Lib4", "lib4", "/tmp/lib4", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	f, err := fr.Insert(ctx, model.File{
		LibraryID:   lib.ID,
		Location:    "reappeared.epub",
		Size:        256,
		Mtime:       now,
		Format:      "EPUB",
		LastScanned: now,
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := fr.MarkMissing(ctx, f.ID, now.Add(-time.Hour)); err != nil {
		t.Fatalf("MarkMissing: %v", err)
	}

	const bucket4 = "bucket4"
	fq := &fakeSQS{
		responses: []*awssqs.ReceiveMessageOutput{
			{Messages: []sqstypes.Message{sqsMsg(makeBody(bucket4, f.Location, "ObjectCreated:Put", 256), "receipt-4")}},
		},
	}

	loopCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()

	task.RunS3EventLoop(loopCtx, baseDeps(fq, fr, map[string]string{bucket4: lib.ID}))

	files, err := fr.ListByLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ListByLibrary: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	if files[0].MissingSince != nil {
		t.Error("want MissingSince nil after ObjectCreated on missing file")
	}
}

// TestS3EventLoop_EmptyRecords verifies that a message with no Records
// is dispatched and deleted without error.
func TestS3EventLoop_EmptyRecords(t *testing.T) {
	fr, lr := newEventTestRepo(t)
	ctx := context.Background()

	_, err := lr.CreateLibrary(ctx, "Lib5", "lib5", "/tmp/lib5", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	fq := &fakeSQS{
		responses: []*awssqs.ReceiveMessageOutput{
			{Messages: []sqstypes.Message{sqsMsg(`{"Records":[]}`, "receipt-5")}},
		},
	}

	loopCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()

	task.RunS3EventLoop(loopCtx, baseDeps(fq, fr, map[string]string{}))

	if len(fq.deleted) != 1 {
		t.Errorf("want 1 deleted receipt, got %d", len(fq.deleted))
	}
}

// TestS3EventLoop_CtxCancellation verifies the loop exits cleanly when ctx
// is cancelled.
func TestS3EventLoop_CtxCancellation(t *testing.T) {
	fr, _ := newEventTestRepo(t)

	fq := &fakeSQS{} // always returns empty

	loopCtx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		task.RunS3EventLoop(loopCtx, task.S3EventLoopDeps{
			SQS:             fq,
			QueueURL:        "https://sqs.us-east-1.amazonaws.com/123/q",
			Files:           fr,
			BucketToLibrary: map[string]string{},
			PollInterval:    5 * time.Millisecond,
		})
	}()

	cancel()

	select {
	case <-done:
		// Clean exit.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("RunS3EventLoop did not exit after context cancellation")
	}
}

// TestS3EventLoop_NilDeps verifies the loop returns immediately when
// required deps are missing.
func TestS3EventLoop_NilDeps(t *testing.T) {
	ctx := context.Background()
	done := make(chan struct{})
	go func() {
		defer close(done)
		task.RunS3EventLoop(ctx, task.S3EventLoopDeps{}) // all nil/empty
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("RunS3EventLoop with nil deps did not return immediately")
	}
}
