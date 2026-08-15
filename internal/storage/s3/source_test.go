// SPDX-License-Identifier: AGPL-3.0-or-later

package s3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestReadAtTimesOutOnAStalledEndpoint pins #332: ReadAt issues its
// GetObject with context.Background(), so an endpoint that accepts the
// connection and never answers used to hang the caller forever. The
// stub server below does exactly that — it never writes a response —
// and readTimeout is set far below the production default so the test
// stays fast while still exercising the real deadline plumbing.
func TestReadAtTimesOutOnAStalledEndpoint(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // never respond until the test releases us
	}))
	defer srv.Close()
	defer close(block) // unblock the handler before Close waits on it

	b, err := New(context.Background(), Config{
		Endpoint:       srv.URL,
		Region:         "us-east-1",
		Bucket:         "test-bucket",
		ForcePathStyle: true,
		SkipValidation: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// 50ms is safe only because readTimeout is an instance field on this
	// test's own local *Backend/s3Source, not a shared package var — no
	// risk of racing or leaking into another test.
	b.readTimeout = 50 * time.Millisecond

	src := &s3Source{
		cli:         b.cli,
		bucket:      b.bucket,
		key:         "some/key",
		size:        1024,
		readTimeout: b.readTimeout,
	}

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 10)
		_, rerr := src.ReadAt(buf, 0)
		done <- rerr
	}()

	select {
	case rerr := <-done:
		if rerr == nil {
			t.Fatal("ReadAt returned nil error against a stalled endpoint")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReadAt did not return within the bound — a stalled endpoint " +
			"hangs the caller forever (#332)")
	}
}

// TestReadAtUsesDefaultTimeoutWhenUnset documents the zero-value
// contract: a source built without an explicit readTimeout (the normal
// path, via Backend.Open) falls back to s3ReadTimeout rather than
// blocking forever or racing with a zero timeout.
func TestReadAtUsesDefaultTimeoutWhenUnset(t *testing.T) {
	src := &s3Source{}
	if got := src.timeout(); got != s3ReadTimeout {
		t.Fatalf("timeout() = %v, want the package default %v", got, s3ReadTimeout)
	}
}
