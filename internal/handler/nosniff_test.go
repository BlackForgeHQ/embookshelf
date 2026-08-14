// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/coverstore"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/staticfs"
)

// Cover bytes come out of book files whose declared image type is chosen
// by whoever wrote the file, so the Content-Type on a cover response is
// only ever as trustworthy as the sniff behind it. nosniff is the second
// half of that: it stops a browser from re-typing the response body and
// rendering a cover as a document.

// TestCoverResponseCarriesNosniff pins the header on a real cover
// response — bytes on the wire, driven through the whole engine.
func TestCoverResponseCarriesNosniff(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	book := model.Book{ID: "b1", HasCover: true, CoverMime: "image/jpeg"}
	// Legacy id-keyed namespace: books/<id> is what Open falls back to
	// when the row has no cover_hash.
	if err := os.MkdirAll(filepath.Join(dir, "books"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	jpeg := []byte("\xff\xd8\xff\xe0jpeg")
	if err := os.WriteFile(filepath.Join(dir, "books", book.ID), jpeg, 0o644); err != nil {
		t.Fatalf("write cover: %v", err)
	}

	h := &Handler{
		cfg:    config.Config{AllowedOrigins: []string{"http://localhost:5173"}},
		covers: coverstore.New(dir),
		books:  &fakeBookStore{book: book},
	}
	engine := h.Engine()

	// The OPDS twin of this route (/opds/cover/:id) authenticates with
	// HTTP Basic against a real user store, so it is pinned at the header
	// level by TestNosniffOnEveryResponse rather than with bytes here.
	for _, target := range []string{"/api/v1/books/b1/cover"} {
		t.Run(target, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, target, nil)
			r.Header.Set("Origin", "http://"+r.Host)
			u := model.User{ID: "u1", Role: model.RoleUser}
			r = r.WithContext(auth.WithUser(r.Context(), &u))
			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, r)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
			if got := rec.Header().Get("Content-Type"); got != "image/jpeg" {
				t.Errorf("Content-Type = %q, want image/jpeg", got)
			}
			if rec.Body.String() != string(jpeg) {
				t.Errorf("body = %q, want the cover bytes", rec.Body.String())
			}
		})
	}
}

// The stored type is sniffed from the bytes since #330, but rows written
// before that — and rows carried in by import-sqlite — still hold
// whatever a manifest or an ID3 frame claimed. The cover routes refuse to
// repeat a claim they cannot vouch for.
func TestCoverContentTypeRefusesWhatItCannotVouchFor(t *testing.T) {
	cases := []struct{ stored, want string }{
		{"image/jpeg", "image/jpeg"},
		{"image/png", "image/png"},
		{"image/gif", "image/gif"},
		{"image/webp", "image/webp"},
		{"image/avif", "image/avif"}, // a raster type this codebase doesn't sniff, still safe to serve
		{"IMAGE/JPEG", "image/jpeg"},
		{" image/jpeg ", "image/jpeg"},
		{"", "application/octet-stream"},
		{"text/html", "application/octet-stream"},
		{"application/xhtml+xml", "application/octet-stream"},
		// SVG is an image type and a scriptable document; nosniff does
		// not defuse it, so it is served as a download.
		{"image/svg+xml", "application/octet-stream"},
	}
	for _, tc := range cases {
		if got := coverContentType(tc.stored); got != tc.want {
			t.Errorf("coverContentType(%q) = %q, want %q", tc.stored, got, tc.want)
		}
	}
}

// TestNosniffOnEveryResponse pins the header as a property of the engine
// rather than of one route: it is set by middleware, so a cover route
// added tomorrow inherits it whether or not its author remembers.
func TestNosniffOnEveryResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{cfg: config.Config{AllowedOrigins: []string{"http://localhost:5173"}}, static: staticfs.FS}
	engine := h.Engine()

	targets := []string{
		"/api/v1/healthcheck",
		"/api/v1/books/b1/cover", // 401 without a session; the header still lands
		"/api/v1/bookdrop/b1/cover",
		"/opds/cover/b1",
		"/",
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, target, nil)
			r.Header.Set("Origin", "http://"+r.Host)
			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, r)
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("%s: X-Content-Type-Options = %q, want nosniff (status %d)", target, got, rec.Code)
			}
		})
	}
}

// nosniff is only safe over the embedded SPA if the file server types
// every asset correctly — a script served as text/plain stops executing
// the moment the browser is told not to guess. This walks the embedded
// bundle and checks the types the shell depends on.
//
// It fails rather than skips when the bundle is missing or holds no
// script or stylesheet. The decision to set nosniff globally rests on
// this check; a version of it that quietly passes on an empty
// internal/staticfs/dist would let that decision go unexamined in
// exactly the situation — a bundle that did not build — where it is
// least obvious.
func TestSPAAssetsAreTypedCorrectlyUnderNosniff(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{cfg: config.Config{AllowedOrigins: []string{"http://localhost:5173"}}, static: staticfs.FS}
	engine := h.Engine()

	dist, err := staticfs.FS.ReadDir("dist/assets")
	if err != nil {
		t.Fatalf("no embedded bundle to check: %v — run `make ui-build`", err)
	}
	var js, css string
	for _, e := range dist {
		switch {
		case js == "" && strings.HasSuffix(e.Name(), ".js"):
			js = "/assets/" + e.Name()
		case css == "" && strings.HasSuffix(e.Name(), ".css"):
			css = "/assets/" + e.Name()
		}
	}
	if js == "" || css == "" {
		t.Fatalf("embedded bundle has no script (%q) or stylesheet (%q) to type-check", js, css)
	}

	cases := []struct{ target, wantPrefix string }{
		{"/", "text/html"}, // the shell; /index.html itself 301s to it
		{js, "text/javascript"},
		{css, "text/css"},
	}
	for _, tc := range cases {
		t.Run(tc.target, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.target, nil)
			r.Header.Set("Origin", "http://"+r.Host)
			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, r)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, tc.wantPrefix) {
				t.Errorf("Content-Type = %q, want prefix %q — nosniff would break this asset", got, tc.wantPrefix)
			}
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
		})
	}
}
