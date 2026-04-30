// go:build s3integration
//
// Run: go test -tags s3integration ./internal/storage/s3/
//
// Requires a running S3-compatible service (minio recommended):
//   TEST_S3_ENDPOINT  - e.g. http://localhost:9000 (required to run)
//   TEST_S3_BUCKET    - bucket to use (default: embookshelf-test)
//   TEST_S3_AK        - access key id
//   TEST_S3_SK        - secret access key

//go:build s3integration

package s3_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	s3backend "github.com/blackforge/embookshelf/internal/storage/s3"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/storagetest"
)

func TestS3Backend_Contract(t *testing.T) {
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("set TEST_S3_ENDPOINT to run s3integration tests")
	}
	bucket := os.Getenv("TEST_S3_BUCKET")
	if bucket == "" {
		bucket = "embookshelf-test"
	}

	storagetest.RunSuite(t, func(t *testing.T) (storage.Storage, func()) {
		b, err := s3backend.New(t.Context(), s3backend.Config{
			Endpoint:        endpoint,
			Region:          "us-east-1",
			Bucket:          bucket,
			Prefix:          fmt.Sprintf("test-%d/", time.Now().UnixNano()),
			AccessKeyID:     os.Getenv("TEST_S3_AK"),
			SecretAccessKey: os.Getenv("TEST_S3_SK"),
			ForcePathStyle:  true,
			SkipValidation:  true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return b, func() { /* scratch prefix; CI bucket recycles on schedule */ }
	})
}
