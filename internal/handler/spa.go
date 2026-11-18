// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

// exportPlaceholder is the segment every dynamic route is prerendered
// for. The UI's static export holds book/_.html, book/_/edit.html,
// read/_.html and so on; any real id resolves to those files and the page
// reads the id back out of the URL. It must match PLACEHOLDER_ID in
// ui/src/lib/routes.ts.
const exportPlaceholder = "_"

// mountSPA serves the UI, a Next.js static export, from the embedded
// filesystem. Every unmatched GET is resolved against the export by
// resolveExportPath; a path the export has no page for gets its 404 page.
func (h *Handler) mountSPA(r *gin.Engine) {
	dist, err := fs.Sub(h.static, "dist")
	if err != nil {
		return
	}
	r.NoRoute(serveExport(dist))
}

func serveExport(dist fs.FS) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Status(http.StatusMethodNotAllowed)
			return
		}
		if name, ok := resolveExportPath(dist, c.Request.URL.Path); ok {
			// ServeFileFS types the response from the extension, which
			// nosniff depends on, and keeps net/http's redirect of
			// /index.html to /.
			http.ServeFileFS(c.Writer, c.Request, dist, name)
			return
		}
		serveNotFound(c, dist)
	}
}

// resolveExportPath maps a request path to the file in the static export
// that answers it, segment by segment:
//
//   - a segment that exists in the export is taken as is;
//   - otherwise, where the directory holds a placeholder, the placeholder
//     stands in for it (/book/42/edit → book/_/edit.html);
//   - the last segment may also name a page by its .html file
//     (/library → library.html).
//
// The same walk answers the router's payload requests for a client-side
// navigation (/book/42/__next._tree.txt → book/_/__next._tree.txt), so
// moving between books never falls back to a full page load.
func resolveExportPath(fsys fs.FS, urlPath string) (string, bool) {
	clean := strings.Trim(path.Clean("/"+urlPath), "/")
	if clean == "" {
		return "index.html", isFile(fsys, "index.html")
	}
	segs := strings.Split(clean, "/")
	dir := ""
	for _, seg := range segs[:len(segs)-1] {
		switch {
		case isDir(fsys, path.Join(dir, seg)):
			dir = path.Join(dir, seg)
		case isDir(fsys, path.Join(dir, exportPlaceholder)):
			dir = path.Join(dir, exportPlaceholder)
		default:
			return "", false
		}
	}

	last := segs[len(segs)-1]
	candidates := []string{last, last + ".html"}
	// A placeholder keeps the extension it stands in for: a client-side
	// navigation asks for /book/42.txt, and the answer is book/_.txt, not
	// the page.
	if ext := path.Ext(last); ext != "" {
		candidates = append(candidates, exportPlaceholder+ext)
	}
	candidates = append(candidates, exportPlaceholder+".html")
	for _, name := range candidates {
		if full := path.Join(dir, name); isFile(fsys, full) {
			return full, true
		}
	}
	return "", false
}

func serveNotFound(c *gin.Context, dist fs.FS) {
	page, err := dist.Open("404.html")
	if err != nil {
		c.String(http.StatusNotFound, "frontend bundle not built — run `make ui-build`")
		return
	}
	defer func() { _ = page.Close() }()
	c.Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	c.Writer.WriteHeader(http.StatusNotFound)
	// Committed explicitly: a HEAD writes no body, and Gin would otherwise
	// append its own 404 text to an unwritten response.
	c.Writer.WriteHeaderNow()
	if c.Request.Method == http.MethodGet {
		_, _ = io.Copy(c.Writer, page)
	}
}

func isFile(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}

func isDir(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && info.IsDir()
}
