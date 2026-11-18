// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

// exportFixture mirrors the shape of the UI's static export: a page per
// route as <name>.html, the router's payloads beside it, and dynamic
// routes prerendered once under the `_` placeholder.
func exportFixture() fstest.MapFS {
	file := &fstest.MapFile{Data: []byte("x")}
	return fstest.MapFS{
		"index.html":                        file,
		"index.txt":                         file,
		"404.html":                          &fstest.MapFile{Data: []byte("not found page")},
		"library.html":                      file,
		"library.txt":                       file,
		"library/__next._tree.txt":          file,
		"login.html":                        file,
		"login/pending.html":                file,
		"book/_.html":                       file,
		"book/_.txt":                        file,
		"book/_/__next._tree.txt":           file,
		"book/_/edit.html":                  file,
		"book/_/edit.txt":                   file,
		"book/_/edit/__next._tree.txt":      file,
		"read/_.html":                       file,
		"_next/static/chunks/app.js":        file,
		"_next/static/media/pdf.worker.mjs": file,
		"favicon.ico":                       file,
	}
}

func TestResolveExportPath(t *testing.T) {
	fsys := exportFixture()
	cases := []struct {
		path string
		want string
	}{
		{"/", "index.html"},
		{"/index.txt", "index.txt"},
		{"/library", "library.html"},
		{"/library/__next._tree.txt", "library/__next._tree.txt"},
		{"/login", "login.html"},
		{"/login/pending", "login/pending.html"},
		{"/favicon.ico", "favicon.ico"},
		{"/_next/static/chunks/app.js", "_next/static/chunks/app.js"},
		// Dynamic routes: any id resolves to the placeholder page.
		{"/book/01HZY3", "book/_.html"},
		{"/book/01HZY3/edit", "book/_/edit.html"},
		{"/book/01HZY3/edit.txt", "book/_/edit.txt"},
		{"/read/01HZY3", "read/_.html"},
		// …and so do the payloads a client-side navigation asks for.
		{"/book/01HZY3.txt", "book/_.txt"},
		{"/book/01HZY3/__next._tree.txt", "book/_/__next._tree.txt"},
		{"/book/01HZY3/edit/__next._tree.txt", "book/_/edit/__next._tree.txt"},
		// Cleaned before it is walked: nothing escapes the export.
		{"/library/../login", "login.html"},
		{"/library/", "library.html"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got, ok := resolveExportPath(fsys, tc.path)
			if !ok || got != tc.want {
				t.Errorf("resolveExportPath(%q) = %q, %v; want %q, true", tc.path, got, ok, tc.want)
			}
		})
	}
}

func TestResolveExportPathMisses(t *testing.T) {
	fsys := exportFixture()
	for _, p := range []string{
		"/nope",
		"/library/nope",     // a static route has no placeholder to fall back on
		"/book/01HZY3/nope", // nor does a dynamic route's unknown child
		"/_next/static/chunks/x.js",
		"/api/v1/unknown",
	} {
		t.Run(p, func(t *testing.T) {
			if got, ok := resolveExportPath(fsys, p); ok {
				t.Errorf("resolveExportPath(%q) = %q, want no match", p, got)
			}
		})
	}
}

func TestSPAServesExportPagesAndA404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.NoRoute(serveExport(exportFixture()))

	cases := []struct {
		method, path string
		wantCode     int
		wantBody     string
	}{
		{http.MethodGet, "/book/01HZY3", http.StatusOK, "x"},
		{http.MethodGet, "/library", http.StatusOK, "x"},
		{http.MethodGet, "/nope", http.StatusNotFound, "not found page"},
		{http.MethodHead, "/nope", http.StatusNotFound, ""},
		{http.MethodPost, "/library", http.StatusMethodNotAllowed, ""},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			if got := rec.Body.String(); got != tc.wantBody {
				t.Errorf("body = %q, want %q", got, tc.wantBody)
			}
		})
	}
}
