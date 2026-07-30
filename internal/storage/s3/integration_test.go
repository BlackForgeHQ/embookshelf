// SPDX-License-Identifier: AGPL-3.0-or-later

// The S3 arm of the storage conformance suite. It used to sit behind a
// `s3integration` build tag *and* an endpoint check, so CI — which runs
// `go test -race ./...` with no tags — never compiled it, let alone ran
// it, and ADR-0030 §3's move-prefix disjunction was only ever asserted
// against the backend that returns both lists empty (#227).
//
// One gate now, not two: the file always compiles, and TEST_S3_ENDPOINT
// alone decides whether it runs. A developer with no object store gets a
// skip; CI sets the variable and gets the suite.
//
//	TEST_S3_ENDPOINT  - e.g. http://localhost:9000 (required to run)
//	TEST_S3_BUCKET    - bucket to use (default: embookshelf-test)
//	TEST_S3_AK        - access key id
//	TEST_S3_SK        - secret access key
//
// Run it with `make test-s3`, which starts the compose MinIO first.

package s3_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awstypes "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/blackforge/embookshelf/internal/storage"
	s3backend "github.com/blackforge/embookshelf/internal/storage/s3"
	"github.com/blackforge/embookshelf/internal/storage/storagetest"
)

func TestS3Backend_Contract(t *testing.T) {
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("set TEST_S3_ENDPOINT to run the S3 conformance suite " +
			"(`make test-s3` starts one)")
	}
	bucket := os.Getenv("TEST_S3_BUCKET")
	if bucket == "" {
		bucket = "embookshelf-test"
	}

	newBackend := func(t *testing.T, prefix string) *s3backend.Backend {
		t.Helper()
		b, err := s3backend.New(t.Context(), s3backend.Config{
			Endpoint:        endpoint,
			Region:          "us-east-1",
			Bucket:          bucket,
			Prefix:          prefix,
			AccessKeyID:     os.Getenv("TEST_S3_AK"),
			SecretAccessKey: os.Getenv("TEST_S3_SK"),
			ForcePathStyle:  true,
			SkipValidation:  true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	// Provision the bucket here rather than in compose or a CI step: the
	// suite then runs against any reachable object store, and the one
	// thing that has to be true of it is true by construction.
	ensureBucket(t, newBackend(t, "").Client(), bucket)

	storagetest.RunSuite(t, func(t *testing.T) (storage.Storage, func()) {
		// A scratch prefix per subtest is what makes "a fresh, empty
		// Storage" true on a store with no delete-all. Cleanup below
		// reaps it so repeated local runs don't accumulate.
		prefix := fmt.Sprintf("test-%d-%d/", time.Now().UnixNano(), os.Getpid())
		b := newBackend(t, prefix)
		return b, func() { purgePrefix(t, b.Client(), bucket, prefix) }
	})
}

func ensureBucket(t *testing.T, cli *awss3.Client, bucket string) {
	t.Helper()
	_, err := cli.CreateBucket(t.Context(), &awss3.CreateBucketInput{Bucket: &bucket})
	if err == nil {
		return
	}
	// Racing test binaries and re-runs both land here; only a genuinely
	// unusable bucket should fail the run.
	var owned *awstypes.BucketAlreadyOwnedByYou
	var exists *awstypes.BucketAlreadyExists
	if errors.As(err, &owned) || errors.As(err, &exists) {
		return
	}
	if _, headErr := cli.HeadBucket(t.Context(), &awss3.HeadBucketInput{Bucket: &bucket}); headErr == nil {
		return
	}
	t.Fatalf("create bucket %q at %s: %v", bucket, os.Getenv("TEST_S3_ENDPOINT"), err)
}

// purgePrefix deletes everything under prefix. Best-effort: a failure to
// clean up is not a contract violation, and reporting it as one would
// turn a full disk into a false negative about the interface.
func purgePrefix(t *testing.T, cli *awss3.Client, bucket, prefix string) {
	t.Helper()
	ctx := context.Background()
	p := awss3.NewListObjectsV2Paginator(cli, &awss3.ListObjectsV2Input{
		Bucket: &bucket,
		Prefix: &prefix,
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			t.Logf("cleanup list %q: %v", prefix, err)
			return
		}
		for _, o := range page.Contents {
			if _, err := cli.DeleteObject(ctx, &awss3.DeleteObjectInput{
				Bucket: &bucket,
				Key:    o.Key,
			}); err != nil {
				t.Logf("cleanup delete %q: %v", *o.Key, err)
			}
		}
	}
}
