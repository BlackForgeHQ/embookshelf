// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
)

// renditionFinish owns the tail every converter worker ends with, the
// way renditionJob owns the head (#341). Three things the two workers
// used to restate: the staged file's lifetime (a byte-identical defer),
// the rejection verdict on the converter call (a *ConvertRejectedError
// is the document itself refusing — same bytes, same answer — so it is
// permanent), and the read-source-hash-before-Record ordering, which
// was an invariant enforced by the same comment in both files. Here the
// ordering is structural: finish reads the hash before it hands Record
// the staged file to consume.
type renditionFinish struct {
	book       model.Book
	sourceHash func(context.Context, model.Book) []byte
	record     func(context.Context, model.Book, string) (service.DerivedRecord, error)

	result service.ConvertResult
}

// cleanup removes whatever is staged; defer it before any step runs, so
// every exit path — refusal, failure, success after Record consumed the
// file — leaves nothing behind.
func (f *renditionFinish) cleanup() {
	if f.result.Path != "" {
		_ = os.Remove(f.result.Path)
	}
}

// convert wraps the artifact's converter call as a renditionRun step
// and keeps its staged result for finish. The rejection verdict lives
// here and nowhere else.
func (f *renditionFinish) convert(run func(context.Context) (service.ConvertResult, error)) renditionStep {
	return func(ctx context.Context) (string, bool, error) {
		res, err := run(ctx)
		if err != nil {
			var rejected *service.ConvertRejectedError
			return err.Error(), errors.As(err, &rejected), err
		}
		f.result = res
		return "", false, nil
	}
}

// finish is the sealing step: source hash first — Record consumes the
// staged file, and the hash is what answers "is this rendition still
// current" — then Record, then the artifact's own seal with the record,
// the hash and the converter version in hand.
func (f *renditionFinish) finish(
	label string,
	seal func(ctx context.Context, rec service.DerivedRecord, sourceHash []byte, version string) error,
) renditionStep {
	return func(ctx context.Context) (string, bool, error) {
		sourceHash := f.sourceHash(ctx, f.book)

		rec, err := f.record(ctx, f.book, f.result.Path)
		if err != nil {
			return label + ": " + err.Error(), false, fmt.Errorf("%s for %s: %w", label, f.book.ID, err)
		}
		if err := seal(ctx, rec, sourceHash, f.result.Version); err != nil {
			return "", false, fmt.Errorf("mark ready: %w", err)
		}
		return "", false, nil
	}
}
