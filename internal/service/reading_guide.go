// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/llm"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
)

// readingGuideStore is the slice of BookReadingGuideRepo this service
// writes through.
type readingGuideStore interface {
	Upsert(ctx context.Context, g model.ReadingGuide) error
}

// bookSourceOpener yields a book's bytes with random access, which the
// EPUB extractor needs to read a zip central directory. Implemented over
// LibraryStore; never over os.Open(book.Path), which is how device push
// on S3 libraries was once silently broken.
type bookSourceOpener interface {
	Open(ctx context.Context, book model.Book) (storage.Source, error)
}

// GuideCompleter is the slice of llm.Client used here. Narrow, so the
// generator is testable without a model or a network. Exported because
// the queue job's Completer seam needs a name for what it returns.
type GuideCompleter interface {
	ChatJSON(ctx context.Context, msgs []llm.Message, out any) error
}

// ReadingGuideOptions configures generation.
type ReadingGuideOptions struct {
	// Language every guide is written in, regardless of the book's own
	// (ADR-0024 §6).
	Language string
	// TextCap bounds how much book text reaches the model. This is the
	// cost and compatibility dial, not a safety valve: a 300-page book
	// extracts to ~433k characters (~110k tokens), which is beyond most
	// local models' context and expensive across a whole library. The
	// default sends roughly the opening 30-40 pages, which is where a
	// book says what it is and who it is for.
	TextCap int64
	// Model name, recorded as provenance on every row it writes.
	Model string
}

// DefaultGuideTextCap is ~12k tokens: fits a 16k-context local model and
// costs cents per book.
const DefaultGuideTextCap = 48_000

// ReadingGuideService writes reading guides (ADR-0024). Single entry
// point: Generate.
type ReadingGuideService struct {
	guides   readingGuideStore
	books    bookSourceOpener
	llm      GuideCompleter
	markdown *MarkdownFeed
	opts     ReadingGuideOptions
}

// NewReadingGuideService builds the generator. markdown may be nil —
// an instance without the converter extension wired generates exactly
// as before ADR-0033: EPUB from its own text, everything else from
// metadata.
func NewReadingGuideService(
	guides readingGuideStore,
	books bookSourceOpener,
	completer GuideCompleter,
	markdown *MarkdownFeed,
	opts ReadingGuideOptions,
) *ReadingGuideService {
	if opts.TextCap <= 0 {
		opts.TextCap = DefaultGuideTextCap
	}
	if strings.TrimSpace(opts.Language) == "" {
		opts.Language = "en"
	}
	return &ReadingGuideService{
		guides: guides, books: books, llm: completer, markdown: markdown, opts: opts,
	}
}

// ErrEmptyGuide is returned when the model answered in the right shape
// with nothing in it. Persisting that would leave a blank guide that
// looks generated and would be skipped by nothing.
var ErrEmptyGuide = errors.New("model returned an empty reading guide")

// Generate writes a reading guide for one book and returns it.
//
// The book's own text is used where the format can give it — EPUB today.
// Anything else, and any EPUB that will not yield text, falls back to a
// metadata-only guide rather than no guide, with SourceKind recording
// which happened so the reader knows how much the model actually saw.
func (s *ReadingGuideService) Generate(ctx context.Context, book model.Book) (model.ReadingGuide, error) {
	text, kind, err := s.readSource(ctx, book)
	if err != nil {
		return model.ReadingGuide{}, err
	}

	var reply guideReply
	if err := s.llm.ChatJSON(ctx, s.buildPrompt(book, text, kind), &reply); err != nil {
		return model.ReadingGuide{}, fmt.Errorf("generate guide: %w", err)
	}
	guideText := reply.toModel()
	if isBlankGuide(guideText) {
		return model.ReadingGuide{}, ErrEmptyGuide
	}

	g := model.ReadingGuide{
		BookID:           book.ID,
		ReadingGuideText: guideText,
		SourceKind:       kind,
		Model:            s.opts.Model,
		Language:         s.opts.Language,
	}
	if err := s.guides.Upsert(ctx, g); err != nil {
		return model.ReadingGuide{}, fmt.Errorf("save guide: %w", err)
	}
	return g, nil
}

// readSource returns the book text to prompt with and what it represents.
//
// The EPUB path never fails: an unreadable EPUB degrades to a
// metadata-only guide, because the alternative is a library where some
// books simply have no guide and the user cannot tell why. The
// Convertible path (ADR-0033) is the opposite by design: text is one
// conversion away, so degrading silently to metadata is exactly the
// quiet failure the loud-failure rule refuses. It errors instead —
// pending while the rendition converts, or the conversion's own message
// verbatim.
func (s *ReadingGuideService) readSource(ctx context.Context, book model.Book) (string, model.GuideSource, error) {
	if strings.EqualFold(book.Format, "EPUB") {
		return s.readEPUBSource(ctx, book)
	}
	if s.markdown != nil && model.Convertible(book.Format) {
		text, err := s.markdown.Text(ctx, book, s.opts.TextCap)
		if err != nil {
			return "", "", err
		}
		return text, model.GuideSourceFullText, nil
	}
	// Not attempted rather than attempted-and-failed: CBZ would need
	// OCR, audio transcription; and with no converter wired a PDF is in
	// the same place it was before ADR-0033. Opening the bytes to
	// rediscover that would cost a full download per book on an
	// S3-backed library.
	return "", model.GuideSourceMetadata, nil
}

func (s *ReadingGuideService) readEPUBSource(ctx context.Context, book model.Book) (string, model.GuideSource, error) {
	src, err := s.books.Open(ctx, book)
	if err != nil {
		slog.Warn("reading guide: open book", "book", book.ID, "err", err)
		return "", model.GuideSourceMetadata, nil
	}
	defer func() { _ = src.Close() }()

	text, truncated, err := fileproc.ExtractEPUBText(ctx, src, s.opts.TextCap)
	if err != nil {
		// Includes ErrNoReadableText — a picture book, or an archive we
		// cannot parse. Both are metadata guides.
		slog.Warn("reading guide: extract text", "book", book.ID, "err", err)
		return "", model.GuideSourceMetadata, nil
	}
	if truncated {
		slog.Debug("reading guide: text truncated", "book", book.ID, "cap", s.opts.TextCap)
	}
	return text, model.GuideSourceFullText, nil
}

// guideReply is the JSON contract the prompt asks for. Field names are
// part of that contract — the prompt names them literally.
type guideReply struct {
	About    string `json:"about"`
	Audience string `json:"audience"`
	NotFor   string `json:"not_for"`
	Problems string `json:"problems"`
}

func (r guideReply) toModel() model.ReadingGuideText {
	return model.ReadingGuideText{
		About:    strings.TrimSpace(r.About),
		Audience: strings.TrimSpace(r.Audience),
		NotFor:   strings.TrimSpace(r.NotFor),
		Problems: strings.TrimSpace(r.Problems),
	}
}

func isBlankGuide(t model.ReadingGuideText) bool {
	return t.About == "" && t.Audience == "" && t.NotFor == "" && t.Problems == ""
}

const guideSystemPrompt = `You write short orientations for books in a personal library.
Answer only with a JSON object, no prose around it, with exactly these keys:
  "about"     - what the book is actually about, 2-3 sentences
  "audience"  - who will get value from it, and why
  "not_for"   - who should skip it, and why; be specific and honest
  "problems"  - the reader problems it addresses, concretely
Do not repeat the title back. Do not praise the book. Do not invent facts.`

// buildPrompt assembles the exchange. When there is no book text the
// system turn says so explicitly: a model told nothing about its own
// ignorance writes as if it read the book, which is precisely the
// confident invention SourceKind warns readers about.
func (s *ReadingGuideService) buildPrompt(book model.Book, text string, kind model.GuideSource) []llm.Message {
	system := guideSystemPrompt + "\nWrite in this language: " + s.opts.Language + "."
	if kind == model.GuideSourceMetadata {
		system += "\nYou have not read this book — only the catalog metadata below. " +
			"Rely on what you know of the title and author, and say plainly when you are unsure."
	}

	var b strings.Builder
	b.WriteString("Title: " + book.Title + "\n")
	if book.Author != "" {
		b.WriteString("Author: " + book.Author + "\n")
	}
	if book.Series != "" {
		b.WriteString("Series: " + book.Series + "\n")
	}
	if len(book.Genres) > 0 {
		b.WriteString("Genres: " + strings.Join(book.Genres, ", ") + "\n")
	}
	if book.Year > 0 {
		fmt.Fprintf(&b, "Year: %d\n", book.Year)
	}
	if book.Publisher != "" {
		b.WriteString("Publisher: " + book.Publisher + "\n")
	}
	if book.Description != "" {
		b.WriteString("Publisher description: " + book.Description + "\n")
	}
	if text != "" {
		b.WriteString("\nText of the book (may be truncated):\n")
		b.WriteString(text)
	}

	return []llm.Message{
		{Role: llm.RoleSystem, Content: system},
		{Role: llm.RoleUser, Content: b.String()},
	}
}

// LibraryBookOpener is the production bookSourceOpener: it resolves a
// book's library, then opens its bytes through the handle. Going through
// LibraryStore rather than os.Open(book.Path) is what keeps the guide
// generator working on S3-backed libraries.
type LibraryBookOpener struct {
	store LibraryStore
}

func NewLibraryBookOpener(store LibraryStore) *LibraryBookOpener {
	return &LibraryBookOpener{store: store}
}

func (o *LibraryBookOpener) Open(ctx context.Context, book model.Book) (storage.Source, error) {
	if o == nil || o.store == nil {
		return nil, errors.New("no library store configured")
	}
	handle, err := o.store.For(ctx, book.LibraryID)
	if err != nil {
		return nil, fmt.Errorf("resolve library %s: %w", book.LibraryID, err)
	}
	return handle.OpenBookSource(ctx, book)
}
