// SPDX-License-Identifier: AGPL-3.0-or-later

package s3

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/blackforge/embookshelf/internal/storage"
)

// s3Iter is a lazy iterator over a ListObjectsV2 paginator. It pulls
// one page at a time to avoid loading all keys into memory at once.
type s3Iter struct {
	b   *Backend
	p   *s3.ListObjectsV2Paginator
	buf []types.Object
}

// Next returns the next object. Returns io.EOF when exhausted.
func (it *s3Iter) Next(ctx context.Context) (storage.ObjectInfo, error) {
	for len(it.buf) == 0 {
		if !it.p.HasMorePages() {
			return storage.ObjectInfo{}, io.EOF
		}
		page, err := it.p.NextPage(ctx)
		if err != nil {
			return storage.ObjectInfo{}, err
		}
		it.buf = page.Contents
	}
	obj := it.buf[0]
	it.buf = it.buf[1:]
	return storage.ObjectInfo{
		Key:     it.b.stripPrefix(strValue(obj.Key)),
		Size:    valueOr(obj.Size, int64(0)),
		ETag:    strings.Trim(strValue(obj.ETag), "\""),
		ModTime: valueOr(obj.LastModified, time.Time{}),
	}, nil
}

// Close releases iterator resources.
func (it *s3Iter) Close() error {
	it.buf = nil
	return nil
}
