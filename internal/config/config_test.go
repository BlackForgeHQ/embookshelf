// SPDX-License-Identifier: AGPL-3.0-or-later

package config_test

import (
	"testing"

	"github.com/blackforge/embookshelf/internal/config"
)

func TestLoad_SharedS3_Configured(t *testing.T) {
	t.Setenv("EMBOOKSHELF_S3_BUCKET", "my-bucket")
	t.Setenv("EMBOOKSHELF_S3_REGION", "eu-west-1")
	t.Setenv("EMBOOKSHELF_S3_ENDPOINT", "https://minio.local")
	t.Setenv("EMBOOKSHELF_S3_ACCESS_KEY_ID", "AKID")
	t.Setenv("EMBOOKSHELF_S3_SECRET_ACCESS_KEY", "SECRET")
	t.Setenv("EMBOOKSHELF_S3_FORCE_PATH_STYLE", "true")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	s3 := cfg.SharedS3
	if s3.Bucket != "my-bucket" {
		t.Errorf("Bucket=%q want %q", s3.Bucket, "my-bucket")
	}
	if s3.Region != "eu-west-1" {
		t.Errorf("Region=%q want %q", s3.Region, "eu-west-1")
	}
	if s3.Endpoint != "https://minio.local" {
		t.Errorf("Endpoint=%q want %q", s3.Endpoint, "https://minio.local")
	}
	if s3.AccessKeyID != "AKID" {
		t.Errorf("AccessKeyID=%q want %q", s3.AccessKeyID, "AKID")
	}
	if s3.SecretAccessKey != "SECRET" {
		t.Errorf("SecretAccessKey=%q want %q", s3.SecretAccessKey, "SECRET")
	}
	if !s3.ForcePathStyle {
		t.Error("ForcePathStyle should be true")
	}
	if !s3.Configured() {
		t.Error("Configured() should return true when bucket is set")
	}
}

func TestLoad_SharedS3_NotConfigured(t *testing.T) {
	// Ensure the env vars are absent.
	t.Setenv("EMBOOKSHELF_S3_BUCKET", "")
	t.Setenv("EMBOOKSHELF_S3_REGION", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SharedS3.Configured() {
		t.Error("Configured() should return false when bucket is empty")
	}
	// Region should stay empty (not defaulted) when bucket is unset.
	if cfg.SharedS3.Region != "" {
		t.Errorf("Region=%q want empty when bucket is unset", cfg.SharedS3.Region)
	}
}

func TestLoad_SharedS3_DefaultRegion(t *testing.T) {
	// When bucket is set but region is not, region should default to us-east-1.
	t.Setenv("EMBOOKSHELF_S3_BUCKET", "auto-region-bucket")
	t.Setenv("EMBOOKSHELF_S3_REGION", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SharedS3.Region != "us-east-1" {
		t.Errorf("Region=%q want %q", cfg.SharedS3.Region, "us-east-1")
	}
}
