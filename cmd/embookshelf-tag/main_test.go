// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	s3backend "github.com/blackforge/embookshelf/internal/storage/s3"
)

// fakeS3 answers the two bucket probes storage/s3.New makes on
// construction, for any bucket name. It exists so this test can build a
// real *s3.Backend without a real bucket — the assertion is about which
// configuration the backend was built from, not about S3 itself.
func fakeS3(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("versioning") {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` +
				`<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
				`<Status>Enabled</Status></VersioningConfiguration>`))
			return
		}
		// GetBucketEncryption and anything else: not configured. The
		// backend treats this as a soft failure.
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestBootStorage_reconcilesBeforeLoading pins the ordering this binary
// used to skip. The row on disk names the bucket it was created with; the
// environment has since moved on. A resolver built from the row as
// written would tag objects in the old bucket.
func TestBootStorage_reconcilesBeforeLoading(t *testing.T) {
	d := repotest.New(t)
	ctx := context.Background()
	endpoint := fakeS3(t)

	backendRepo := repo.NewStorageBackendRepo(d)
	row, err := backendRepo.Create(ctx, "s3", map[string]any{
		"bucket":            "stale-bucket",
		"region":            "us-east-1",
		"endpoint":          endpoint,
		"prefix":            "libraries/novels/",
		"access_key_id":     "OLDKEY",
		"secret_access_key": "OLDSECRET",
		"force_path_style":  true,
	})
	if err != nil {
		t.Fatalf("create backend row: %v", err)
	}

	shared := config.SharedS3Config{
		Bucket:          "fresh-bucket",
		Region:          "eu-central-1",
		Endpoint:        endpoint,
		AccessKeyID:     "NEWKEY",
		SecretAccessKey: "NEWSECRET",
		ForcePathStyle:  true,
	}

	resolver, err := bootStorage(ctx, backendRepo, shared)
	if err != nil {
		t.Fatalf("bootStorage: %v", err)
	}

	backend, err := resolver.Resolve(row.ID)
	if err != nil {
		t.Fatalf("resolve %s: %v", row.ID, err)
	}
	s3b, ok := backend.(*s3backend.Backend)
	if !ok {
		t.Fatalf("resolved %T, want *s3.Backend", backend)
	}
	if got := s3b.Bucket(); got != shared.Bucket {
		t.Fatalf("bucket = %q, want %q — the backend was built from the row before the reconcile ran", got, shared.Bucket)
	}
	// The per-library prefix is the row's own and must survive the
	// reconcile; only the bucket-level fields come from the environment.
	if got := s3b.Prefix(); got != "libraries/novels/" {
		t.Fatalf("prefix = %q, want %q", got, "libraries/novels/")
	}
}
