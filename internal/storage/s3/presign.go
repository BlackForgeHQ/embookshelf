// SPDX-License-Identifier: AGPL-3.0-or-later

package s3

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

// PresignGet returns a pre-signed URL for direct GET access to key.
// ttl is clamped to [1 minute, 7 days] (S3's hard limits).
//
// The caller should check Capabilities() & storage.CapPresign before
// calling this method; it is always present on the S3 backend.
func (b *Backend) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if ttl < time.Minute {
		ttl = time.Minute
	}
	if ttl > 7*24*time.Hour {
		ttl = 7 * 24 * time.Hour
	}
	req, err := b.psign.PresignGetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(b.keyFor(key)),
	}, awss3.WithPresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}
