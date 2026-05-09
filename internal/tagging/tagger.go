// SPDX-License-Identifier: AGPL-3.0-or-later

// Package tagging classifies books into hot/warm/cold tiers based on
// recency of last-read events and writes the result back to S3 via
// PutObjectTagging. Tier tags drive bucket lifecycle rules that
// transition cold objects to cheaper storage classes (e.g. GLACIER_IR).
package tagging

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Tier is the classification a lifecycle rule keys off.
type Tier string

const (
	// TierHot marks a book read within the last 90 days.
	TierHot Tier = "hot"
	// TierWarm marks a book read 91–365 days ago.
	TierWarm Tier = "warm"
	// TierCold marks a book never read, or last read more than 365 days ago.
	TierCold Tier = "cold"
)

// Classify returns the Tier for a book given its last-read time.
// A zero lastRead (book never opened) returns TierCold.
func Classify(now, lastRead time.Time) Tier {
	if lastRead.IsZero() {
		return TierCold
	}
	age := now.Sub(lastRead)
	switch {
	case age <= 90*24*time.Hour:
		return TierHot
	case age <= 365*24*time.Hour:
		return TierWarm
	default:
		return TierCold
	}
}

// TagWriter is the S3 surface the Apply path needs. *s3.Client satisfies
// this interface so callers can pass the client directly.
type TagWriter interface {
	PutObjectTagging(ctx context.Context, in *s3.PutObjectTaggingInput, optFns ...func(*s3.Options)) (*s3.PutObjectTaggingOutput, error)
}

// Apply writes tier=<value> as an object tag on the object at key in bucket.
func Apply(ctx context.Context, tw TagWriter, bucket, key string, tier Tier) error {
	_, err := tw.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Tagging: &types.Tagging{
			TagSet: []types.Tag{{
				Key:   aws.String("tier"),
				Value: aws.String(string(tier)),
			}},
		},
	})
	if err != nil {
		return fmt.Errorf("tagging: put %q: %w", key, err)
	}
	return nil
}
