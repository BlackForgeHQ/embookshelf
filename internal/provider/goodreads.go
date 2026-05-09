// SPDX-License-Identifier: AGPL-3.0-or-later

package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// GoodReads scrapes https://www.goodreads.com/search?q=… and walks the
// listing's `<tr itemtype=".../Book">` rows. There is no public API —
// the internal one got killed in 2020 — so this is an explicit, narrow
// DOM-level integration. Goodreads blocks aggressive crawlers; the
// EnrichmentService's per-provider cooldown is what keeps this polite.
//
// ErrGoodreadsBlocked is returned on 403 / 429 so the UI can surface
// the right message ("Goodreads rate-limited — try again later")
// instead of a generic "provider failed".
type GoodReads struct {
	Client *http.Client
}

func NewGoodReads(client *http.Client) *GoodReads {
	return &GoodReads{Client: client}
}

func (*GoodReads) Name() Source { return SourceGoodReads }

func (g *GoodReads) Search(ctx context.Context, q Query) ([]Match, error) {
	query := buildGoodreadsQuery(q)
	if query == "" {
		return nil, nil
	}
	u := fmt.Sprintf(
		"https://www.goodreads.com/search?q=%s&search_type=books",
		url.QueryEscape(query),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	// Goodreads returns stripped-down HTML to unauthenticated curl
	// user-agents; a plausible browser string avoids that downgrade.
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (embookshelf metadata fetch; +https://github.com/blackforge/embookshelf)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := g.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	// 403/429 both mean "try later"; signal via a 429-substring error
	// so the service's cooldown kicks in.
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("goodreads 429")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("goodreads %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse goodreads: %w", err)
	}

	var out []Match
	doc.Find("tr[itemtype$='/Book']").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if len(out) >= 5 {
			return false
		}
		titleAnchor := s.Find("a.bookTitle").First()
		title := cleanText(titleAnchor.Find("span[itemprop=name]").Text())
		if title == "" {
			title = cleanText(titleAnchor.Text())
		}
		if title == "" {
			return true
		}
		href, _ := titleAnchor.Attr("href")
		id := extractGoodreadsID(href)

		// Authors sit under `a.authorName span[itemprop=name]` — there
		// can be several (co-authors, "editor" roles, …). We pick the
		// plain-name entries and drop role suffixes.
		var authors []string
		s.Find("a.authorName span[itemprop=name]").Each(func(_ int, a *goquery.Selection) {
			name := cleanText(a.Text())
			if name != "" {
				authors = append(authors, name)
			}
		})

		cover, _ := s.Find("img.bookCover").First().Attr("src")
		cover = strings.TrimSpace(cover)
		// Goodreads serves tiny thumbs by default ("…SY75…"); request
		// the large variant by scrubbing the size marker.
		cover = upgradeGoodreadsCover(cover)

		// Publish year lives inside a free-text span like
		// "published 2013". Extract the first 4-digit number.
		var year int
		s.Find("span.greyText.smallText.uitext").Each(func(_ int, txt *goquery.Selection) {
			if y := yearFromText(txt.Text()); y != 0 {
				year = y
			}
		})

		m := Match{
			Source:   SourceGoodReads,
			SourceID: id,
			Title:    title,
			Authors:  authors,
			Year:     year,
			CoverURL: cover,
		}
		m.Confidence = scoreMatch(q, m.Title, m.Authors)
		out = append(out, m)
		return true
	})
	return out, nil
}

func buildGoodreadsQuery(q Query) string {
	if isbn := strings.TrimSpace(q.ISBN); isbn != "" {
		return isbn
	}
	var parts []string
	if t := strings.TrimSpace(q.Title); t != "" {
		parts = append(parts, t)
	}
	if a := strings.TrimSpace(q.Author); a != "" {
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

// extractGoodreadsID pulls the numeric book id out of "/book/show/41-title".
// We strip the slug so the stored id is stable across title renames.
func extractGoodreadsID(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	// Strip query string.
	if i := strings.Index(href, "?"); i >= 0 {
		href = href[:i]
	}
	const prefix = "/book/show/"
	i := strings.Index(href, prefix)
	if i < 0 {
		return ""
	}
	rest := href[i+len(prefix):]
	// Numeric id ends at the first '-' or end-of-string.
	if dash := strings.Index(rest, "-"); dash >= 0 {
		rest = rest[:dash]
	}
	return rest
}

// upgradeGoodreadsCover rewrites the thumbnail size marker
// (`_S{Y,X}75_`) to a larger one when present. Falls through unchanged
// for URLs that don't match the pattern — we don't want to mangle
// anything we can't parse.
func upgradeGoodreadsCover(u string) string {
	if u == "" {
		return u
	}
	// Common forms: `compressed.photo.goodreads.com/books/1234l/…._SY75_.jpg`.
	for _, marker := range []string{"._SY75_", "._SX50_", "._SY160_", "._SX98_"} {
		if strings.Contains(u, marker) {
			return strings.Replace(u, marker, "._SY475_", 1)
		}
	}
	return u
}

func yearFromText(s string) int {
	fields := strings.Fields(s)
	for _, f := range fields {
		f = strings.Trim(f, "(),.")
		if len(f) != 4 {
			continue
		}
		n, err := strconv.Atoi(f)
		if err != nil {
			continue
		}
		if n > 1000 && n < 3000 {
			return n
		}
	}
	return 0
}

func cleanText(s string) string {
	s = strings.TrimSpace(s)
	// Collapse runs of whitespace (newlines inside <td>s are common).
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}
