// SPDX-License-Identifier: AGPL-3.0-or-later

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// OpenLibrary queries https://openlibrary.org/search.json. No key required.
//
// We prefer the search endpoint over /works because it takes title/author
// separately and ranks results by a relevance score.
type OpenLibrary struct {
	Client     *http.Client
	MaxResults int
}

func NewOpenLibrary(client *http.Client) *OpenLibrary {
	return &OpenLibrary{Client: client, MaxResults: 5}
}

func (*OpenLibrary) Name() Source { return SourceOpenLibrary }

func (o *OpenLibrary) Search(ctx context.Context, q Query) ([]Match, error) {
	params := url.Values{}
	if strings.TrimSpace(q.ISBN) != "" {
		params.Set("isbn", q.ISBN)
	} else {
		if t := strings.TrimSpace(q.Title); t != "" {
			params.Set("title", t)
		}
		if a := strings.TrimSpace(q.Author); a != "" {
			params.Set("author", a)
		}
	}
	if len(params) == 0 {
		return nil, nil
	}
	max := o.MaxResults
	if max <= 0 {
		max = 5
	}
	params.Set("limit", fmt.Sprintf("%d", max))

	u := "https://openlibrary.org/search.json?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := o.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("open library %d", resp.StatusCode)
	}

	var body struct {
		Docs []struct {
			Key              string   `json:"key"`
			Title            string   `json:"title"`
			Subtitle         string   `json:"subtitle"`
			AuthorName       []string `json:"author_name"`
			FirstPublishYear int      `json:"first_publish_year"`
			ISBN             []string `json:"isbn"`
			Publisher        []string `json:"publisher"`
			CoverI           int      `json:"cover_i"`
			Subject          []string `json:"subject"`
			Language         []string `json:"language"`
		} `json:"docs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode open library: %w", err)
	}

	out := make([]Match, 0, len(body.Docs))
	for _, d := range body.Docs {
		m := Match{
			Source:     SourceOpenLibrary,
			SourceID:   d.Key,
			Title:      d.Title,
			Authors:    d.AuthorName,
			Year:       d.FirstPublishYear,
			ISBN:       firstNonEmpty(d.ISBN),
			Publisher:  firstNonEmpty(d.Publisher),
			Categories: trimTo(d.Subject, 6),
			Language:   firstNonEmpty(d.Language),
		}
		if d.CoverI > 0 {
			m.CoverURL = fmt.Sprintf("https://covers.openlibrary.org/b/id/%d-L.jpg", d.CoverI)
		}
		m.Confidence = scoreMatch(q, m.Title, m.Authors)
		out = append(out, m)
	}
	return out, nil
}

func firstNonEmpty(ss []string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func trimTo(ss []string, n int) []string {
	if len(ss) <= n {
		return ss
	}
	return ss[:n]
}
