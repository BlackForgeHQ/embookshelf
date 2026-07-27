// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/provider"
	"github.com/blackforge/embookshelf/internal/repo"
)

// ---------------------------------------------------------------------------
// Fakes for the narrow seams
// ---------------------------------------------------------------------------

type fakeProviderSettings struct {
	enabled   map[string]bool
	configs   map[string]json.RawMessage
	successes []string
	errs      []string
	// readErr makes EnabledIDs and List fail, standing in for the
	// provider_settings table being unreadable.
	readErr error
}

func newFakeProviderSettings() *fakeProviderSettings {
	return &fakeProviderSettings{enabled: map[string]bool{}, configs: map[string]json.RawMessage{}}
}

func (f *fakeProviderSettings) AllConfigs(context.Context) (map[string]json.RawMessage, error) {
	return f.configs, nil
}
func (f *fakeProviderSettings) EnabledIDs(context.Context) (map[string]bool, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.enabled, nil
}
func (f *fakeProviderSettings) List(context.Context) ([]repo.ProviderSetting, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	// Returns a nil slice when empty, matching what the repo yields for a
	// table with no rows — the case the old `rows != nil` guard inverted.
	var rows []repo.ProviderSetting
	for id, on := range f.enabled {
		rows = append(rows, repo.ProviderSetting{ID: id, Enabled: on})
	}
	return rows, nil
}
func (f *fakeProviderSettings) SetConfig(_ context.Context, id string, cfg json.RawMessage) error {
	f.configs[id] = cfg
	return nil
}
func (f *fakeProviderSettings) SetEnabled(_ context.Context, id string, on bool) error {
	f.enabled[id] = on
	return nil
}
func (f *fakeProviderSettings) SetPriority(context.Context, string, *int) error { return nil }
func (f *fakeProviderSettings) RecordSuccess(_ context.Context, id string) error {
	f.successes = append(f.successes, id)
	return nil
}
func (f *fakeProviderSettings) RecordError(_ context.Context, id, msg string) error {
	f.errs = append(f.errs, id+":"+msg)
	return nil
}

type fakeBookStore struct {
	updated   []model.Book
	coverMime string
	coverHash []byte
	updateErr error
}

func (f *fakeBookStore) UpdateMetadata(_ context.Context, b model.Book) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updated = append(f.updated, b)
	return nil
}
func (f *fakeBookStore) SetCover(_ context.Context, _ string, _ bool, mime string) error {
	f.coverMime = mime
	return nil
}
func (f *fakeBookStore) SetCoverHash(_ context.Context, _ string, hash []byte) error {
	f.coverHash = hash
	return nil
}

// SetFolderPath and RenameFolderTx round the fake out to
// BookMetadataWriter, so the same object can back the MetadataWriter the
// enrichment service now writes through. That is deliberate: assertions
// on f.updated therefore observe the DB step of the real pipeline, not a
// direct repo call that only ever existed in tests.
func (f *fakeBookStore) SetFolderPath(_ context.Context, _, _, _ string) error { return nil }
func (f *fakeBookStore) RenameFolderTx(_ context.Context, _ repo.RenameFolderTxArgs) error {
	return nil
}

type fakeCoverStore struct {
	saved   []byte
	mime    string
	deleted []string
}

func (f *fakeCoverStore) SaveBookHashed(_ []byte, mime string, data []byte) error {
	f.mime = mime
	f.saved = data
	return nil
}
func (f *fakeCoverStore) DeleteBook(id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

// stubHTTP serves one canned response, so cover tests never touch the network.
type stubHTTP struct {
	status int
	ctype  string
	body   []byte
	err    error
	calls  int
}

func (s *stubHTTP) Do(*http.Request) (*http.Response, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	h := http.Header{}
	h.Set("Content-Type", s.ctype)
	return &http.Response{
		StatusCode: s.status,
		Header:     h,
		Body:       io.NopCloser(bytes.NewReader(s.body)),
	}, nil
}

func newEnrichForTest(t *testing.T) (*EnrichmentService, *fakeBookStore, *fakeCoverStore) {
	t.Helper()
	books := &fakeBookStore{}
	covers := &fakeCoverStore{}
	writer, _ := newPipelineWriter(t, books, &recordingSidecarWriter{}, nil)
	svc := NewEnrichmentService(nil, newFakeProviderSettings(), books, covers, nil, writer)
	return svc, books, covers
}

// ---------------------------------------------------------------------------
// ApplyMatch — the lock-aware merge (ADR-0012's "empty-only" relies on this)
// ---------------------------------------------------------------------------

func TestApplyMatchFillsUnlockedFields(t *testing.T) {
	t.Parallel()
	svc, books, _ := newEnrichForTest(t)

	got, err := svc.ApplyMatch(context.Background(),
		model.Book{ID: "b1", Title: "old"},
		provider.Match{
			Title:       "Deep Work",
			Authors:     []string{"Cal Newport", "Someone Else"},
			Description: "focus",
			Publisher:   "Grand Central",
			Language:    "en",
			Year:        2016,
		}, ApplyOptions{}, TriggerManualEdit)
	if err != nil {
		t.Fatalf("ApplyMatch: %v", err)
	}

	if got.Title != "Deep Work" {
		t.Errorf("Title = %q, want Deep Work", got.Title)
	}
	if got.Author != "Cal Newport, Someone Else" {
		t.Errorf("Author = %q, want the joined list", got.Author)
	}
	if got.Year != 2016 || got.Language != "en" || got.Publisher != "Grand Central" {
		t.Errorf("unlocked fields not filled: %+v", got)
	}
	if len(books.updated) != 1 {
		t.Fatalf("expected one persisted write, got %d", len(books.updated))
	}
}

// A locked field must survive an incoming value — the invariant the whole
// per-field lock feature exists for.
func TestApplyMatchRespectsLocks(t *testing.T) {
	t.Parallel()
	svc, _, _ := newEnrichForTest(t)

	book := model.Book{ID: "b1", Title: "Keep Me", Author: "Mine"}
	book.Locks.Title = true
	book.Locks.Author = true

	got, err := svc.ApplyMatch(context.Background(), book,
		provider.Match{Title: "Overwrite", Authors: []string{"Someone"}},
		ApplyOptions{}, TriggerManualEdit)
	if err != nil {
		t.Fatalf("ApplyMatch: %v", err)
	}
	if got.Title != "Keep Me" || got.Author != "Mine" {
		t.Fatalf("locked fields were overwritten: %+v", got)
	}
}

// Providers hand back one ISBN slot; 13 digits route to ISBN, 10 to ISBN10,
// and each destination has its own lock so locking one doesn't block the other.
func TestApplyMatchRoutesISBNByDigitCount(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		isbn       string
		lockISBN   bool
		lockISBN10 bool
		wantISBN   string
		wantISBN10 string
	}{
		{name: "13 digits → ISBN", isbn: "978-1455586691", wantISBN: "978-1455586691"},
		{name: "10 digits → ISBN10", isbn: "1455586692", wantISBN10: "1455586692"},
		{name: "odd length falls back to ISBN", isbn: "12345", wantISBN: "12345"},
		{name: "locked ISBN blocks the 13 form", isbn: "978-1455586691", lockISBN: true},
		{name: "locked ISBN10 blocks the 10 form", isbn: "1455586692", lockISBN10: true},
		{
			name: "locking ISBN does not block ISBN10",
			isbn: "1455586692", lockISBN: true, wantISBN10: "1455586692",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc, _, _ := newEnrichForTest(t)
			book := model.Book{ID: "b1"}
			book.Locks.ISBN = tc.lockISBN
			book.Locks.ISBN10 = tc.lockISBN10

			got, err := svc.ApplyMatch(context.Background(), book,
				provider.Match{ISBN: tc.isbn}, ApplyOptions{}, TriggerManualEdit)
			if err != nil {
				t.Fatalf("ApplyMatch: %v", err)
			}
			if got.ISBN != tc.wantISBN {
				t.Errorf("ISBN = %q, want %q", got.ISBN, tc.wantISBN)
			}
			if got.ISBN10 != tc.wantISBN10 {
				t.Errorf("ISBN10 = %q, want %q", got.ISBN10, tc.wantISBN10)
			}
		})
	}
}

func TestApplyMatchMergesOrReplacesCategories(t *testing.T) {
	t.Parallel()

	t.Run("replace by default", func(t *testing.T) {
		t.Parallel()
		svc, _, _ := newEnrichForTest(t)
		got, err := svc.ApplyMatch(context.Background(),
			model.Book{ID: "b1", Genres: []string{"Existing"}},
			provider.Match{Categories: []string{"Productivity"}},
			ApplyOptions{}, TriggerManualEdit)
		if err != nil {
			t.Fatalf("ApplyMatch: %v", err)
		}
		if len(got.Genres) != 1 || got.Genres[0] != "Productivity" {
			t.Errorf("Genres = %v, want the incoming set only", got.Genres)
		}
	})

	t.Run("union when MergeCategories", func(t *testing.T) {
		t.Parallel()
		svc, _, _ := newEnrichForTest(t)
		got, err := svc.ApplyMatch(context.Background(),
			model.Book{ID: "b1", Genres: []string{"Existing"}},
			provider.Match{Categories: []string{"Productivity"}},
			ApplyOptions{MergeCategories: true}, TriggerManualEdit)
		if err != nil {
			t.Fatalf("ApplyMatch: %v", err)
		}
		if len(got.Genres) != 2 {
			t.Errorf("Genres = %v, want both retained", got.Genres)
		}
	})
}

// A failed persist must not report success — the caller would otherwise
// show the user metadata that was never stored.
func TestApplyMatchPropagatesWriteFailure(t *testing.T) {
	t.Parallel()
	books := &fakeBookStore{updateErr: errors.New("db down")}
	writer, _ := newPipelineWriter(t, books, &recordingSidecarWriter{}, nil)
	svc := NewEnrichmentService(nil, newFakeProviderSettings(), books, &fakeCoverStore{}, nil, writer)

	if _, err := svc.ApplyMatch(context.Background(), model.Book{ID: "b1"},
		provider.Match{Title: "x"}, ApplyOptions{}, TriggerManualEdit); err == nil {
		t.Fatal("want the write error surfaced, got nil")
	}
}

// ---------------------------------------------------------------------------
// Cover intake — the security gates
// ---------------------------------------------------------------------------

func TestImportCoverRejectsNonHTTPS(t *testing.T) {
	t.Parallel()
	svc, _, _ := newEnrichForTest(t)
	stub := &stubHTTP{status: 200, ctype: "image/jpeg", body: []byte("x")}
	svc.WithHTTPClient(stub)

	if _, err := svc.ImportCoverFromURL(context.Background(), "b1", "http://books.google.com/x.jpg"); err == nil {
		t.Fatal("plain http must be refused")
	}
	if stub.calls != 0 {
		t.Error("rejected URL must not be fetched")
	}
}

// The allow-list is what stops this endpoint becoming an open proxy.
func TestImportCoverRejectsHostOutsideAllowList(t *testing.T) {
	t.Parallel()
	svc, _, _ := newEnrichForTest(t)
	stub := &stubHTTP{status: 200, ctype: "image/jpeg", body: []byte("x")}
	svc.WithHTTPClient(stub)

	_, err := svc.ImportCoverFromURL(context.Background(), "b1", "https://evil.example.com/x.jpg")
	if err == nil {
		t.Fatal("host outside the allow-list must be refused")
	}
	if !strings.Contains(err.Error(), "evil.example.com") {
		t.Errorf("error should name the host so it can be audited: %v", err)
	}
	if stub.calls != 0 {
		t.Error("rejected host must not be fetched")
	}
}

func TestImportCoverAcceptsSuffixAllowListedHost(t *testing.T) {
	t.Parallel()
	svc, books, covers := newEnrichForTest(t)
	svc.WithHTTPClient(&stubHTTP{status: 200, ctype: "image/jpeg", body: []byte("jpegbytes")})

	// ".gr-assets.com" is a suffix entry — subdomains must be admitted.
	mime, err := svc.ImportCoverFromURL(context.Background(), "b1", "https://i.gr-assets.com/x.jpg")
	if err != nil {
		t.Fatalf("suffix-matched host should be accepted: %v", err)
	}
	if mime != "image/jpeg" {
		t.Errorf("mime = %q, want image/jpeg", mime)
	}
	if string(covers.saved) != "jpegbytes" {
		t.Errorf("cover bytes not stored: %q", covers.saved)
	}
	if books.coverMime != "image/jpeg" || len(books.coverHash) == 0 {
		t.Error("book cover metadata not stamped")
	}
	if len(covers.deleted) != 1 {
		t.Error("legacy id-keyed cover should be swept after a hashed save")
	}
}

func TestImportCoverRejectsNonImageContentType(t *testing.T) {
	t.Parallel()
	svc, _, covers := newEnrichForTest(t)
	svc.WithHTTPClient(&stubHTTP{status: 200, ctype: "text/html", body: []byte("<html>")})

	if _, err := svc.ImportCoverFromURL(context.Background(), "b1", "https://books.google.com/x"); err == nil {
		t.Fatal("non-image content type must be refused")
	}
	if covers.saved != nil {
		t.Error("nothing should be stored for a rejected content type")
	}
}

func TestImportCoverRejectsOversizeBody(t *testing.T) {
	t.Parallel()
	svc, _, covers := newEnrichForTest(t)
	svc.WithHTTPClient(&stubHTTP{
		status: 200, ctype: "image/png",
		body: bytes.Repeat([]byte("a"), maxCoverBytes+1),
	})

	if _, err := svc.ImportCoverFromURL(context.Background(), "b1", "https://books.google.com/x"); err == nil {
		t.Fatal("a body over the cap must be refused")
	}
	if covers.saved != nil {
		t.Error("oversize body must not be stored")
	}
}

func TestImportCoverRejectsNon200(t *testing.T) {
	t.Parallel()
	svc, _, _ := newEnrichForTest(t)
	svc.WithHTTPClient(&stubHTTP{status: 404, ctype: "image/jpeg"})

	if _, err := svc.ImportCoverFromURL(context.Background(), "b1", "https://books.google.com/x"); err == nil {
		t.Fatal("non-200 must be refused")
	}
}

// ---------------------------------------------------------------------------
// Result cache — documented behaviour, pinned
// ---------------------------------------------------------------------------

// Cached Search results exist to protect provider quota (Google Books is
// ~100 req/100s per IP). Note the asymmetry this pins: Search is cached,
// SearchStream deliberately is not, and editing provider config does not
// invalidate — an admin can see a stale match list for up to the TTL.
func TestCacheRoundTripAndExpiry(t *testing.T) {
	t.Parallel()
	svc, _, _ := newEnrichForTest(t)

	matches := []provider.Match{{Title: "Deep Work"}}
	svc.cachePut("k", matches)

	got, ok := svc.cacheGet("k")
	if !ok || len(got) != 1 || got[0].Title != "Deep Work" {
		t.Fatalf("fresh entry not returned: %v %v", got, ok)
	}

	// Age the entry past the TTL.
	svc.cacheMu.Lock()
	e := svc.cache["k"]
	e.at = time.Now().Add(-2 * enrichCacheTTL)
	svc.cache["k"] = e
	svc.cacheMu.Unlock()

	if _, ok := svc.cacheGet("k"); ok {
		t.Error("entry older than the TTL must not be served")
	}
}

func TestCacheKeyNormalisesQuery(t *testing.T) {
	t.Parallel()

	a := enrichCacheKey(provider.Query{Title: "Deep Work", Author: "Newport"})
	b := enrichCacheKey(provider.Query{Title: "  deep work  ", Author: "NEWPORT"})
	if a != b {
		t.Errorf("case/whitespace variants should share a key:\n  %q\n  %q", a, b)
	}

	if enrichCacheKey(provider.Query{Title: "A"}) == enrichCacheKey(provider.Query{Title: "B"}) {
		t.Error("different titles must not collide")
	}
}

// ---------------------------------------------------------------------------
// Category helpers
// ---------------------------------------------------------------------------

func TestCleanCategorySliceTrimsAndDrops(t *testing.T) {
	t.Parallel()
	got := cleanCategorySlice([]string{" Productivity ", "", "   ", "Focus"})
	if len(got) != 2 || got[0] != "Productivity" || got[1] != "Focus" {
		t.Errorf("got %v, want [Productivity Focus]", got)
	}
}

func TestMergeCategorySlicesUnionsOnExactMatch(t *testing.T) {
	t.Parallel()

	got := mergeCategorySlices([]string{"Focus", "Focus"}, []string{"Focus", "Productivity"})
	if len(got) != 2 || got[0] != "Focus" || got[1] != "Productivity" {
		t.Fatalf("got %v, want [Focus Productivity] — existing first, then new", got)
	}

	// Dedupe is exact-match, so case variants both survive. This matches
	// model.DedupTags, which is the codebase's convention for tag-ish
	// slices, so it is pinned rather than "fixed" here — but it does mean
	// two providers disagreeing on capitalisation yield two genres.
	both := mergeCategorySlices([]string{"Focus"}, []string{"focus"})
	if len(both) != 2 {
		t.Fatalf("got %v, want both case variants retained (exact-match dedupe)", both)
	}
}

func TestCountDigits(t *testing.T) {
	t.Parallel()
	if n := countDigits("978-1455586691"); n != 13 {
		t.Errorf("countDigits = %d, want 13 (hyphens ignored)", n)
	}
	if n := countDigits("none"); n != 0 {
		t.Errorf("countDigits = %d, want 0", n)
	}
}

// ---------------------------------------------------------------------------
// Provider health telemetry
// ---------------------------------------------------------------------------

// The writes are deliberately detached in production; the test waits for
// them rather than racing them.
func TestProviderHealthWritesAreRecorded(t *testing.T) {
	t.Parallel()
	settings := newFakeProviderSettings()
	books := &fakeBookStore{}
	writer, _ := newPipelineWriter(t, books, &recordingSidecarWriter{}, nil)
	svc := NewEnrichmentService(nil, settings, books, &fakeCoverStore{}, nil, writer)

	svc.recordProviderSuccess(provider.Source("googlebooks"))
	svc.recordProviderError(provider.Source("amazon"), errors.New("rate limited"))
	svc.awaitHealthWrites()

	if len(settings.successes) != 1 || settings.successes[0] != "googlebooks" {
		t.Errorf("successes = %v, want [googlebooks]", settings.successes)
	}
	if len(settings.errs) != 1 || !strings.Contains(settings.errs[0], "rate limited") {
		t.Errorf("errors = %v, want the amazon failure with its message", settings.errs)
	}
}
