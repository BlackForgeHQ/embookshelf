// SPDX-License-Identifier: AGPL-3.0-or-later

package task_test

import (
	"archive/zip"
	"bytes"
	"context"
	"strconv"
	"testing"

	"github.com/blackforge/embookshelf/internal/audio"
	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/tts"
)

// ---------------------------------------------------------------------------
// MP3 fixtures
// ---------------------------------------------------------------------------

// mpeg1FrameHeader is one MPEG-1 Layer III frame header: 128 kbit/s,
// 44.1 kHz, stereo, no CRC. audio.Payload rejects anything that is not
// this, and both the staging and the assembly paths run through it.
//
// Duplicated from internal/audio's own test support rather than exported
// from it: a frame builder is a fixture, and putting one in the
// production surface of a package whose job is parsing real files would
// be worse than four lines of repetition.
var mpeg1FrameHeader = []byte{0xFF, 0xFB, 0x90, 0x00}

// mp3FrameBytes is the size of the frame above: 144 * 128000 / 44100.
const mp3FrameBytes = 417

// mp3Frames builds n back-to-back frames of silence.
func mp3Frames(n int) []byte {
	var b bytes.Buffer
	for range n {
		b.Write(mpeg1FrameHeader)
		b.Write(make([]byte, mp3FrameBytes-len(mpeg1FrameHeader)))
	}
	return b.Bytes()
}

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
type fakeEngine struct {
	reply    []byte
	err      error
	calls    int
	requests []tts.Request
}

func (e *fakeEngine) Synthesize(_ context.Context, req tts.Request) ([]byte, error) {
	e.calls++
	e.requests = append(e.requests, req)
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

// The fixtures are load-bearing: audio.Payload rejects a frame it does
// not recognise, and ExtractEPUBSegments rejects an archive it cannot
// walk. Both would fail every test downstream with an error about the
// fixture rather than about the worker, so they are checked here once.
func TestFixturesAreWhatTheProductionParsersExpect(t *testing.T) {
	frames, durationMS, err := audio.Payload(mp3Frames(4))
	if err != nil {
		t.Fatalf("audio.Payload rejected the frame fixture: %v", err)
	}
	if len(frames) != 4*mp3FrameBytes {
		t.Errorf("payload is %d bytes, want %d", len(frames), 4*mp3FrameBytes)
	}
	if durationMS <= 0 {
		t.Errorf("duration is %dms, want a positive measurement", durationMS)
	}

	src := epubWithChapters(t, "One sentence. Another sentence.", "A second chapter.")
	segs, err := fileproc.ExtractEPUBSegments(context.Background(), src, fileproc.SegmentOptions{MaxChars: 1000})
	if err != nil {
		t.Fatalf("ExtractEPUBSegments rejected the EPUB fixture: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("extracted %d segments, want one per spine item", len(segs))
	}
}
