package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// Hardcover queries https://api.hardcover.app/v1/graphql. Requires a
// personal API token — the provider is inert without one and the UI
// shows the "disabled until configured" hint.
type Hardcover struct {
	Client *http.Client

	mu    sync.RWMutex
	token string
}

type HardcoverConfig struct {
	// APIToken is a personal Hardcover API token (pasted from the
	// account settings page on hardcover.app). Sent as a Bearer header.
	APIToken string `json:"apiToken"`
}

func NewHardcover() *Hardcover {
	return &Hardcover{Client: defaultHTTPClient}
}

func (*Hardcover) Name() Source { return SourceHardcover }

func (h *Hardcover) Configure(raw []byte) error {
	var c HardcoverConfig
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &c); err != nil {
			return err
		}
	}
	h.mu.Lock()
	h.token = strings.TrimSpace(c.APIToken)
	h.mu.Unlock()
	return nil
}

func (*Hardcover) ConfigSchema() []ConfigField {
	return []ConfigField{
		{
			Key:         "apiToken",
			Label:       "API token",
			Kind:        ConfigFieldPassword,
			Placeholder: "hc_…",
			Help:        "Required. Generate one at hardcover.app → Account → API Access.",
		},
	}
}

// hardcoverSearchQuery is a minimal GraphQL query against the public
// `books` index. Fields picked to match our Match DTO — Hardcover
// exposes more (moods, reviews, series), but we only persist what the
// UI renders today. When detail-fetch lands (ASIN/HardcoverID walk)
// we'll grow this into a separate query.
const hardcoverSearchQuery = `
query Search($q: String!, $perPage: Int!) {
  search(query: $q, query_type: "books", per_page: $perPage) {
    results
  }
}
`

func (h *Hardcover) Search(ctx context.Context, q Query) ([]Match, error) {
	h.mu.RLock()
	token := h.token
	h.mu.RUnlock()
	// No token means we can't hit the API at all — bail silently so the
	// fan-out doesn't produce a noisy error frame.
	if token == "" {
		return nil, nil
	}

	query := buildHardcoverQuery(q)
	if query == "" {
		return nil, nil
	}

	body, err := json.Marshal(map[string]any{
		"query": hardcoverSearchQuery,
		"variables": map[string]any{
			"q":       query,
			"perPage": 5,
		},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.hardcover.app/v1/graphql", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hardcover %d", resp.StatusCode)
	}

	var wrapper struct {
		Data struct {
			Search struct {
				// Hardcover returns `results` as a JSON-encoded Meilisearch
				// response — opaque object with a `hits` array. Decode in
				// two passes so shape changes in the deep hit payload don't
				// wedge the outer envelope.
				Results json.RawMessage `json:"results"`
			} `json:"search"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("decode hardcover: %w", err)
	}
	if len(wrapper.Errors) > 0 {
		return nil, fmt.Errorf("hardcover: %s", wrapper.Errors[0].Message)
	}
	if len(wrapper.Data.Search.Results) == 0 {
		return nil, nil
	}

	var inner struct {
		Hits []struct {
			Document struct {
				ID          string   `json:"id"`
				Title       string   `json:"title"`
				Subtitle    string   `json:"subtitle"`
				AuthorNames []string `json:"author_names"`
				ReleaseYear int      `json:"release_year"`
				Description string   `json:"description"`
				ISBNs       []string `json:"isbns"`
				Image       struct {
					URL string `json:"url"`
				} `json:"image"`
				Genres []string `json:"genres"`
				Moods  []string `json:"moods"`
				Series []struct {
					Name string `json:"name"`
				} `json:"featured_series"`
			} `json:"document"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(wrapper.Data.Search.Results, &inner); err != nil {
		return nil, fmt.Errorf("decode hardcover hits: %w", err)
	}

	out := make([]Match, 0, len(inner.Hits))
	for _, hit := range inner.Hits {
		d := hit.Document
		m := Match{
			Source:      SourceHardcover,
			SourceID:    d.ID,
			Title:       strings.TrimSpace(d.Title),
			Authors:     d.AuthorNames,
			Description: d.Description,
			Year:        d.ReleaseYear,
			ISBN:        firstNonEmpty(d.ISBNs),
			Categories:  mergeHardcoverCategories(d.Genres, d.Moods),
			CoverURL:    strings.TrimSpace(d.Image.URL),
		}
		if len(d.Series) > 0 {
			m.Series = strings.TrimSpace(d.Series[0].Name)
		}
		m.Confidence = scoreMatch(q, m.Title, m.Authors)
		out = append(out, m)
	}
	return out, nil
}

// buildHardcoverQuery composes a freeform query string. Hardcover's
// search index is single-field so title + author are concatenated.
// ISBN wins when present — Hardcover indexes ISBNs too and a pure ISBN
// query is unambiguous.
func buildHardcoverQuery(q Query) string {
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

// mergeHardcoverCategories unions genres + moods, dedupes, caps to 8.
// Moods are slightly lower-signal than genres but useful filters, so
// we keep both; the UI can decide how to surface them.
func mergeHardcoverCategories(genres, moods []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(genres)+len(moods))
	for _, list := range [][]string{genres, moods} {
		for _, v := range list {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
			if len(out) >= 8 {
				return out
			}
		}
	}
	return out
}
