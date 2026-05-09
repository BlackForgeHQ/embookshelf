// SPDX-License-Identifier: AGPL-3.0-or-later

package email

import (
	"bytes"
	"embed"
	"fmt"
	ht "html/template"
	tt "text/template"
)

//go:embed templates/*.html templates/*.txt
var templatesFS embed.FS

// Templates is the parse-once-at-boot bundle of HTML+plaintext
// templates. New constructs it; the result is safe for concurrent
// rendering.
type Templates struct {
	html *ht.Template
	text *tt.Template
}

// NewTemplates parses every embedded template once. Returns an error
// if a parse fails — callers must not fall back to a runtime parse
// per send.
func NewTemplates() (*Templates, error) {
	html, err := ht.New("").ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse html templates: %w", err)
	}
	text, err := tt.New("").ParseFS(templatesFS, "templates/*.txt")
	if err != nil {
		return nil, fmt.Errorf("parse text templates: %w", err)
	}
	return &Templates{html: html, text: text}, nil
}

// Render executes the named .html and .txt templates with data and
// returns the rendered strings. name is the basename without
// extension (e.g. "password_reset").
func (t *Templates) Render(name string, data any) (text string, html string, err error) {
	var textBuf, htmlBuf bytes.Buffer
	if err := t.text.ExecuteTemplate(&textBuf, name+".txt", data); err != nil {
		return "", "", fmt.Errorf("text %s: %w", name, err)
	}
	if err := t.html.ExecuteTemplate(&htmlBuf, name+".html", data); err != nil {
		return "", "", fmt.Errorf("html %s: %w", name, err)
	}
	return textBuf.String(), htmlBuf.String(), nil
}
