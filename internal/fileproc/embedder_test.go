package fileproc

import (
	"errors"
	"testing"
)

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
