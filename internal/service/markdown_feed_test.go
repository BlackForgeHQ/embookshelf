// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
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
		State: model.RenditionReady, Location: "A/T/T.md",
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
		State: model.RenditionReady, Location: "A/T/T.md",
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
		State: model.RenditionFailed,
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
		State: model.RenditionRunning,
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
		State: model.RenditionReady, Location: "A/T/T.md",
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

// ---------------------------------------------------------------------------
// NewMarkdownFeed — the pairings, decided once (#356)
// ---------------------------------------------------------------------------

// feedRowsFake is the constructor's whole row surface: state read,
// request half, bulk candidates.
type feedRowsFake struct {
	getErr  error
	started []string
}

func (f *feedRowsFake) GetByBookID(context.Context, string) (model.MarkdownRendition, error) {
	return model.MarkdownRendition{}, f.getErr
}
func (f *feedRowsFake) Start(_ context.Context, bookID string) error {
	f.started = append(f.started, bookID)
	return nil
}
func (f *feedRowsFake) MarkFailed(context.Context, string, string) error { return nil }
func (f *feedRowsFake) ListConversionCandidates(context.Context) ([]repo.ConversionCandidate, error) {
	return nil, nil
}

type feedEnqFake struct{ kinds []string }

func (e *feedEnqFake) Enqueue(_ context.Context, a jobs.Args) error {
	e.kinds = append(e.kinds, a.Kind())
	return nil
}

// The constructor's bindings, driven end to end: a missing row reads as
// "never requested" (the repo.ErrNotFound binding that used to live
// untested in the queue tier), and the request goes through the request
// module — Start lands on the row before the enqueue.
func TestNewMarkdownFeedBindsMissingAndRequest(t *testing.T) {
	rows := &feedRowsFake{getErr: repo.ErrNotFound}
	enq := &feedEnqFake{}
	feed := NewMarkdownFeed(rows, NewBookOps(nil, nil), enq)

	_, err := feed.Text(context.Background(), model.Book{ID: "b1", LibraryID: "l1"}, 1000)
	if !errors.Is(err, ErrRenditionPending) {
		t.Fatalf("Text on a never-requested book = %v, want ErrRenditionPending", err)
	}
	if len(rows.started) != 1 || rows.started[0] != "b1" {
		t.Fatalf("started = %v — the request must go through the request module's Start-before-Enqueue", rows.started)
	}
	if len(enq.kinds) != 1 {
		t.Fatalf("enqueued %v, want the markdown render job", enq.kinds)
	}

	// And a row read that fails with anything else is an error, not a
	// request — the other half of the ErrNotFound binding.
	rows.getErr = errors.New("db unavailable")
	rows.started = nil
	if _, err := feed.Text(context.Background(), model.Book{ID: "b1"}, 1000); errors.Is(err, ErrRenditionPending) || err == nil {
		t.Fatalf("a broken row read produced %v — must not read as never-requested", err)
	}
	if len(rows.started) != 0 {
		t.Fatalf("a broken read requested a conversion: %v", rows.started)
	}
}
