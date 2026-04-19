package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// DuckDuckGo queries the public Instant Answer API
// (https://api.duckduckgo.com/?q=…&format=json). It's free, no key,
// CORS-allowed, and ToS-friendly — the trade-off is sparse coverage:
// most queries return an empty Abstract. For famous books it surfaces
// the Wikipedia summary + a cover image.
type DuckDuckGo struct {
	Client *http.Client
}

func NewDuckDuckGo() *DuckDuckGo {
	return &DuckDuckGo{Client: defaultHTTPClient}
}

func (*DuckDuckGo) Name() Source { return SourceDuckDuckGo }

func (d *DuckDuckGo) Search(ctx context.Context, q Query) ([]Match, error) {
	queryStr := buildDDGQuery(q)
	if queryStr == "" {
		return nil, nil
	}
	u := fmt.Sprintf(
		"https://api.duckduckgo.com/?q=%s&format=json&no_html=1&no_redirect=1&t=embookshelf",
		url.QueryEscape(queryStr),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := d.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("duckduckgo %d", resp.StatusCode)
	}

	var body struct {
		Heading      string `json:"Heading"`
		AbstractText string `json:"AbstractText"`
		Image        string `json:"Image"`
		AbstractURL  string `json:"AbstractURL"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode duckduckgo: %w", err)
	}

	// DDG returns an empty Abstract for most book queries; bail out
	// quietly so the merged results don't carry noise.
	if strings.TrimSpace(body.AbstractText) == "" && strings.TrimSpace(body.Image) == "" {
		return nil, nil
	}

	title := strings.TrimSpace(body.Heading)
	if title == "" {
		title = strings.TrimSpace(q.Title)
	}
	cover := absolutizeDDGImage(body.Image)

	m := Match{
		Source:      SourceDuckDuckGo,
		SourceID:    body.AbstractURL,
		Title:       title,
		Description: body.AbstractText,
		CoverURL:    cover,
	}
	// Authors is left empty — DDG doesn't model book authors directly.
	// Confidence is intentionally below scoreMatch's normal output so
	// DDG sits beneath any provider that found a real metadata hit.
	m.Confidence = scoreMatch(q, m.Title, nil)
	if m.Confidence > 50 {
		m.Confidence = 50
	}
	return []Match{m}, nil
}

// buildDDGQuery composes the Instant Answer query string. Adding "book"
// nudges DDG toward the right Wikipedia article when the title is
// generic.
func buildDDGQuery(q Query) string {
	var parts []string
	if t := strings.TrimSpace(q.Title); t != "" {
		parts = append(parts, t)
	}
	if a := strings.TrimSpace(q.Author); a != "" {
		parts = append(parts, a)
	}
	if len(parts) == 0 {
		return ""
	}
	parts = append(parts, "book")
	return strings.Join(parts, " ")
}

// absolutizeDDGImage prefixes DDG's relative image paths (`/i/...`)
// with the duckduckgo.com host so the cover-fetch sandbox can verify
// the host against the allow-list.
func absolutizeDDGImage(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	if strings.HasPrefix(raw, "//") {
		return "https:" + raw
	}
	return "https://duckduckgo.com" + raw
}
