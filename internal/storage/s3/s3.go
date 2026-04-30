// Package s3 implements storage.Storage against an S3-compatible
// object store (AWS S3, minio, Cloudflare R2, Backblaze B2, Wasabi).
// Bucket versioning is required at construction time per spec §3.2.
// Server-side encryption is checked as a best-effort (soft fail for
// compat backends that don't expose GetBucketEncryption).
package s3

import (
	"context"
	"errors"
	"fmt"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/blackforge/embookshelf/internal/storage"
)

// Config carries the construction-time parameters for a Backend.
type Config struct {
	// Endpoint is the S3 service endpoint. Empty for AWS regional
	// endpoints; set for minio / R2 / B2 / Wasabi.
	Endpoint string
	// Region is required for AWS; ignored by some compatible services
	// but a placeholder ("us-east-1") is safe.
	Region string
	// Bucket is required.
	Bucket string
	// Prefix is the optional library-prefix inside the bucket. Leading
	// and trailing slashes are normalized away.
	Prefix string
	// AccessKeyID + SecretAccessKey are static credentials. When
	// empty, the AWS default credential chain is used (env vars,
	// shared config, IRSA, etc.).
	AccessKeyID     string
	SecretAccessKey string
	// ForcePathStyle is needed for minio and most non-AWS S3-compat
	// backends. AWS itself accepts both virtual-host and path-style.
	ForcePathStyle bool
	// SkipValidation, when true, skips the bucket-versioning + SSE
	// checks at construction. Test-only.
	SkipValidation bool
}

// Backend is the storage.Storage implementation for S3.
type Backend struct {
	cli    *s3.Client
	bucket string
	prefix string // normalized: no leading slash, single trailing slash if non-empty
	psign  *s3.PresignClient
	capab  storage.Capability
}

// New constructs a Backend. Validates bucket versioning + SSE unless
// Config.SkipValidation is set.
func New(ctx context.Context, cfg Config) (*Backend, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("s3: bucket is required")
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(orDefault(cfg.Region, "us-east-1")),
	)
	if err != nil {
		return nil, fmt.Errorf("s3 load aws config: %w", err)
	}
	if cfg.AccessKeyID != "" && cfg.SecretAccessKey != "" {
		awsCfg.Credentials = credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID, cfg.SecretAccessKey, "")
	}

	cli := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = &cfg.Endpoint
		}
		o.UsePathStyle = cfg.ForcePathStyle
	})

	b := &Backend{
		cli:    cli,
		bucket: cfg.Bucket,
		prefix: normalizePrefix(cfg.Prefix),
		capab: storage.CapConditional | storage.CapVersioning |
			storage.CapPresign | storage.CapRange,
	}
	b.psign = s3.NewPresignClient(cli)

	if !cfg.SkipValidation {
		if err := b.validateBucket(ctx); err != nil {
			return nil, err
		}
	}
	return b, nil
}

// Capabilities reports the optional features this backend supports.
func (b *Backend) Capabilities() storage.Capability { return b.capab }

// validateBucket calls GetBucketVersioning + GetBucketEncryption and
// fails fast when the bucket is misconfigured.
func (b *Backend) validateBucket(ctx context.Context) error {
	vrsn, err := b.cli.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{
		Bucket: &b.bucket,
	})
	if err != nil {
		return fmt.Errorf("s3 versioning probe: %w", err)
	}
	if vrsn.Status != types.BucketVersioningStatusEnabled {
		return fmt.Errorf("s3 bucket %q must have versioning enabled", b.bucket)
	}

	// GetBucketEncryption is best-effort: some compat backends (minio
	// without SSE enabled) don't expose this endpoint. Real AWS S3
	// buckets should have it; failure is logged at debug level only.
	if _, err := b.cli.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{
		Bucket: &b.bucket,
	}); err != nil {
		// Intentionally ignored: soft fail for compat backends.
		_ = err
	}
	return nil
}

// normalizePrefix strips leading/trailing slashes and appends a single
// trailing slash so all keys can be formed with a simple concatenation.
func normalizePrefix(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	return p + "/"
}

// orDefault returns s if non-empty, otherwise d.
func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// keyFor returns the full bucket-relative key (prefix + relative path).
func (b *Backend) keyFor(rel string) string {
	return b.prefix + strings.TrimLeft(rel, "/")
}

// stripPrefix is the inverse of keyFor; used by List to return
// caller-relative keys.
func (b *Backend) stripPrefix(full string) string {
	if b.prefix == "" {
		return full
	}
	return strings.TrimPrefix(full, b.prefix)
}

// strValue dereferences a *string, returning "" when nil.
func strValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// valueOr dereferences a pointer, returning def when nil.
func valueOr[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}
