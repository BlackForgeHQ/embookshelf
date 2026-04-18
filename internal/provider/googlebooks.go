package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// GoogleBooks queries https://www.googleapis.com/books/v1/volumes.
// No API key is required for low-volume anonymous use (~1000 req/day).
type GoogleBooks struct {
	Client *http.Client
	// MaxResults caps how many hits the server returns. Google accepts up to
	// 40; 5 is plenty for a disambiguation picker.
	MaxResults int
}

func NewGoogleBooks() *GoogleBooks {
	return &GoogleBooks{Client: defaultHTTPClient, MaxResults: 5}
}

func (*GoogleBooks) Name() Source { return SourceGoogleBooks }

func (g *GoogleBooks) Search(ctx context.Context, q Query) ([]Match, error) {
	queryStr := buildGoogleQuery(q)
	if queryStr == "" {
		return nil, nil
	}
	max := g.MaxResults
	if max <= 0 {
		max = 5
	}
	u := fmt.Sprintf(
		"https://www.googleapis.com/books/v1/volumes?q=%s&maxResults=%d&printType=books",
		url.QueryEscape(queryStr), max,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := g.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google books %d", resp.StatusCode)
	}

	var body struct {
		Items []struct {
			ID         string `json:"id"`
			VolumeInfo struct {
				Title         string   `json:"title"`
				Subtitle      string   `json:"subtitle"`
				Authors       []string `json:"authors"`
				Publisher     string   `json:"publisher"`
				PublishedDate string   `json:"publishedDate"`
				Description   string   `json:"description"`
				PageCount     int      `json:"pageCount"`
				Categories    []string `json:"categories"`
				Language      string   `json:"language"`
				ImageLinks    struct {
					Thumbnail      string `json:"thumbnail"`
					SmallThumbnail string `json:"smallThumbnail"`
				} `json:"imageLinks"`
				IndustryIdentifiers []struct {
					Type       string `json:"type"`
					Identifier string `json:"identifier"`
				} `json:"industryIdentifiers"`
			} `json:"volumeInfo"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode google books: %w", err)
	}

	out := make([]Match, 0, len(body.Items))
	for _, it := range body.Items {
		vi := it.VolumeInfo
		m := Match{
			Source:      SourceGoogleBooks,
			SourceID:    it.ID,
			Title:       vi.Title,
			Authors:     vi.Authors,
			Description: vi.Description,
			Publisher:   vi.Publisher,
			Year:        yearFromDate(vi.PublishedDate),
			ISBN:        pickISBN(vi.IndustryIdentifiers),
			Categories:  vi.Categories,
			Language:    vi.Language,
			CoverURL:    preferHTTPS(vi.ImageLinks.Thumbnail),
		}
		m.Confidence = scoreMatch(q, m.Title, m.Authors)
		out = append(out, m)
	}
	return out, nil
}

func buildGoogleQuery(q Query) string {
	var parts []string
	if strings.TrimSpace(q.ISBN) != "" {
		// ISBN alone is the strongest signal — other fields just add noise.
		return "isbn:" + q.ISBN
	}
	if t := strings.TrimSpace(q.Title); t != "" {
		parts = append(parts, "intitle:"+quoteIfSpaces(t))
	}
	if a := strings.TrimSpace(q.Author); a != "" {
		parts = append(parts, "inauthor:"+quoteIfSpaces(a))
	}
	return strings.Join(parts, "+")
}

func quoteIfSpaces(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}

func yearFromDate(s string) int {
	if len(s) < 4 {
		return 0
	}
	n, err := strconv.Atoi(s[:4])
	if err != nil {
		return 0
	}
	return n
}

func pickISBN(ids []struct {
	Type       string `json:"type"`
	Identifier string `json:"identifier"`
}) string {
	// Prefer ISBN_13 over ISBN_10.
	var ten string
	for _, id := range ids {
		if id.Type == "ISBN_13" {
			return id.Identifier
		}
		if id.Type == "ISBN_10" {
			ten = id.Identifier
		}
	}
	return ten
}

func preferHTTPS(u string) string {
	// Google serves http:// URLs by default — bump to https so mixed-content
	// warnings don't kick in when embookshelf runs behind TLS.
	return strings.Replace(u, "http://", "https://", 1)
}
