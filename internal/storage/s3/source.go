// SPDX-License-Identifier: AGPL-3.0-or-later

package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/blackforge/embookshelf/internal/storage"
)

// DefaultReadTimeout bounds a single ReadAt end to end — the GetObject
// round trip plus draining its body — so a stalled or slow S3 endpoint
// errors out instead of hanging the caller forever (#332; the contract
// is stated on storage.Source since #343). ReadAt implements
// io.ReaderAt, which carries no context, so a fixed deadline derived
// from context.Background() is the only shape available here; deriving
// it from the ctx passed to Open instead would tie a page-cache fill's
// reads to the request that happened to trigger it, and the fill must
// outlive that request (see TestPageCacheFillOutlivesTheRequesterThatStartedIt).
//
// Operators dial it with Config.ReadTimeout. Sizing of the default: the
// largest single range read this application issues is a full-size
// comic page or cover, capped at 32 MiB elsewhere in fileproc. At a
// deliberately pessimistic 256 KB/s (2 Mbit/s — well below what anyone
// would call broadband) that read takes 32 MiB / 256 KB/s = 128s. 3
// minutes (180s) leaves ~1.4x headroom over that worst case (180/128 ≈
// 1.41), enough for the SDK's own internal retry/backoff on a merely
// slow endpoint while still bounding a genuinely stalled one.
const DefaultReadTimeout = 3 * time.Minute

// s3Source is a random-access view of an S3 object. Each ReadAt
// issues a GetObject with a Range header.
//
// This is appropriate for small reads (zip directory at EOF, OPF
// rootfile, PDF XREF table) where the alternative would be downloading
// the entire object. For full-file streaming use Backend.Get instead.
type s3Source struct {
	cli    *s3.Client
	bucket string
	key    string
	size   int64
	closed bool

	// readTimeout is the resolved per-read deadline — Config.ReadTimeout,
	// or DefaultReadTimeout when the config left it zero. Always set by
	// Open; never zero.
	readTimeout time.Duration
}

func (s *s3Source) Size() int64 { return s.size }

func (s *s3Source) ReadAt(p []byte, off int64) (int, error) {
	if s.closed {
		return 0, errors.New("s3 source: closed")
	}
	if off >= s.size {
		return 0, io.EOF
	}
	end := off + int64(len(p)) - 1
	if end >= s.size {
		end = s.size - 1
	}
	// The deadline covers the GetObject round trip AND the body read
	// below: cancel is deferred past io.ReadFull, not fired the moment
	// GetObject returns. The response body is fully drained and closed
	// inside this function before the context can be cancelled by
	// anything other than the timeout itself, so there is no live
	// streaming body left holding a dying context when ReadAt returns.
	ctx, cancel := context.WithTimeout(context.Background(), s.readTimeout)
	defer cancel()
	out, err := s.cli.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucket,
		Key:    &s.key,
		Range:  aws.String(fmt.Sprintf("bytes=%d-%d", off, end)),
	})
	if err != nil {
		return 0, err
	}
	defer func() { _ = out.Body.Close() }()
	n, rerr := io.ReadFull(out.Body, p[:end-off+1])
	if rerr == io.ErrUnexpectedEOF {
		rerr = nil
	}
	if n < len(p) && rerr == nil {
		rerr = io.EOF
	}
	return n, rerr
}

func (s *s3Source) Close() error { s.closed = true; return nil }

// Open returns a random-access view of the object at key. Returns
// ErrNotFound when missing. Callers must Close the returned Source.
func (b *Backend) Open(ctx context.Context, key string) (storage.Source, error) {
	out, err := b.cli.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(b.keyFor(key)),
	})
	if err != nil {
		var nf *types.NotFound
		if errors.As(err, &nf) {
			return nil, errors.Join(storage.ErrNotFound, err)
		}
		return nil, err
	}
	return &s3Source{
		cli:         b.cli,
		bucket:      b.bucket,
		key:         b.keyFor(key),
		size:        valueOr(out.ContentLength, 0),
		readTimeout: b.readTimeout,
	}, nil
}
