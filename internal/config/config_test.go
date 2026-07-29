// SPDX-License-Identifier: AGPL-3.0-or-later

package config_test

import (
	"os"
	"path/filepath"
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

// DataPath roots every local library, and since ADR-0030 a local
// library's storage is rooted at "/" — so the path stored on the row is
// resolved as an absolute key on read.
//
// Left relative, the two disagree: LibraryService.Create makes the
// directory relative to the process's working directory, and every
// later read looks for it at the filesystem root. The library is created
// successfully, the books import successfully, and every file fetch
// 403s with "no such file or directory /data/...". The repo's own .env
// ships DATA_PATH=./data, so this was the default dev experience.
func TestLoadResolvesARelativeDataPath(t *testing.T) {
	t.Setenv("DATA_PATH", "./data")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !filepath.IsAbs(cfg.DataPath) {
		t.Errorf("DataPath = %q, want it resolved against the working directory — "+
			"a local library rooted here is read back from the filesystem root (ADR-0030 §1)",
			cfg.DataPath)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if want := filepath.Join(wd, "data"); cfg.DataPath != want {
		t.Errorf("DataPath = %q, want %q", cfg.DataPath, want)
	}
}

// An absolute path is already what everything downstream expects and
// must survive untouched — this is the deployed shape (DATA_PATH=/data
// in the container).
func TestLoadLeavesAnAbsoluteDataPathAlone(t *testing.T) {
	t.Setenv("DATA_PATH", "/srv/embookshelf/data")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DataPath != "/srv/embookshelf/data" {
		t.Errorf("DataPath = %q, want it unchanged", cfg.DataPath)
	}
}
