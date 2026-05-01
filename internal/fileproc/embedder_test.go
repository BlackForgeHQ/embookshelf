package fileproc

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

// bytesSource adapts a []byte to storage.Source for tests.
type bytesSource struct {
	data []byte
	r    *bytes.Reader
}

func newBytesSource(data []byte) *bytesSource {
	return &bytesSource{data: data, r: bytes.NewReader(data)}
}

func (b *bytesSource) ReadAt(p []byte, off int64) (int, error) {
	return b.r.ReadAt(p, off)
}
func (b *bytesSource) Close() error { return nil }
func (b *bytesSource) Size() int64  { return int64(len(b.data)) }

func TestMinimalFixture_Parses(t *testing.T) {
	data := makeMinimalEPUB(t)
	src := newBytesSource(data)
	defer func() { _ = src.Close() }()
	m, err := EPUBProcessor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if m.Title != "Original Title" {
		t.Errorf("Title=%q want Original Title", m.Title)
	}
	if m.Author != "Original Author" {
		t.Errorf("Author=%q want Original Author", m.Author)
	}
	if !m.HasCover {
		t.Error("HasCover=false; want true")
	}
}

func TestDispatchEmbedder_UnsupportedFormat(t *testing.T) {
	_, err := DispatchEmbedder("CBZ")
	if !errors.Is(err, ErrUnsupportedEmbed) {
		t.Errorf("got %v, want ErrUnsupportedEmbed", err)
	}
}

func TestDispatchEmbedder_EPUB(t *testing.T) {
	emb, err := DispatchEmbedder("EPUB")
	if err != nil {
		t.Fatalf("DispatchEmbedder(EPUB): %v", err)
	}
	if _, ok := emb.(EPUBEmbedder); !ok {
		t.Errorf("got %T, want EPUBEmbedder", emb)
	}
}
