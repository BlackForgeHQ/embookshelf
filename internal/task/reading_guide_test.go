// SPDX-License-Identifier: AGPL-3.0-or-later

package task_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/llm"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/task"
)

// fakeGuides records what the worker wrote.
type fakeGuides struct {
	saved []model.ReadingGuide
	err   error
}

func (f *fakeGuides) Upsert(_ context.Context, g model.ReadingGuide) error {
	if f.err != nil {
		return f.err
	}
	f.saved = append(f.saved, g)
	return nil
}

// fakeCompleter answers the guide prompt with a well-formed reply.
type fakeCompleter struct {
	calls int
	err   error
}

func (f *fakeCompleter) ChatJSON(_ context.Context, _ []llm.Message, out any) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	return json.Unmarshal([]byte(
		`{"about":"About","audience":"Audience","not_for":"Not for","problems":"Problems"}`), out)
}

type guideHarness struct {
	deps      task.ReadingGuideDeps
	guides    *fakeGuides
	books     *fakeBooks
	completer *fakeCompleter
	published int
	builds    int
}

func newGuideHarness(t *testing.T) *guideHarness {
	t.Helper()
	h := &guideHarness{
		guides:    &fakeGuides{},
		books:     &fakeBooks{book: model.Book{ID: "b1", Title: "Dune", Author: "Frank Herbert", Format: "EPUB"}},
		completer: &fakeCompleter{},
	}
	src := epubWithChapters(t, "The spice must flow. It is the one thing that matters.")
	h.deps = task.ReadingGuideDeps{
		Config: func(context.Context) (repo.ReadingGuideConfig, error) {
			return repo.ReadingGuideConfig{
				Enabled: true, Model: "test-model", Language: "en", TextCap: 48_000,
			}, nil
		},
		Completer: func(repo.ReadingGuideConfig) (service.GuideCompleter, error) {
			h.builds++
			return h.completer, nil
		},
		Guides:  h.guides,
		Books:   h.books,
		Open:    func(context.Context, model.Book) (storage.Source, error) { return src, nil },
		Publish: func(string) { h.published++ },
	}
	return h
}

func (h *guideHarness) run() error {
	return task.ReadingGuide(context.Background(), task.ReadingGuideArgs{BookID: "b1"}, h.deps)
}

// A disabled feature will still be disabled in thirty seconds. River
// must treat this as permanent rather than retrying it 25 times.
func TestReadingGuideRefusesWhenTheFeatureIsDisabled(t *testing.T) {
	h := newGuideHarness(t)
	h.deps.Config = func(context.Context) (repo.ReadingGuideConfig, error) {
		return repo.ReadingGuideConfig{Enabled: false}, nil
	}

	err := h.run()

	if !errors.Is(err, task.ErrReadingGuidesDisabled) {
		t.Fatalf("err = %v, want ErrReadingGuidesDisabled", err)
	}
	if h.builds != 0 {
		t.Errorf("built a client %d times with the feature off", h.builds)
	}
}

// A book edited or deleted between enqueue and dispatch is why the row
// is re-read rather than baked into the payload.
func TestReadingGuideSurfacesAMissingBook(t *testing.T) {
	h := newGuideHarness(t)
	h.books.err = repo.ErrNotFound

	err := h.run()

	if err == nil {
		t.Fatal("ReadingGuide returned nil for a deleted book")
	}
	if len(h.guides.saved) != 0 {
		t.Errorf("saved %d guides for a book that no longer exists", len(h.guides.saved))
	}
}

func TestReadingGuideSavesAndPublishesOnce(t *testing.T) {
	h := newGuideHarness(t)

	if err := h.run(); err != nil {
		t.Fatalf("ReadingGuide: %v", err)
	}
	if len(h.guides.saved) != 1 {
		t.Fatalf("saved %d guides, want 1", len(h.guides.saved))
	}
	got := h.guides.saved[0]
	if got.BookID != "b1" {
		t.Errorf("book = %q, want b1", got.BookID)
	}
	if got.Model != "test-model" {
		t.Errorf("model = %q, want the settings row's, recorded as provenance", got.Model)
	}
	if got.SourceKind != model.GuideSourceFullText {
		t.Errorf("source = %q, want full-text — the EPUB yielded text", got.SourceKind)
	}
	if h.published != 1 {
		t.Errorf("published %d times, want exactly 1", h.published)
	}
}

func TestReadingGuideDoesNotPublishWhenGenerationFails(t *testing.T) {
	h := newGuideHarness(t)
	h.completer.err = errors.New("model unreachable")

	if err := h.run(); err == nil {
		t.Fatal("ReadingGuide returned nil when the model was unreachable")
	}
	if h.published != 0 {
		t.Errorf("published %d times for a guide that was never written", h.published)
	}
}
