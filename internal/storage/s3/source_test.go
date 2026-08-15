// SPDX-License-Identifier: AGPL-3.0-or-later

package s3_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/storage"
	s3backend "github.com/blackforge/embookshelf/internal/storage/s3"
	"github.com/blackforge/embookshelf/internal/storage/storagetest"
)

// TestReadAtTimesOutOnAStalledEndpoint pins #332 through the shared
// conformance arm (#343): ReadAt issues its GetObject with
// context.Background(), so an endpoint that accepts the connection and
// never answers used to hang the caller forever. The stub server
// answers HEAD (so Open can size the source) and stalls every GET; the
// deadline comes in through Config.ReadTimeout — the exported dial, not
// the unexported back-door this test used to reach for.
func TestReadAtTimesOutOnAStalledEndpoint(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", "1024")
			w.WriteHeader(http.StatusOK)
			return
		}
		<-block // never respond until the test releases us
	}))
	defer srv.Close()
	defer close(block) // unblock the handler before Close waits on it

	storagetest.RunSourceReadBound(t, func(t *testing.T) storage.Source {
		t.Helper()
		b, err := s3backend.New(context.Background(), s3backend.Config{
			Endpoint:        srv.URL,
			Region:          "us-east-1",
			Bucket:          "test-bucket",
			AccessKeyID:     "test",
			SecretAccessKey: "test",
			ForcePathStyle:  true,
			SkipValidation:  true,
			ReadTimeout:     50 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		src, err := b.Open(context.Background(), "some/key")
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		return src
	}, 2*time.Second)
}
