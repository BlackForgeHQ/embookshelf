// SPDX-License-Identifier: AGPL-3.0-or-later

package task_test

import (
	"archive/zip"
	"bytes"
	"context"
	"strconv"
	"testing"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/tts"
)

// ---------------------------------------------------------------------------
// EPUB fixture
// ---------------------------------------------------------------------------

// memSrc is a byte slice as a storage.Source.
type memSrc struct {
	*bytes.Reader
	size int64
}

func (m memSrc) Size() int64  { return m.size }
func (m memSrc) Close() error { return nil }

// epubWithChapters builds a minimal EPUB, one spine item per body, which
// is what ExtractEPUBSegments walks.
func epubWithChapters(t *testing.T, bodies ...string) storage.Source {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip: %v", err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	add("META-INF/container.xml", `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container">`+
		`<rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`)

	var manifest, spine bytes.Buffer
	for i, body := range bodies {
		id := "c" + strconv.Itoa(i)
		href := id + ".xhtml"
		manifest.WriteString(`<item id="` + id + `" href="` + href + `" media-type="application/xhtml+xml"/>`)
		spine.WriteString(`<itemref idref="` + id + `"/>`)
		add(href, `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><body><p>`+body+`</p></body></html>`)
	}
	add("content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0">`+
		`<manifest>`+manifest.String()+`</manifest><spine>`+spine.String()+`</spine></package>`)

	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	b := buf.Bytes()
	return memSrc{Reader: bytes.NewReader(b), size: int64(len(b))}
}

// ---------------------------------------------------------------------------
// Fakes
//
// Not called from this file: audiobook_segment_test.go, audiobook_finalize_test.go
// and reading_guide_test.go substitute these for repo.BookRepo and a
// tts.Engine, which is what lets those tests run without Postgres or a
// live TTS endpoint.
// ---------------------------------------------------------------------------

// audioUpdate records what a finalize wrote back onto the book row.
type audioUpdate struct {
	DurationSeconds *int
	Narrator        string
	Chapters        []model.Chapter
}

// fakeBooks is a book row plus recorder for the audio write-back. It
// satisfies both bookReader and bookAudioWriter, which is what lets one
// fake stand in for the segment, finalize and guide workers' book seam.
type fakeBooks struct {
	book  model.Book
	err   error
	audio *audioUpdate
}

func (f *fakeBooks) GetByID(_ context.Context, _, _ string) (model.Book, error) {
	if f.err != nil {
		return model.Book{}, f.err
	}
	return f.book, nil
}

func (f *fakeBooks) UpdateAudio(
	_ context.Context, _ string, durationSeconds *int, narrator string, chapters []model.Chapter,
) error {
	f.audio = &audioUpdate{DurationSeconds: durationSeconds, Narrator: narrator, Chapters: chapters}
	return nil
}

// fakeEngine is a tts.Engine that never leaves the process.
//
// chunks simulates the per-request splitting that internal/tts now owns
// (Task 4): a segment worker makes exactly one Synthesize call per
// segment, and it is the adapter — real or fake — that turns that into
// however many engine calls its cap needs. chunks defaults to one
// simulated piece so every test that does not care about chunking still
// sees the one request it always saw.
//
// It models chunked's BeforeChunk-before-each-piece ordering only, not
// its full behaviour: no ctx.Err() pre-check, the same outer Request
// (whole-segment Text, live BeforeChunk) is recorded on every simulated
// piece rather than a fresh per-piece Request, and e.err surfaces only
// after all n pieces are counted rather than at the first failure. Fine
// for what this package's tests assert; don't lean on any of the three.
type fakeEngine struct {
	reply    []byte
	err      error
	chunks   int
	calls    int
	requests []tts.Request
}

func (e *fakeEngine) Synthesize(ctx context.Context, req tts.Request) ([]byte, error) {
	n := e.chunks
	if n <= 0 {
		n = 1
	}
	for range n {
		// Mirrors chunked.Synthesize in internal/tts: the callback runs
		// before every simulated piece, unwrapped, so a caller matching
		// errCanceled with errors.Is still works.
		if req.BeforeChunk != nil {
			if err := req.BeforeChunk(ctx); err != nil {
				return nil, err
			}
		}
		e.calls++
		e.requests = append(e.requests, req)
	}
	if e.err != nil {
		return nil, e.err
	}
	return e.reply, nil
}

func (e *fakeEngine) ListVoices(context.Context) ([]tts.Voice, error) { return nil, nil }

// fakeEngine satisfies tts.Engine at compile time — self-verifying, unlike
// a nolint comment, and still worth keeping after the worker tests consume
// it: it breaks loudly if the fake ever drifts from the real interface.
var _ tts.Engine = (*fakeEngine)(nil)

// The EPUB fixture is load-bearing: ExtractEPUBSegments rejects an
// archive it cannot walk, which would fail every test downstream with an
// error about the fixture rather than about the worker, so it is checked
// here once. (The audio frame fixture used to get the same treatment
// here too, but audiotest validates its own fixture — see
// audiotest_test.go — so asserting it again in this package was
// redundant.)
func TestFixtureIsWhatTheProductionParserExpects(t *testing.T) {
	src := epubWithChapters(t, "One sentence. Another sentence.", "A second chapter.")
	segs, err := fileproc.ExtractEPUBSegments(context.Background(), src, fileproc.SegmentOptions{MaxChars: 1000})
	if err != nil {
		t.Fatalf("ExtractEPUBSegments rejected the EPUB fixture: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("extracted %d segments, want one per spine item", len(segs))
	}
}
