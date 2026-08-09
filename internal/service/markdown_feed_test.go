// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
)

var errNoRow = errors.New("no row")

type fakeRenditionReader struct {
	row     model.MarkdownRendition
	missing bool
}

func (f *fakeRenditionReader) GetByBookID(context.Context, string) (model.MarkdownRendition, error) {
	if f.missing {
		return model.MarkdownRendition{}, errNoRow
	}
	return f.row, nil
}

func feedWith(reader *fakeRenditionReader, requested *[]string) *MarkdownFeed {
	return &MarkdownFeed{
		Renditions: reader,
		IsMissing:  func(err error) bool { return errors.Is(err, errNoRow) },
		Open: func(_ context.Context, _ model.Book, location string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("# markdown body\n")), nil
		},
		CurrentHash: func(context.Context, model.Book) []byte { return []byte{0x01} },
		Request: func(_ context.Context, bookID string) error {
			*requested = append(*requested, bookID)
			return nil
		},
	}
}

func feedBook() model.Book { return model.Book{ID: "b1", Format: "PDF"} }

func TestMarkdownFeedReadsAFreshRendition(t *testing.T) {
	var requested []string
	feed := feedWith(&fakeRenditionReader{row: model.MarkdownRendition{
		State: model.MarkdownRenditionReady, Location: "A/T/T.md",
		SourceContentHash: []byte{0x01},
	}}, &requested)

	text, err := feed.Text(context.Background(), feedBook(), 48_000)
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	if text != "# markdown body\n" {
		t.Fatalf("text = %q", text)
	}
	if len(requested) != 0 {
		t.Fatal("a fresh rendition triggered a conversion")
	}
}

// TestMarkdownFeedRequestsWhatIsMissing — absent means "never asked",
// so the feed asks and reports pending; the guide job's retry is the
// wait.
func TestMarkdownFeedRequestsWhatIsMissing(t *testing.T) {
	var requested []string
	feed := feedWith(&fakeRenditionReader{missing: true}, &requested)

	_, err := feed.Text(context.Background(), feedBook(), 48_000)
	if !errors.Is(err, ErrRenditionPending) {
		t.Fatalf("err = %v, want ErrRenditionPending", err)
	}
	if len(requested) != 1 || requested[0] != "b1" {
		t.Fatalf("requested = %v", requested)
	}
}

// TestMarkdownFeedRequestsWhatIsStale — a rendition of older bytes is
// re-requested rather than silently fed to the model (ADR-0033 §4).
func TestMarkdownFeedRequestsWhatIsStale(t *testing.T) {
	var requested []string
	feed := feedWith(&fakeRenditionReader{row: model.MarkdownRendition{
		State: model.MarkdownRenditionReady, Location: "A/T/T.md",
		SourceContentHash: []byte{0xff},
	}}, &requested)

	_, err := feed.Text(context.Background(), feedBook(), 48_000)
	if !errors.Is(err, ErrRenditionPending) {
		t.Fatalf("err = %v, want ErrRenditionPending", err)
	}
	if len(requested) != 1 {
		t.Fatalf("requested = %v", requested)
	}
}

// TestMarkdownFeedFailureIsVerbatim — the rendition row's error crosses
// untouched; it is what the admin will read.
func TestMarkdownFeedFailureIsVerbatim(t *testing.T) {
	var requested []string
	feed := feedWith(&fakeRenditionReader{row: model.MarkdownRendition{
		State: model.MarkdownRenditionFailed,
		Error: "converter extension is not configured",
	}}, &requested)

	_, err := feed.Text(context.Background(), feedBook(), 48_000)
	var failed *RenditionFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("err = %v, want RenditionFailedError", err)
	}
	if failed.Msg != "converter extension is not configured" {
		t.Fatalf("Msg = %q", failed.Msg)
	}
	if len(requested) != 0 {
		t.Fatal("a failed rendition must not silently re-request — the admin acts first")
	}
}

func TestMarkdownFeedPendingWhileConverting(t *testing.T) {
	var requested []string
	feed := feedWith(&fakeRenditionReader{row: model.MarkdownRendition{
		State: model.MarkdownRenditionRunning,
	}}, &requested)

	_, err := feed.Text(context.Background(), feedBook(), 48_000)
	if !errors.Is(err, ErrRenditionPending) {
		t.Fatalf("err = %v, want ErrRenditionPending", err)
	}
	if len(requested) != 0 {
		t.Fatal("a running conversion was re-requested")
	}
}

// TestMarkdownFeedCapsText — the text cap is the cost dial and applies
// to markdown exactly as it does to EPUB extraction.
func TestMarkdownFeedCapsText(t *testing.T) {
	var requested []string
	feed := feedWith(&fakeRenditionReader{row: model.MarkdownRendition{
		State: model.MarkdownRenditionReady, Location: "A/T/T.md",
		SourceContentHash: []byte{0x01},
	}}, &requested)

	text, err := feed.Text(context.Background(), feedBook(), 4)
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	if text != "# ma" {
		t.Fatalf("text = %q, want capped to 4 bytes", text)
	}
}
