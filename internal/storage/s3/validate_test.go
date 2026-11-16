// SPDX-License-Identifier: AGPL-3.0-or-later

package s3_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	s3backend "github.com/blackforge/embookshelf/internal/storage/s3"
)

const accessDenied = `<?xml version="1.0" encoding="UTF-8"?>
<Error><Code>AccessDenied</Code><Message>Access Denied</Message></Error>`

// TestNewWithObjectScopedCredentials: a token that may list objects but
// not read bucket config (R2 "Object Read & Write") must still boot;
// one that can't list either must not.
func TestNewWithObjectScopedCredentials(t *testing.T) {
	for _, tc := range []struct {
		name     string
		listCode int
		wantErr  bool
	}{
		{"list allowed", http.StatusOK, false},
		{"list denied", http.StatusForbidden, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				if r.URL.Query().Has("versioning") || tc.listCode != http.StatusOK {
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(accessDenied))
					return
				}
				_, _ = w.Write([]byte(`<ListBucketResult><Name>b</Name><KeyCount>0</KeyCount></ListBucketResult>`))
			}))
			defer srv.Close()

			_, err := s3backend.New(context.Background(), s3backend.Config{
				Endpoint:        srv.URL,
				Region:          "auto",
				Bucket:          "b",
				AccessKeyID:     "test",
				SecretAccessKey: "test",
				ForcePathStyle:  true,
			})
			if (err != nil) != tc.wantErr {
				t.Fatalf("New() err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
