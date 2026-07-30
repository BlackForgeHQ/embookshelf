// SPDX-License-Identifier: AGPL-3.0-or-later

package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

// putPartSize is the multipart chunk size, and therefore the ceiling on
// what a Put holds in memory regardless of how large the object is. S3
// requires every part but the last to be at least 5 MiB; 8 MiB keeps a
// margin over that while capping a 10 000-part upload at 80 GB, far
// above anything this application stores.
const putPartSize = 8 << 20

// putMaxParts is S3's hard limit. Reaching it means the object is larger
// than putPartSize*putMaxParts, which is worth an explicit error rather
// than a confusing one from CompleteMultipartUpload.
const putMaxParts = 10000

// putHeadStart is where the first read's buffer starts before doubling
// towards putPartSize. Most objects this application writes are sidecars
// and covers; allocating a whole part for a two-kilobyte JSON file would
// trade one pathology for another.
const putHeadStart = 32 << 10

// Put writes r to key.
//
// The body is streamed: at most one putPartSize buffer is live at a
// time, so the heap cost of a put is bounded by the part size and not by
// the object. This matters because a narration for a full-length book is
// hundreds of megabytes and a server may be generating several at once —
// the previous implementation read the whole body with io.ReadAll before
// handing it to PutObject, which cost roughly 2.5x the object in
// allocation per upload (#266).
//
// Objects that fit in one buffer take the same single PutObject they
// always did, which keeps the common case (books, covers, sidecars)
// byte-for-byte identical, ETag included. Anything larger goes through
// multipart, whose ETag is S3's multipart form (`<hex>-<parts>`) rather
// than a content MD5 — the interface already documents ETag as an opaque
// change token that is never a content hash, and Head reports the same
// value back, so If-Match round-trips unchanged.
//
// Conditional options (WithIfMatch / WithIfNoneMatch) use S3's native
// IfMatch / IfNoneMatch request fields — on the PutObject for a small
// body, on the CompleteMultipartUpload for a streamed one, which is the
// point at which the object becomes visible either way — and surface
// ErrPreconditionFailed on 412 responses.
func (b *Backend) Put(ctx context.Context, key string, r io.Reader, opts ...storage.PutOption) (storage.PutResult, error) {
	o := storage.ApplyPut(opts)

	// The first read decides the path. Hitting EOF inside one part means
	// the whole object is in hand; filling the part means there may be
	// more, and the multipart branch takes over owning that same buffer.
	head, atEOF, err := readUpTo(r, putPartSize)
	if err != nil {
		return storage.PutResult{}, err
	}
	if atEOF {
		return b.putSingle(ctx, key, head, o)
	}
	return b.putMultipart(ctx, key, head, r, o)
}

// readUpTo reads at most limit bytes, growing its buffer by doubling
// from putHeadStart so that a small object costs a small allocation and
// a large one costs at most limit. It reports whether the reader was
// exhausted — the caller cannot infer that from a full buffer, and the
// difference is the whole choice between a single put and a multipart.
func readUpTo(r io.Reader, limit int) ([]byte, bool, error) {
	buf := make([]byte, 0, min(putHeadStart, limit))
	for {
		if len(buf) == cap(buf) {
			if cap(buf) >= limit {
				return buf, false, nil
			}
			grown := make([]byte, len(buf), min(cap(buf)*2, limit))
			copy(grown, buf)
			buf = grown
		}
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err != nil {
			if errors.Is(err, io.EOF) {
				return buf, true, nil
			}
			return nil, false, err
		}
	}
}

// putSingle is the unchanged single-request path, taken whenever the
// object fits in one part.
func (b *Backend) putSingle(ctx context.Context, key string, body []byte, o storage.PutOpts) (storage.PutResult, error) {
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
		return storage.PutResult{}, mapPreconditionErr(err)
	}
	return storage.PutResult{
		ETag:      strings.Trim(strValue(out.ETag), "\""),
		VersionID: strValue(out.VersionId),
	}, nil
}

// putMultipart uploads r as a sequence of parts, starting with first —
// the buffer Put already filled, which it then reuses for every
// subsequent part. Parts go up one at a time on purpose: concurrency
// would multiply the buffer count by the worker count, and the reason
// this path exists is to bound that number at one.
func (b *Backend) putMultipart(ctx context.Context, key string, first []byte, r io.Reader, o storage.PutOpts) (storage.PutResult, error) {
	fullKey := b.keyFor(key)

	createIn := &s3.CreateMultipartUploadInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(fullKey),
	}
	if o.ContentType != "" {
		createIn.ContentType = aws.String(o.ContentType)
	}
	created, err := b.cli.CreateMultipartUpload(ctx, createIn)
	if err != nil {
		return storage.PutResult{}, err
	}
	uploadID := created.UploadId

	// An abandoned multipart upload keeps its parts billable and
	// invisible until a lifecycle rule reaps them, so every failure below
	// aborts. WithoutCancel so a cancelled or timed-out put still cleans
	// up after itself.
	abort := func() {
		_, aerr := b.cli.AbortMultipartUpload(context.WithoutCancel(ctx),
			&s3.AbortMultipartUploadInput{
				Bucket:   aws.String(b.bucket),
				Key:      aws.String(fullKey),
				UploadId: uploadID,
			})
		if aerr != nil {
			slog.Warn("s3: could not abort a failed multipart upload; its "+
				"parts will linger until a lifecycle rule reaps them",
				"bucket", b.bucket, "key", fullKey, "err", aerr)
		}
	}

	var parts []types.CompletedPart
	chunk := first
	for {
		if len(parts) >= putMaxParts {
			abort()
			return storage.PutResult{}, fmt.Errorf(
				"s3: object %q exceeds %d bytes, the largest this backend can "+
					"upload in %d parts", key, int64(putPartSize)*putMaxParts, putMaxParts)
		}
		partNum := int32(len(parts) + 1) //nolint:gosec // bounded by putMaxParts

		up, err := b.cli.UploadPart(ctx, &s3.UploadPartInput{
			Bucket:     aws.String(b.bucket),
			Key:        aws.String(fullKey),
			UploadId:   uploadID,
			PartNumber: aws.Int32(partNum),
			Body:       bytes.NewReader(chunk),
		})
		if err != nil {
			abort()
			return storage.PutResult{}, err
		}
		parts = append(parts, types.CompletedPart{
			ETag:              up.ETag,
			PartNumber:        aws.Int32(partNum),
			ChecksumCRC32:     up.ChecksumCRC32,
			ChecksumCRC32C:    up.ChecksumCRC32C,
			ChecksumCRC64NVME: up.ChecksumCRC64NVME,
			ChecksumSHA1:      up.ChecksumSHA1,
			ChecksumSHA256:    up.ChecksumSHA256,
		})
		if len(chunk) < putPartSize {
			// A short part is by definition the last one.
			break
		}

		// Refill the one buffer for the next part. A zero-length tail is
		// dropped rather than sent, since S3 rejects an empty part.
		n, rerr := io.ReadFull(r, first)
		if rerr != nil && !errors.Is(rerr, io.EOF) && !errors.Is(rerr, io.ErrUnexpectedEOF) {
			abort()
			return storage.PutResult{}, rerr
		}
		if n == 0 {
			break
		}
		chunk = first[:n]
	}

	completeIn := &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(b.bucket),
		Key:             aws.String(fullKey),
		UploadId:        uploadID,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
	}
	// The preconditions belong here rather than on CreateMultipartUpload:
	// this is the request that makes the object visible, so evaluating
	// them any earlier would leave a window in which the condition held
	// at check time and not at write time.
	if o.IfMatchSet {
		completeIn.IfMatch = aws.String(o.IfMatch)
	}
	if o.IfNoneMatchSet {
		completeIn.IfNoneMatch = aws.String(o.IfNoneMatch)
	}

	out, err := b.cli.CompleteMultipartUpload(ctx, completeIn)
	if err != nil {
		abort()
		return storage.PutResult{}, mapPreconditionErr(err)
	}
	return storage.PutResult{
		ETag:      strings.Trim(strValue(out.ETag), "\""),
		VersionID: strValue(out.VersionId),
	}, nil
}

// mapPreconditionErr turns a 412 into the interface's sentinel so
// callers can match on it without knowing about HTTP.
func mapPreconditionErr(err error) error {
	var re *smithyhttp.ResponseError
	if errors.As(err, &re) && re.HTTPStatusCode() == http.StatusPreconditionFailed {
		return errors.Join(storage.ErrPreconditionFailed, err)
	}
	return err
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
