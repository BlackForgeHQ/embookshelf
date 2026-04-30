package s3

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/blackforge/embookshelf/internal/storage"
)

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
	out, err := s.cli.GetObject(context.Background(), &s3.GetObjectInput{
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
		cli:    b.cli,
		bucket: b.bucket,
		key:    b.keyFor(key),
		size:   valueOr(out.ContentLength, 0),
	}, nil
}
