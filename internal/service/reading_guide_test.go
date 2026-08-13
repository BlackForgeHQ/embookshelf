// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/llm"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
)

// --- fakes ---------------------------------------------------------------

type fakeGuideStore struct {
	saved []model.ReadingGuide
	err   error
}

func (f *fakeGuideStore) Upsert(_ context.Context, g model.ReadingGuide) error {
	if f.err != nil {
		return f.err
	}
	f.saved = append(f.saved, g)
	return nil
}

// fakeCompleter records the prompt and replies with canned JSON.
type fakeCompleter struct {
	msgs  []llm.Message
	reply map[string]string
	err   error
	calls int
}

func (f *fakeCompleter) ChatJSON(_ context.Context, msgs []llm.Message, out any) error {
	f.calls++
	f.msgs = msgs
	if f.err != nil {
		return f.err
	}
	raw, _ := json.Marshal(f.reply)
	return json.Unmarshal(raw, out)
}

func (f *fakeCompleter) prompt() string {
	var b strings.Builder
	for _, m := range f.msgs {
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}

type fakeBookOpener struct {
	src   storage.Source
	err   error
	opens int
}

func (f *fakeBookOpener) Open(context.Context, model.Book) (storage.Source, error) {
	f.opens++
	if f.err != nil {
		return nil, f.err
	}
	return f.src, nil
}

// memSrc is a byte slice as a storage.Source.
type memSrc struct {
	*bytes.Reader
	size int64
}

func (m memSrc) Size() int64  { return m.size }
func (m memSrc) Close() error { return nil }

// epubWithText builds a minimal EPUB whose single chapter holds body.
func epubWithText(t *testing.T, body string) storage.Source {
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
	add("META-INF/container.xml", `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`)
	add("content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><manifest><item id="c1" href="one.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c1"/></spine></package>`)
	add("one.xhtml", `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><body><p>`+body+`</p></body></html>`)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	b := buf.Bytes()
	return memSrc{Reader: bytes.NewReader(b), size: int64(len(b))}
}

// --- harness -------------------------------------------------------------

type guideHarness struct {
	svc    *ReadingGuideService
	store  *fakeGuideStore
	llm    *fakeCompleter
	opener *fakeBookOpener
}

func newGuideHarness(t *testing.T, src storage.Source) *guideHarness {
	t.Helper()
	h := &guideHarness{
		store: &fakeGuideStore{},
		llm: &fakeCompleter{reply: map[string]string{
			"about": "About text", "audience": "Audience text",
			"not_for": "Not-for text", "problems": "Problems text",
		}},
		opener: &fakeBookOpener{src: src},
	}
	h.svc = NewReadingGuideService(h.store, h.opener, h.llm, nil, ReadingGuideOptions{
		Language: "en", TextCap: 48_000, Model: "test-model",
	})
	return h
}

func epubBook() model.Book {
	return model.Book{
		ID: "b1", Title: "Dune", Author: "Frank Herbert", Format: "EPUB",
		Description: "Publisher blurb.", Genres: []string{"sci-fi"},
	}
}

// --- source selection ----------------------------------------------------

func TestGuideUsesFullTextForEPUB(t *testing.T) {
	h := newGuideHarness(t, epubWithText(t, "Spice must flow across the desert."))

	got, err := h.svc.Generate(context.Background(), epubBook())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got.SourceKind != model.GuideSourceFullText {
		t.Fatalf("SourceKind = %q, want full_text", got.SourceKind)
	}
	if !strings.Contains(h.llm.prompt(), "Spice must flow") {
		t.Error("the book's text never reached the prompt")
	}
}

// TestGuideUsesMetadataForFormatsWithoutText — PDF, CBZ and audio have no
// extractable text, and opening their bytes to discover that would cost a
// full download per book on an S3-backed library.
func TestGuideUsesMetadataForFormatsWithoutText(t *testing.T) {
	for _, format := range []string{"PDF", "CBZ", "MP3", "M4B"} {
		t.Run(format, func(t *testing.T) {
			h := newGuideHarness(t, nil)
			b := epubBook()
			b.Format = format

			got, err := h.svc.Generate(context.Background(), b)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if got.SourceKind != model.GuideSourceMetadata {
				t.Errorf("SourceKind = %q, want metadata", got.SourceKind)
			}
			if h.opener.opens != 0 {
				t.Errorf("opened the file for %s, which has no text to read", format)
			}
		})
	}
}

// TestGuideFallsBackWhenEPUBHasNoText — a picture book or a broken archive
// still gets a guide, marked so the reader knows it was written without
// the book (ADR-0024 §2).
func TestGuideFallsBackWhenEPUBHasNoText(t *testing.T) {
	empty := epubWithText(t, "")
	h := newGuideHarness(t, empty)

	got, err := h.svc.Generate(context.Background(), epubBook())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got.SourceKind != model.GuideSourceMetadata {
		t.Fatalf("SourceKind = %q, want metadata after an unreadable EPUB", got.SourceKind)
	}
}

func TestGuideFallsBackWhenOpenFails(t *testing.T) {
	h := newGuideHarness(t, nil)
	h.opener.err = errors.New("storage down")

	got, err := h.svc.Generate(context.Background(), epubBook())
	if err != nil {
		t.Fatalf("Generate = %v, want a metadata guide rather than a failure", err)
	}
	if got.SourceKind != model.GuideSourceMetadata {
		t.Errorf("SourceKind = %q, want metadata", got.SourceKind)
	}
}

// TestGuideCapsTextSent — the cap is the cost dial. A real 300-page book
// extracts to ~433k characters; sending that per book is both expensive
// and beyond most local models' context.
func TestGuideCapsTextSent(t *testing.T) {
	long := strings.Repeat("desert ", 20_000) // ~140k chars
	h := newGuideHarness(t, epubWithText(t, long))

	if _, err := h.svc.Generate(context.Background(), epubBook()); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if n := len(h.llm.prompt()); n > 60_000 {
		t.Fatalf("prompt is %d chars, cap was 48k — the limit did not bind", n)
	}
}

// --- prompt contents -----------------------------------------------------

func TestGuidePromptCarriesMetadataAndContract(t *testing.T) {
	h := newGuideHarness(t, epubWithText(t, "text"))

	if _, err := h.svc.Generate(context.Background(), epubBook()); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	p := h.llm.prompt()
	for _, want := range []string{
		"Dune", "Frank Herbert", "Publisher blurb.", "sci-fi",
		"about", "audience", "not_for", "problems",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
}

// TestGuidePromptRequestsConfiguredLanguage — guides are written in one
// configured language regardless of the book's own (ADR-0024 §6).
func TestGuidePromptRequestsConfiguredLanguage(t *testing.T) {
	h := newGuideHarness(t, epubWithText(t, "text"))
	h.svc.opts.Language = "ru"

	if _, err := h.svc.Generate(context.Background(), epubBook()); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(strings.ToLower(h.llm.prompt()), "ru") {
		t.Error("prompt does not name the configured language")
	}
}

// TestGuidePromptSaysWhenItIsMetadataOnly — without this the model writes
// as though it read the book, which is exactly the confident invention the
// source_kind label exists to warn readers about.
func TestGuidePromptSaysWhenItIsMetadataOnly(t *testing.T) {
	h := newGuideHarness(t, nil)
	b := epubBook()
	b.Format = "MP3"

	if _, err := h.svc.Generate(context.Background(), b); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(strings.ToLower(h.llm.prompt()), "have not read") {
		t.Errorf("metadata-only prompt does not tell the model it lacks the text:\n%s", h.llm.prompt())
	}
}

// --- persistence and failure ---------------------------------------------

func TestGuidePersistsWithProvenance(t *testing.T) {
	h := newGuideHarness(t, epubWithText(t, "text"))

	if _, err := h.svc.Generate(context.Background(), epubBook()); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(h.store.saved) != 1 {
		t.Fatalf("saved %d guides, want 1", len(h.store.saved))
	}
	g := h.store.saved[0]
	if g.BookID != "b1" || g.Model != "test-model" || g.Language != "en" {
		t.Errorf("provenance wrong: %+v", g)
	}
	if g.About != "About text" || g.NotFor != "Not-for text" {
		t.Errorf("text not carried: %+v", g)
	}
}

func TestGuideDoesNotPersistWhenModelFails(t *testing.T) {
	h := newGuideHarness(t, epubWithText(t, "text"))
	h.llm.err = errors.New("model unavailable")

	if _, err := h.svc.Generate(context.Background(), epubBook()); err == nil {
		t.Fatal("Generate returned nil despite the model failing")
	}
	if len(h.store.saved) != 0 {
		t.Errorf("wrote a guide anyway: %+v", h.store.saved)
	}
}

// TestGuideRejectsEmptyResponse — a model that returns the right shape with
// nothing in it would otherwise persist a blank guide that looks generated
// and excludes the book from nothing.
func TestGuideRejectsEmptyResponse(t *testing.T) {
	h := newGuideHarness(t, epubWithText(t, "text"))
	h.llm.reply = map[string]string{"about": "  ", "audience": "", "not_for": "", "problems": ""}

	if _, err := h.svc.Generate(context.Background(), epubBook()); err == nil {
		t.Fatal("an empty guide was accepted")
	}
	if len(h.store.saved) != 0 {
		t.Errorf("persisted an empty guide: %+v", h.store.saved)
	}
}

func TestGuideSurfacesStoreFailure(t *testing.T) {
	h := newGuideHarness(t, epubWithText(t, "text"))
	h.store.err = errors.New("db down")

	if _, err := h.svc.Generate(context.Background(), epubBook()); err == nil {
		t.Fatal("Generate returned nil despite the write failing")
	}
}

// --- markdown rendition consumption (ADR-0033, #288) ---------------------

// TestGuideUsesMarkdownRenditionForPDF — a Convertible-format book with a
// fresh rendition generates a full-text guide from the markdown, without
// touching the book's own bytes.
func TestGuideUsesMarkdownRenditionForPDF(t *testing.T) {
	h := newGuideHarness(t, nil)
	h.svc.markdown = &MarkdownFeed{
		Renditions: &fakeRenditionReader{row: model.MarkdownRendition{
			State: model.RenditionReady, Location: "A/T/T.md",
		}},
		Open: func(context.Context, model.Book, string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("# Sand and spice, in markdown.")), nil
		},
		CurrentHash: func(context.Context, model.Book) []byte { return nil },
	}
	b := epubBook()
	b.Format = "PDF"

	got, err := h.svc.Generate(context.Background(), b)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got.SourceKind != model.GuideSourceFullText {
		t.Fatalf("SourceKind = %q, want full_text", got.SourceKind)
	}
	if !strings.Contains(h.llm.prompt(), "Sand and spice") {
		t.Error("the rendition's markdown never reached the prompt")
	}
	if h.opener.opens != 0 {
		t.Error("opened the PDF's own bytes despite a rendition being available")
	}
}

// TestGuideSurfacesRenditionFailureVerbatim — never a silently degraded
// metadata guide when text was one conversion away (ADR-0033 §5): the
// conversion's own message is the error.
func TestGuideSurfacesRenditionFailureVerbatim(t *testing.T) {
	h := newGuideHarness(t, nil)
	h.svc.markdown = &MarkdownFeed{
		Renditions: &fakeRenditionReader{row: model.MarkdownRendition{
			State: model.RenditionFailed,
			Error: "converter extension is not configured",
		}},
	}
	b := epubBook()
	b.Format = "PDF"

	_, err := h.svc.Generate(context.Background(), b)
	var failed *RenditionFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("err = %v, want RenditionFailedError", err)
	}
	if failed.Msg != "converter extension is not configured" {
		t.Fatalf("Msg = %q, want verbatim", failed.Msg)
	}
	if len(h.store.saved) != 0 {
		t.Fatal("a guide row was written despite the failure")
	}
}

// TestGuideWithoutFeedKeepsPreConverterBehaviour — no feed wired means
// exactly the old world: PDF degrades to a metadata guide.
func TestGuideWithoutFeedKeepsPreConverterBehaviour(t *testing.T) {
	h := newGuideHarness(t, nil)
	b := epubBook()
	b.Format = "PDF"

	got, err := h.svc.Generate(context.Background(), b)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got.SourceKind != model.GuideSourceMetadata {
		t.Fatalf("SourceKind = %q, want metadata", got.SourceKind)
	}
}
