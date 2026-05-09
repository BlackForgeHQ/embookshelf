// SPDX-License-Identifier: AGPL-3.0-or-later

package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/blackforge/embookshelf/internal/storage"
)

// List returns an iterator over objects under prefix.
func (b *Backend) List(ctx context.Context, prefix string) (storage.Iterator, error) {
	fullPrefix := b.keyFor(prefix)
	p := s3.NewListObjectsV2Paginator(b.cli, &s3.ListObjectsV2Input{
		Bucket: &b.bucket,
		Prefix: &fullPrefix,
	})
	return &s3Iter{b: b, p: p}, nil
}

// Head returns metadata for a single key.
func (b *Backend) Head(ctx context.Context, key string) (storage.ObjectInfo, error) {
	out, err := b.cli.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(b.keyFor(key)),
	})
	if err != nil {
		var nf *types.NotFound
		if errors.As(err, &nf) {
			return storage.ObjectInfo{}, errors.Join(storage.ErrNotFound, err)
		}
		return storage.ObjectInfo{}, err
	}
	return storage.ObjectInfo{
		Key:         key,
		Size:        valueOr(out.ContentLength, 0),
		ETag:        strings.Trim(strValue(out.ETag), "\""),
		ModTime:     valueOr(out.LastModified, time.Time{}),
		ContentType: strValue(out.ContentType),
	}, nil
}

// Get returns a stream for the given key. Supports byte-range reads via
// WithRange; length <= 0 means "to EOF".
func (b *Backend) Get(ctx context.Context, key string, opts ...storage.GetOption) (io.ReadCloser, error) {
	o := storage.ApplyGet(opts)
	in := &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(b.keyFor(key)),
	}
	if o.RangeSet {
		if o.RangeLength <= 0 {
			in.Range = aws.String(fmt.Sprintf("bytes=%d-", o.RangeOffset))
		} else {
			end := o.RangeOffset + o.RangeLength - 1
			in.Range = aws.String(fmt.Sprintf("bytes=%d-%d", o.RangeOffset, end))
		}
	}
	out, err := b.cli.GetObject(ctx, in)
	if err != nil {
		var nk *types.NoSuchKey
		if errors.As(err, &nk) {
			return nil, errors.Join(storage.ErrNotFound, err)
		}
		// HeadObject returns NotFound for missing objects; GetObject may
		// return a generic HTTP 404 on some backends.
		var re *smithyhttp.ResponseError
		if errors.As(err, &re) && re.HTTPStatusCode() == http.StatusNotFound {
			return nil, errors.Join(storage.ErrNotFound, err)
		}
		return nil, err
	}
	return out.Body, nil
}

// Put writes r to key. Buffers the reader in memory (no multipart;
// acceptable up to 5 GB per object — Plan F trade-off documented in plan).
// Conditional options (WithIfMatch / WithIfNoneMatch) use S3's native
// IfMatch / IfNoneMatch request fields and surface ErrPreconditionFailed
// on 412 responses.
func (b *Backend) Put(ctx context.Context, key string, r io.Reader, opts ...storage.PutOption) (storage.PutResult, error) {
	o := storage.ApplyPut(opts)

	body, err := io.ReadAll(r)
	if err != nil {
		return storage.PutResult{}, err
	}

	in := &s3.PutObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(b.keyFor(key)),
		Body:   bytes.NewReader(body),
	}
	if o.ContentType != "" {
		in.ContentType = aws.String(o.ContentType)
	}
	if o.IfMatchSet {
		in.IfMatch = aws.String(o.IfMatch)
	}
	if o.IfNoneMatchSet {
		in.IfNoneMatch = aws.String(o.IfNoneMatch)
	}

	out, err := b.cli.PutObject(ctx, in)
	if err != nil {
		var re *smithyhttp.ResponseError
		if errors.As(err, &re) && re.HTTPStatusCode() == http.StatusPreconditionFailed {
			return storage.PutResult{}, errors.Join(storage.ErrPreconditionFailed, err)
		}
		return storage.PutResult{}, err
	}
	return storage.PutResult{
		ETag:      strings.Trim(strValue(out.ETag), "\""),
		VersionID: strValue(out.VersionId),
	}, nil
}

// Delete removes a key. A missing key is not an error.
func (b *Backend) Delete(ctx context.Context, key string, opts ...storage.DeleteOption) error {
	o := storage.ApplyDelete(opts)
	in := &s3.DeleteObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(b.keyFor(key)),
	}
	if o.VersionID != "" {
		in.VersionId = aws.String(o.VersionID)
	}
	_, err := b.cli.DeleteObject(ctx, in)
	if err != nil {
		var nk *types.NoSuchKey
		if errors.As(err, &nk) {
			return nil
		}
		return err
	}
	return nil
}

// Copy duplicates srcKey to dstKey via a server-side copy.
func (b *Backend) Copy(ctx context.Context, srcKey, dstKey string) (storage.CopyResult, error) {
	copySource := b.bucket + "/" + b.keyFor(srcKey)
	out, err := b.cli.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(b.bucket),
		CopySource: aws.String(copySource),
		Key:        aws.String(b.keyFor(dstKey)),
	})
	if err != nil {
		var nk *types.NoSuchKey
		if errors.As(err, &nk) {
			return storage.CopyResult{}, errors.Join(storage.ErrNotFound, err)
		}
		var nf *types.NotFound
		if errors.As(err, &nf) {
			return storage.CopyResult{}, errors.Join(storage.ErrNotFound, err)
		}
		return storage.CopyResult{}, err
	}
	etag := ""
	if out.CopyObjectResult != nil {
		etag = strings.Trim(strValue(out.CopyObjectResult.ETag), "\"")
	}
	return storage.CopyResult{ETag: etag}, nil
}
